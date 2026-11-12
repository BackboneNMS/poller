// Package snmptest runs a fake SNMPv2c agent that serves a fixed set of values, so code that gets scalars and walks
// tables can be tested against a simulated device, such as a new vendor definition against values copied from a real
// device's snmpwalk.
package snmptest

import (
	"main/src/snmp"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// NewAgent starts an agent on a random local UDP port that answers Get, GetNext and GetBulk requests from values (keyed
// by OID without a leading dot) under any community, and stops it when the test ends. It returns the port.
func NewAgent(t testing.TB, values map[string]gosnmp.SnmpPDU) uint16 {
	t.Helper()

	oids := make([]string, 0, len(values))

	for oid := range values {
		oids = append(oids, oid)
	}

	slices.SortFunc(oids, snmp.CompareOIDs)

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { conn.Close() })

	get := func(oid string) gosnmp.SnmpPDU {
		value, ok := values[oid]

		if !ok {
			return gosnmp.SnmpPDU{Name: "." + oid, Type: gosnmp.NoSuchObject}
		}

		value.Name = "." + oid

		return value
	}

	next := func(oid string) gosnmp.SnmpPDU {
		for _, candidate := range oids {
			if snmp.CompareOIDs(candidate, oid) > 0 {
				return get(candidate)
			}
		}

		return gosnmp.SnmpPDU{Name: "." + oid, Type: gosnmp.EndOfMibView}
	}

	go func() {
		buffer := make([]byte, 65535)
		decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c, Logger: gosnmp.NewLogger(nil)}

		for {
			n, address, err := conn.ReadFromUDP(buffer)

			if err != nil {
				return
			}

			request, err := decoder.SnmpDecodePacket(buffer[:n])

			if err != nil {
				continue
			}

			cursors := make([]string, len(request.Variables))

			for i, variable := range request.Variables {
				cursors[i] = strings.TrimPrefix(variable.Name, ".")
			}

			variables := make([]gosnmp.SnmpPDU, 0, len(cursors))

			switch request.PDUType {
			case gosnmp.GetRequest:
				for _, oid := range cursors {
					variables = append(variables, get(oid))
				}
			case gosnmp.GetNextRequest:
				for _, oid := range cursors {
					variables = append(variables, next(oid))
				}
			case gosnmp.GetBulkRequest:
				for range request.MaxRepetitions {
					for i, oid := range cursors {
						value := next(oid)
						variables = append(variables, value)
						cursors[i] = strings.TrimPrefix(value.Name, ".")
					}
				}
			}

			request.PDUType = gosnmp.GetResponse
			request.Variables = variables
			request.Error, request.ErrorIndex, request.NonRepeaters, request.MaxRepetitions = gosnmp.NoError, 0, 0, 0

			if response, err := request.MarshalMsg(); err == nil {
				conn.WriteToUDP(response, address)
			}
		}
	}()

	return uint16(conn.LocalAddr().(*net.UDPAddr).Port)
}

// NewClient starts an agent serving values and returns an SNMPv2c client connected to it
func NewClient(t testing.TB, values map[string]gosnmp.SnmpPDU) *gosnmp.GoSNMP {
	t.Helper()

	client := &gosnmp.GoSNMP{
		Target:    "127.0.0.1",
		Port:      NewAgent(t, values),
		Transport: "udp",
		Community: "public",
		Version:   gosnmp.Version2c,
		Timeout:   2 * time.Second,
		MaxOids:   gosnmp.MaxOids,
	}

	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { client.Conn.Close() })

	return client
}

func Integer(value int) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.Integer, Value: value}
}

func Gauge(value uint) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.Gauge32, Value: value}
}

func TimeTicks(value uint32) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.TimeTicks, Value: value}
}

func OctetString(value string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte(value)}
}

func ObjectID(value string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.ObjectIdentifier, Value: value}
}
