// Package snmp holds the SNMP primitives shared by the poller and the vendor definitions: walking tables, getting
// scalars and reading values out of the rows they return.
package snmp

import (
	"strings"

	"github.com/gosnmp/gosnmp"
)

// Row is one row of an SNMP table, or a set of scalars, keyed by column number (or position)
type Row map[int]gosnmp.SnmpPDU

// String returns a column as text, or "" when it is missing or not text
func (r Row) String(column int) string {
	pdu, ok := r[column]

	if !ok {
		return ""
	}

	return PDUString(pdu)
}

// Bytes returns an octet string column as raw bytes, or nil
func (r Row) Bytes(column int) []byte {
	pdu, ok := r[column]

	if !ok || pdu.Type != gosnmp.OctetString {
		return nil
	}

	value, _ := pdu.Value.([]byte)

	return value
}

// Int returns a numeric column, or 0 when it is missing or not a number
func (r Row) Int(column int) int64 {
	value, _ := r.Number(column)

	return value
}

// Number returns a numeric column and whether it was present as a number, telling 0 apart from a missing value
func (r Row) Number(column int) (int64, bool) {
	pdu, ok := r[column]

	if !ok {
		return 0, false
	}

	switch pdu.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64, gosnmp.Uinteger32:
		return PDUInt(pdu), true
	default:
		return 0, false
	}
}

func PDUString(pdu gosnmp.SnmpPDU) string {
	switch pdu.Type {
	case gosnmp.OctetString:
		value, _ := pdu.Value.([]byte)

		return strings.TrimSpace(strings.ToValidUTF8(strings.TrimRight(string(value), "\x00"), ""))
	case gosnmp.ObjectIdentifier, gosnmp.IPAddress:
		value, _ := pdu.Value.(string)

		return strings.TrimPrefix(value, ".")
	default:
		return ""
	}
}

func PDUInt(pdu gosnmp.SnmpPDU) int64 {
	return gosnmp.ToBigInt(pdu.Value).Int64()
}
