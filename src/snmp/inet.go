package snmp

import (
	"net"
	"strconv"
)

// inetAddressLengths are the octets an InetAddress of each InetAddressType holds, with how many of them are the address
// (ipv4z and ipv6z add a 4-octet zone index)
var inetAddressLengths = map[string][2]int{"1": {4, 4}, "2": {16, 16}, "3": {8, 4}, "4": {20, 16}}

// ShiftInetAddress reads an InetAddressType and InetAddress off the front of a table index split into its parts, and
// returns the address and the parts after it. Agents differ on whether the address is preceded by its length, so both
// are read; trailing is how many parts follow the address, which tells them apart when it is not the last.
func ShiftInetAddress(parts []string, trailing int) (string, []string, bool) {
	if len(parts) < 1 {
		return "", nil, false
	}

	lengths, ok := inetAddressLengths[parts[0]]
	rest := parts[1:]
	available := len(rest) - trailing

	if !ok {
		return "", nil, false
	}

	if available == lengths[0]+1 && rest[0] == strconv.Itoa(lengths[0]) {
		rest, available = rest[1:], available-1
	}

	if available != lengths[0] {
		return "", nil, false
	}

	octets := make([]byte, lengths[1])

	for i := range octets {
		octet, err := strconv.ParseUint(rest[i], 10, 8)

		if err != nil {
			return "", nil, false
		}

		octets[i] = byte(octet)
	}

	return net.IP(octets).String(), rest[lengths[0]:], true
}

// FormatInetAddress writes an InetAddress value (4 or 16 octets, or 8 or 20 with a zone index, which is left out), or
// "" for any other length, such as an unset address
func FormatInetAddress(value []byte) string {
	switch len(value) {
	case 4, 8:
		return net.IP(value[:4]).String()
	case 16, 20:
		return net.IP(value[:16]).String()
	default:
		return ""
	}
}
