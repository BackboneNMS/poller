package snmp

import (
	"slices"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// GetRow reads scalar values into a row keyed by their position in oids, so they can be read like a table row. Objects
// the agent does not have are left out.
func GetRow(client *gosnmp.GoSNMP, oids ...string) (Row, error) {
	packet, err := client.Get(oids)

	if err != nil {
		return nil, err
	}

	row := make(Row, len(oids))

	if packet.Error != gosnmp.NoError {
		return row, nil
	}

	for _, pdu := range packet.Variables {
		if position := slices.Index(oids, strings.TrimPrefix(pdu.Name, ".")); position >= 0 {
			row[position] = pdu
		}
	}

	return row, nil
}
