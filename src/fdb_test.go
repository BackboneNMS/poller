package main

import (
	"fmt"
	"main/src/api"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"slices"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func expectForwardingTable(t *testing.T, got []api.ForwardingEntry, expected []api.ForwardingEntry) {
	t.Helper()

	equal := slices.EqualFunc(got, expected, func(a, b api.ForwardingEntry) bool {
		return a.MACAddress == b.MACAddress && a.InterfaceIndex == b.InterfaceIndex && valueOr(a.VLAN, -1) == valueOr(b.VLAN, -1)
	})

	if !equal {
		t.Fatalf("expected forwarding table %s, got %s", describeForwardingTable(expected), describeForwardingTable(got))
	}
}

func describeForwardingTable(entries []api.ForwardingEntry) string {
	parts := make([]string, len(entries))

	for i, entry := range entries {
		parts[i] = fmt.Sprintf("%s@%d/%d", entry.MACAddress, entry.InterfaceIndex, valueOr(entry.VLAN, -1))
	}

	return strings.Join(parts, " ")
}

// TestQBridgeForwardingTable maps filtering databases to VLANs and bridge ports to interfaces, leaving out entries on
// port 0 (the device's own address)
func TestQBridgeForwardingTable(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Switch", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.17.1.4.1.2.1":                      snmptest.Integer(35684352),
		"1.3.6.1.2.1.17.7.1.4.2.1.3.0.10":               snmptest.Gauge(5),
		"1.3.6.1.2.1.17.7.1.4.2.1.3.0.20":               snmptest.Gauge(6),
		"1.3.6.1.2.1.17.7.1.2.2.1.2.5.0.17.34.51.68.85": snmptest.Integer(1),
		"1.3.6.1.2.1.17.7.1.2.2.1.2.6.0.17.34.51.68.86": snmptest.Integer(1),
		"1.3.6.1.2.1.17.7.1.2.2.1.2.9.0.17.34.51.68.87": snmptest.Integer(1),
		"1.3.6.1.2.1.17.7.1.2.2.1.2.5.170.0.0.0.0.1":    snmptest.Integer(0),
	})

	expectForwardingTable(t, data.ForwardingTable, []api.ForwardingEntry{
		{MACAddress: "00:11:22:33:44:57", InterfaceIndex: 35684352},
		{MACAddress: "00:11:22:33:44:55", InterfaceIndex: 35684352, VLAN: PointerOf(10)},
		{MACAddress: "00:11:22:33:44:56", InterfaceIndex: 35684352, VLAN: PointerOf(20)},
	})
}

func TestQBridgeEntriesWithoutVLANTable(t *testing.T) {
	entries := qBridgeEntries(
		map[string]snmp.Row{"30.0.17.34.51.68.85": {2: snmptest.Integer(7)}},
		map[int64]int{},
		map[int64]int{7: 107},
	)

	expectForwardingTable(t, entries, []api.ForwardingEntry{{MACAddress: "00:11:22:33:44:55", InterfaceIndex: 107, VLAN: PointerOf(30)}})
}

// TestBridgeForwardingTable reads BRIDGE-MIB when the device has no Q-BRIDGE-MIB table, with no VLANs
func TestBridgeForwardingTable(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Switch", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.17.1.4.1.2.3":                snmptest.Integer(35684352),
		"1.3.6.1.2.1.17.4.3.1.2.0.17.34.51.68.85": snmptest.Integer(3),
		"1.3.6.1.2.1.17.4.3.1.2.0.17.34.51.68.86": snmptest.Integer(4),
	})

	expectForwardingTable(t, data.ForwardingTable, []api.ForwardingEntry{{MACAddress: "00:11:22:33:44:55", InterfaceIndex: 35684352}})
}

// TestCiscoForwardingTablePerVLAN reads each operational Ethernet VLAN in its own context. The simulated agent serves
// the same values under every community, so each VLAN reports the same address.
func TestCiscoForwardingTablePerVLAN(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.9.1.1208", "Cisco IOS Software", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.4.1.9.9.46.1.3.1.1.2.1.1":        snmptest.Integer(1),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.3.1.1":        snmptest.Integer(1),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.2.1.30":       snmptest.Integer(1),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.3.1.30":       snmptest.Integer(1),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.2.1.40":       snmptest.Integer(2),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.3.1.40":       snmptest.Integer(1),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.2.1.1002":     snmptest.Integer(1),
		"1.3.6.1.4.1.9.9.46.1.3.1.1.3.1.1002":     snmptest.Integer(2),
		"1.3.6.1.2.1.17.1.4.1.2.3":                snmptest.Integer(35684352),
		"1.3.6.1.2.1.17.4.3.1.2.0.17.34.51.68.85": snmptest.Integer(3),
	})

	expectForwardingTable(t, data.ForwardingTable, []api.ForwardingEntry{
		{MACAddress: "00:11:22:33:44:55", InterfaceIndex: 35684352, VLAN: PointerOf(1)},
		{MACAddress: "00:11:22:33:44:55", InterfaceIndex: 35684352, VLAN: PointerOf(30)},
	})
}

func TestBridgePortsDefaultToInterfaceIndexes(t *testing.T) {
	ports := bridgePortInterfaces(map[string]snmp.Row{}, []api.Interface{{Index: 4}, {Index: 9}})

	if len(ports) != 2 || ports[4] != 4 || ports[9] != 9 {
		t.Fatalf("expected interfaces to map to themselves, got %v", ports)
	}
}
