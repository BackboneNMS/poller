package main

import (
	"main/src/api"
	"main/src/snmp/snmptest"
	"net"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func expectBGPPeer(t *testing.T, peer api.BGPPeer, expected api.BGPPeer) {
	t.Helper()

	same := peer.Address == expected.Address && peer.RouterID == expected.RouterID && peer.LocalAddress == expected.LocalAddress &&
		peer.RemoteAS == expected.RemoteAS && valueOr(peer.LocalAS, -1) == valueOr(expected.LocalAS, -1) && peer.State == expected.State &&
		peer.AdminEnabled == expected.AdminEnabled && valueOr(peer.EstablishedTimeSeconds, -1) == valueOr(expected.EstablishedTimeSeconds, -1) &&
		valueOr(peer.InterfaceIndex, -1) == valueOr(expected.InterfaceIndex, -1)

	if !same {
		t.Fatalf("expected peer %+v, got %+v", expected, peer)
	}
}

// TestCollectBGP4MIB reads BGP4-MIB for vendors without a table of their own: the AS number and identifier, and each
// session sorted by address, with the interface on the peer's subnet for directly connected peers
func TestCollectBGP4MIB(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Router", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.4.20.1.2.10.81.255.109":  snmptest.Integer(35684352),
		"1.3.6.1.2.1.4.20.1.3.10.81.255.109":  snmptest.IPAddress("255.255.255.252"),
		"1.3.6.1.2.1.15.2.0":                  snmptest.Integer(65000),
		"1.3.6.1.2.1.15.4.0":                  snmptest.IPAddress("100.78.0.4"),
		"1.3.6.1.2.1.15.3.1.1.203.0.113.1":    snmptest.IPAddress("0.0.0.0"),
		"1.3.6.1.2.1.15.3.1.2.203.0.113.1":    snmptest.Integer(3),
		"1.3.6.1.2.1.15.3.1.3.203.0.113.1":    snmptest.Integer(1),
		"1.3.6.1.2.1.15.3.1.5.203.0.113.1":    snmptest.IPAddress("0.0.0.0"),
		"1.3.6.1.2.1.15.3.1.9.203.0.113.1":    snmptest.Integer(3356),
		"1.3.6.1.2.1.15.3.1.16.203.0.113.1":   snmptest.Gauge(0),
		"1.3.6.1.2.1.15.3.1.1.10.81.255.110":  snmptest.IPAddress("100.78.0.200"),
		"1.3.6.1.2.1.15.3.1.2.10.81.255.110":  snmptest.Integer(6),
		"1.3.6.1.2.1.15.3.1.3.10.81.255.110":  snmptest.Integer(2),
		"1.3.6.1.2.1.15.3.1.5.10.81.255.110":  snmptest.IPAddress("10.81.255.109"),
		"1.3.6.1.2.1.15.3.1.9.10.81.255.110":  snmptest.Integer(65000),
		"1.3.6.1.2.1.15.3.1.16.10.81.255.110": snmptest.Gauge(3600),
	})

	if data.BGP == nil || valueOr(data.BGP.LocalAS, 0) != 65000 || data.BGP.Identifier != "100.78.0.4" || len(data.BGP.Peers) != 2 {
		t.Fatalf("expected AS 65000, identifier 100.78.0.4 and 2 peers, got %+v", data.BGP)
	}

	expectBGPPeer(t, data.BGP.Peers[0], api.BGPPeer{
		Address: "10.81.255.110", RouterID: "100.78.0.200", LocalAddress: "10.81.255.109", RemoteAS: 65000,
		State: api.BGPPeerStateEstablished, AdminEnabled: true, EstablishedTimeSeconds: PointerOf[int64](3600), InterfaceIndex: PointerOf(35684352),
	})

	expectBGPPeer(t, data.BGP.Peers[1], api.BGPPeer{
		Address: "203.0.113.1", RemoteAS: 3356, State: api.BGPPeerStateActive, EstablishedTimeSeconds: PointerOf[int64](0),
	})
}

// TestCollectBGPFromVendorTable reads Juniper's own table instead of BGP4-MIB, IPv4 and IPv6 sessions alike, taking
// the AS number from the sessions when BGP4-MIB reports AS_TRANS
func TestCollectBGPFromVendorTable(t *testing.T) {
	const entry = "1.3.6.1.4.1.2636.5.1.1.2.1.1.1"

	ipv4 := "0.1.10.0.0.1.1.10.0.0.2"
	ipv6 := "0.2.16.32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.1.2.16.32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.2"

	data := collectFromAgent(t, "1.3.6.1.4.1.2636.1.1.1.2.137", "Juniper Networks, Inc. mx204 internet router", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.15.2.0":                       snmptest.Integer(23456),
		"1.3.6.1.2.1.15.3.1.2.10.0.0.2":            snmptest.Integer(6),
		entry + ".1." + ipv4:                       snmptest.OctetString(string([]byte{192, 0, 2, 2})),
		entry + ".2." + ipv4:                       snmptest.Integer(6),
		entry + ".3." + ipv4:                       snmptest.Integer(2),
		entry + ".7." + ipv4:                       snmptest.OctetString(string([]byte{10, 0, 0, 1})),
		entry + ".9." + ipv4:                       snmptest.Gauge(4200000001),
		entry + ".13." + ipv4:                      snmptest.Gauge(4200000002),
		"1.3.6.1.4.1.2636.5.1.1.2.4.1.1.1." + ipv4: snmptest.Gauge(86400),
		entry + ".1." + ipv6:                       snmptest.OctetString(string([]byte{0, 0, 0, 0})),
		entry + ".2." + ipv6:                       snmptest.Integer(1),
		entry + ".3." + ipv6:                       snmptest.Integer(1),
		entry + ".7." + ipv6:                       snmptest.OctetString(string(net.ParseIP("2001:db8::1"))),
		entry + ".9." + ipv6:                       snmptest.Gauge(4200000001),
		entry + ".13." + ipv6:                      snmptest.Gauge(6939),
	})

	if data.BGP == nil || valueOr(data.BGP.LocalAS, 0) != 4200000001 || len(data.BGP.Peers) != 2 {
		t.Fatalf("expected AS 4200000001 and 2 peers from the vendor table, got %+v", data.BGP)
	}

	expectBGPPeer(t, data.BGP.Peers[0], api.BGPPeer{
		Address: "10.0.0.2", RouterID: "192.0.2.2", LocalAddress: "10.0.0.1", RemoteAS: 4200000002, LocalAS: PointerOf[int64](4200000001),
		State: api.BGPPeerStateEstablished, AdminEnabled: true, EstablishedTimeSeconds: PointerOf[int64](86400),
	})

	expectBGPPeer(t, data.BGP.Peers[1], api.BGPPeer{
		Address: "2001:db8::2", LocalAddress: "2001:db8::1", RemoteAS: 6939, LocalAS: PointerOf[int64](4200000001), State: api.BGPPeerStateIdle,
	})
}

// TestCollectBGPWithoutBGP reports an empty result for devices without BGP, so the stored sessions are cleared
func TestCollectBGPWithoutBGP(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Switch", map[string]gosnmp.SnmpPDU{})

	if data.BGP == nil || data.BGP.Peers == nil || len(data.BGP.Peers) > 0 || data.BGP.LocalAS != nil {
		t.Fatalf("expected an empty BGP result, got %+v", data.BGP)
	}
}
