package snmp

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// FormatHexOctets writes bytes as upper-case hex octets separated by colons, as in "00:11:22:33:44:55"
func FormatHexOctets(value []byte) string {
	parts := make([]string, len(value))

	for i, octet := range value {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{octet}))
	}

	return strings.Join(parts, ":")
}

// MACAddressFromIndex reads a MAC address written in a table index as six decimal OID parts, as in "0.17.34.51.68.85"
func MACAddressFromIndex(value string) (string, bool) {
	parts := strings.Split(value, ".")

	if len(parts) != 6 {
		return "", false
	}

	octets := make([]byte, len(parts))

	for i, part := range parts {
		octet, err := strconv.ParseUint(part, 10, 8)

		if err != nil {
			return "", false
		}

		octets[i] = byte(octet)
	}

	return FormatHexOctets(octets), true
}
