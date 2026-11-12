package snmp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// bulkValuesPerRequest is roughly how many values each GetBulk asks for, shared between the columns being walked
const bulkValuesPerRequest = 50

type columnCursor struct {
	column int
	prefix string
	last   string
}

// WalkTable reads the given columns of an SNMP table into rows keyed by their index. Only the columns that are used are
// requested, because some agents answer slowly per value (Juniper EX switches take minutes to walk every column of the
// interface tables). The columns are walked side by side: one row per request on SNMPv1, and several rows per request
// with GetBulk on v2c and v3.
func WalkTable(client *gosnmp.GoSNMP, entryOID string, columns ...int) (map[string]Row, error) {
	rows := make(map[string]Row)
	active := make([]*columnCursor, 0, len(columns))

	for _, column := range columns {
		columnOID := entryOID + "." + strconv.Itoa(column)
		active = append(active, &columnCursor{column: column, prefix: columnOID + ".", last: columnOID})
	}

	for len(active) > 0 {
		oids := make([]string, len(active))

		for i, cursor := range active {
			oids[i] = cursor.last
		}

		var (
			packet *gosnmp.SnmpPacket
			err    error
		)

		if client.Version == gosnmp.Version1 {
			packet, err = client.GetNext(oids)
		} else {
			packet, err = client.GetBulk(oids, 0, uint32(max(1, bulkValuesPerRequest/len(active))))
		}

		if err != nil {
			return rows, err
		}

		// SNMPv1 has no end-of-MIB marker, so an agent fails the whole request with noSuchName and points at the
		// column that ran past the end of everything it serves. That column is finished and the rest are retried.
		if packet.Error == gosnmp.NoSuchName && client.Version == gosnmp.Version1 {
			index := int(packet.ErrorIndex) - 1

			if index < 0 || index >= len(active) {
				return rows, nil
			}

			active = append(active[:index], active[index+1:]...)

			continue
		}

		if packet.Error != gosnmp.NoError {
			return rows, fmt.Errorf("WalkTable: agent responded with error %s", packet.Error)
		}

		if len(packet.Variables) < 1 {
			return rows, nil
		}

		// Variables repeat the requested columns in order, so each belongs to the column at its position modulo the
		// number of columns. A column is finished once a value leaves it, or the agent stops moving forward.
		finished := make([]bool, len(active))

		for i, pdu := range packet.Variables {
			position := i % len(active)

			if finished[position] {
				continue
			}

			cursor := active[position]
			name := strings.TrimPrefix(pdu.Name, ".")
			index, ok := strings.CutPrefix(name, cursor.prefix)

			if !ok || pdu.Type == gosnmp.EndOfMibView || pdu.Type == gosnmp.NoSuchObject || pdu.Type == gosnmp.NoSuchInstance || CompareOIDs(name, cursor.last) <= 0 {
				finished[position] = true

				continue
			}

			if rows[index] == nil {
				rows[index] = make(Row)
			}

			rows[index][cursor.column] = pdu
			cursor.last = name
		}

		remaining := active[:0]

		for i, cursor := range active {
			if !finished[i] {
				remaining = append(remaining, cursor)
			}
		}

		active = remaining
	}

	return rows, nil
}

// CompareOIDs orders two dotted OIDs numerically, the order agents return them in
func CompareOIDs(a, b string) int {
	aParts, bParts := strings.Split(a, "."), strings.Split(b, ".")

	for i := range min(len(aParts), len(bParts)) {
		aValue, _ := strconv.ParseUint(aParts[i], 10, 64)
		bValue, _ := strconv.ParseUint(bParts[i], 10, 64)

		if aValue != bValue {
			if aValue < bValue {
				return -1
			}

			return 1
		}
	}

	return len(aParts) - len(bParts)
}

// SortedIndexes returns the indexes of table rows in the order the agent returns them
func SortedIndexes(rows map[string]Row) []string {
	indexes := make([]string, 0, len(rows))

	for index := range rows {
		indexes = append(indexes, index)
	}

	slices.SortFunc(indexes, CompareOIDs)

	return indexes
}
