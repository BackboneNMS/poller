package snmp

import (
	"slices"
	"strings"
	"testing"
)

func TestShiftInetAddress(t *testing.T) {
	for _, test := range []struct {
		index    string
		trailing int
		address  string
		rest     []string
	}{
		{"1.10.0.0.1", 0, "10.0.0.1", []string{}},
		{"1.4.10.0.0.1", 0, "10.0.0.1", []string{}},
		{"1.10.0.0.1.1.10.0.0.2", 5, "10.0.0.1", []string{"1", "10", "0", "0", "2"}},
		{"1.4.10.0.0.1.1.4.10.0.0.2", 6, "10.0.0.1", []string{"1", "4", "10", "0", "0", "2"}},
		{"2.32.1.184.0.0.0.0.0.0.0.0.0.0.0.0.1", 0, "2001:b800::1", []string{}},
		{"2.16.32.1.184.0.0.0.0.0.0.0.0.0.0.0.0.1", 0, "2001:b800::1", []string{}},
	} {
		address, rest, ok := ShiftInetAddress(strings.Split(test.index, "."), test.trailing)

		if !ok || address != test.address || !slices.Equal(rest, test.rest) {
			t.Errorf("%s: expected %s with %v left, got %s with %v left (%v)", test.index, test.address, test.rest, address, rest, ok)
		}
	}

	for _, index := range []string{"", "9.1.2.3.4", "1.10.0.0", "1.10.0.0.300"} {
		if _, _, ok := ShiftInetAddress(strings.Split(index, "."), 0); ok {
			t.Errorf("%q: expected no address", index)
		}
	}
}

func TestFormatInetAddress(t *testing.T) {
	for value, expected := range map[string]string{
		"\x0a\x00\x00\x01":                 "10.0.0.1",
		"\x0a\x00\x00\x01\x00\x00\x00\x07": "10.0.0.1",
		"":                                 "",
		"\x01\x02":                         "",
	} {
		if address := FormatInetAddress([]byte(value)); address != expected {
			t.Errorf("%q: expected %q, got %q", value, expected, address)
		}
	}
}
