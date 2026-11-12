package snmp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// bulkValuesPerRequest is the most values one GetBulk asks for, shared between the columns walked side by side
const bulkValuesPerRequest = 50

type columnCursor struct {
	column int
	prefix string
	last   string
}

// WalkTable reads the given columns of an SNMP table into rows keyed by their index. Only the columns that are used are
// requested, because some agents answer slowly per value (Juniper EX switches take minutes to walk every column of the
// interface tables). Up to the client's MaxOids columns are walked side by side: one row per request on SNMPv1, and
// several rows per request with GetBulk on v2c and v3.
//
// Each walk starts by asking for a single row and doubles that with every answer, up to the client's MaxRepetitions.
// Tables an agent does not have then cost one small request instead of a full one, which the agent would otherwise
// fill with the values that come after the table (often the interface counters, the most expensive values a switch
// has). An agent answering tooBig gets half as many rows per request for the rest of the walk.
func WalkTable(client *gosnmp.GoSNMP, entryOID string, columns ...int) (map[string]Row, error) {
	return walkTable(client, entryOID, client.Version != gosnmp.Version1, columns)
}

// WalkTableWithoutBulk is WalkTable using GetNext only, one row per request, for tables that agents answer wrongly or
// struggle with when asked for with GetBulk
func WalkTableWithoutBulk(client *gosnmp.GoSNMP, entryOID string, columns ...int) (map[string]Row, error) {
	return walkTable(client, entryOID, false, columns)
}

func walkTable(client *gosnmp.GoSNMP, entryOID string, bulk bool, columns []int) (map[string]Row, error) {
	rows := make(map[string]Row)

	for _, group := range splitEvenly(columns, maxOIDs(client)) {
		if err := walkColumns(client, entryOID, bulk, group, rows); err != nil {
			return rows, err
		}
	}

	return rows, nil
}

// walkColumns walks columns side by side, adding their values to rows
func walkColumns(client *gosnmp.GoSNMP, entryOID string, bulk bool, columns []int, rows map[string]Row) error {
	active := make([]*columnCursor, 0, len(columns))

	for _, column := range columns {
		columnOID := entryOID + "." + strconv.Itoa(column)
		active = append(active, &columnCursor{column: column, prefix: columnOID + ".", last: columnOID})
	}

	repetitions, ceiling := 1, maxRepetitions(client)

	for len(active) > 0 {
		oids := make([]string, len(active))

		for i, cursor := range active {
			oids[i] = cursor.last
		}

		var (
			packet *gosnmp.SnmpPacket
			err    error
		)

		if bulk {
			repetitions = max(1, min(repetitions, ceiling, bulkValuesPerRequest/len(active)))
			packet, err = client.GetBulk(oids, 0, uint32(repetitions))
		} else {
			packet, err = client.GetNext(oids)
		}

		if err != nil {
			return err
		}

		if packet.Error == gosnmp.TooBig && bulk && repetitions > 1 {
			repetitions /= 2
			ceiling = repetitions

			continue
		}

		// SNMPv1 has no end-of-MIB marker, so an agent fails the whole request with noSuchName and points at the
		// column that ran past the end of everything it serves. That column is finished and the rest are retried.
		if packet.Error == gosnmp.NoSuchName && client.Version == gosnmp.Version1 {
			index := int(packet.ErrorIndex) - 1

			if index < 0 || index >= len(active) {
				return nil
			}

			active = append(active[:index], active[index+1:]...)

			continue
		}

		if packet.Error != gosnmp.NoError {
			return fmt.Errorf("WalkTable: agent responded with error %s", packet.Error)
		}

		if len(packet.Variables) < 1 {
			return nil
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

			if !ok || IsMissing(pdu) || CompareOIDs(name, cursor.last) <= 0 {
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
		repetitions *= 2
	}

	return nil
}

// IsMissing reports whether an agent answered for an object it does not have
func IsMissing(pdu gosnmp.SnmpPDU) bool {
	return pdu.Type == gosnmp.EndOfMibView || pdu.Type == gosnmp.NoSuchObject || pdu.Type == gosnmp.NoSuchInstance
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
