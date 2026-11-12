package snmp

import (
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// GetRow reads scalar values into a row keyed by their position in oids, so they can be read like a table row. Objects
// the agent does not have are left out, and so are the objects of a request the agent failed with an error. The oids
// are read in as many requests as the client's MaxOids needs.
func GetRow(client *gosnmp.GoSNMP, oids ...string) (Row, error) {
	row := make(Row, len(oids))

	for chunk := range slices.Chunk(oids, maxOIDs(client)) {
		packet, err := client.Get(chunk)

		if err != nil {
			return nil, err
		}

		if packet.Error != gosnmp.NoError {
			continue
		}

		for _, pdu := range packet.Variables {
			if position := slices.Index(oids, strings.TrimPrefix(pdu.Name, ".")); position >= 0 && !IsMissing(pdu) {
				row[position] = pdu
			}
		}
	}

	return row, nil
}

// FillColumns adds values of a table's columns to some of the rows already read from it, given as the indexes needing
// each column. A column most rows need is walked, since that takes far fewer requests; otherwise only the values
// needed are read with Get. Rows not already in rows are left out.
func FillColumns(client *gosnmp.GoSNMP, entryOID string, rows map[string]Row, needs map[int][]string) error {
	walked, got := make([]int, 0), make([]string, 0)

	for column, indexes := range needs {
		switch {
		case len(indexes) < 1:
		case len(indexes)*2 > len(rows):
			walked = append(walked, column)
		default:
			for _, index := range indexes {
				got = append(got, entryOID+"."+strconv.Itoa(column)+"."+index)
			}
		}
	}

	if len(walked) > 0 {
		slices.Sort(walked)

		values, err := WalkTable(client, entryOID, walked...)

		if err != nil {
			return err
		}

		for index, row := range values {
			if rows[index] == nil {
				continue
			}

			for column, pdu := range row {
				rows[index][column] = pdu
			}
		}
	}

	if len(got) < 1 {
		return nil
	}

	values, err := GetRow(client, got...)

	if err != nil {
		return err
	}

	for position, pdu := range values {
		rawColumn, index, _ := strings.Cut(strings.TrimPrefix(got[position], entryOID+"."), ".")
		column, _ := strconv.Atoi(rawColumn)

		if rows[index] != nil {
			rows[index][column] = pdu
		}
	}

	return nil
}
