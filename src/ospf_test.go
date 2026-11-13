package main

import (
	"main/src/api"
	"main/src/snmp/snmptest"
	"net"
	"slices"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// TestCollectOSPF reads the router IDs (the OSPFv3 one written like OSPFv2's, listed once when they are the same) and
// both neighbor tables: OSPFv2 neighbors on numbered links get the interface with an address in their subnet, those on
// unnumbered links the interface in their index, and OSPFv3 neighbors the interface in their index
func TestCollectOSPF(t *testing.T) {
	linkLocal := net.ParseIP("fe80::1")

	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Router", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.4.20.1.2.10.81.255.101":            snmptest.Integer(35684352),
		"1.3.6.1.2.1.4.20.1.3.10.81.255.101":            snmptest.IPAddress("255.255.255.252"),
		"1.3.6.1.2.1.14.1.1.0":                          snmptest.IPAddress("100.78.0.200"),
		"1.3.6.1.2.1.191.1.1.1.0":                       snmptest.Gauge(1682833608),
		"1.3.6.1.2.1.14.10.1.3.10.81.255.102.0":         snmptest.IPAddress("100.77.0.1"),
		"1.3.6.1.2.1.14.10.1.6.10.81.255.102.0":         snmptest.Integer(8),
		"1.3.6.1.2.1.14.10.1.3.192.0.2.9.7":             snmptest.IPAddress("192.0.2.9"),
		"1.3.6.1.2.1.14.10.1.6.192.0.2.9.7":             snmptest.Integer(4),
		"1.3.6.1.2.1.191.1.9.1.4.35684352.0.1682702337": snmptest.Integer(2),
		"1.3.6.1.2.1.191.1.9.1.5.35684352.0.1682702337": snmptest.OctetString(string(linkLocal)),
		"1.3.6.1.2.1.191.1.9.1.8.35684352.0.1682702337": snmptest.Integer(8),
	})

	if data.OSPF == nil {
		t.Fatal("expected OSPF data, got none")
	}

	if !slices.Equal(data.OSPF.RouterIDs, []string{"100.78.0.200"}) {
		t.Fatalf("expected router ID 100.78.0.200, got %v", data.OSPF.RouterIDs)
	}

	expected := []api.OSPFNeighbor{
		{Version: 2, RouterID: "192.0.2.9", Address: "192.0.2.9", InterfaceIndex: PointerOf(7), State: api.OSPFNeighborStateTwoWay},
		{Version: 2, RouterID: "100.77.0.1", Address: "10.81.255.102", InterfaceIndex: PointerOf(35684352), State: api.OSPFNeighborStateFull},
		{Version: 3, RouterID: "100.76.0.1", Address: "fe80::1", InterfaceIndex: PointerOf(35684352), State: api.OSPFNeighborStateFull},
	}

	equal := slices.EqualFunc(data.OSPF.Neighbors, expected, func(a, b api.OSPFNeighbor) bool {
		return a.Version == b.Version && a.RouterID == b.RouterID && a.Address == b.Address && valueOr(a.InterfaceIndex, -1) == valueOr(b.InterfaceIndex, -1) && a.State == b.State
	})

	if !equal {
		t.Fatalf("expected neighbors %+v, got %+v", expected, data.OSPF.Neighbors)
	}
}

// TestCollectOSPFWithoutOSPF reports an empty result for devices without OSPF, so the stored neighbors are cleared
func TestCollectOSPFWithoutOSPF(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Switch", map[string]gosnmp.SnmpPDU{})

	if data.OSPF == nil || data.OSPF.Neighbors == nil || len(data.OSPF.RouterIDs) > 0 || len(data.OSPF.Neighbors) > 0 {
		t.Fatalf("expected an empty OSPF result, got %+v", data.OSPF)
	}
}

// TestInterfaceInSubnet picks the most specific subnet, as Juniper's internal em3.0 has a /8 that also contains the
// addresses of point-to-point links
func TestInterfaceInSubnet(t *testing.T) {
	interfaces := []api.Interface{
		{Index: 1, Name: "em3.0", Addresses: []string{"10.0.0.1/8", "128.0.0.1/2"}},
		{Index: 2, Name: "et-0/0/0.907", Addresses: []string{"10.81.255.109/30"}},
	}

	if item := interfaceInSubnet(interfaces, "10.81.255.110"); item == nil || item.Index != 2 {
		t.Fatalf("expected et-0/0/0.907, got %+v", item)
	}

	if item := interfaceInSubnet(interfaces, "10.9.9.9"); item == nil || item.Index != 1 {
		t.Fatalf("expected em3.0 for an address only its /8 contains, got %+v", item)
	}

	if item := interfaceInSubnet(interfaces, "192.0.2.1"); item != nil {
		t.Fatalf("expected no interface, got %+v", item)
	}
}
