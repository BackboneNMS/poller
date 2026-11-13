package main

import (
	"main/src/api"
	"main/src/snmp"
	"main/src/vendors"
	"net"
	"slices"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidBGPLocalAS    = "1.3.6.1.2.1.15.2.0" // BGP4-MIB bgpLocalAs
	oidBGPIdentifier = "1.3.6.1.2.1.15.4.0" // BGP4-MIB bgpIdentifier
	oidBGPPeerEntry  = "1.3.6.1.2.1.15.3.1" // BGP4-MIB bgpPeerEntry, indexed by the peer's IPv4 address

	// bgpPeerEntry columns, and the bgpPeerAdminStatus value of a session that is administratively stopped
	bgpPeerIdentifier      = 1
	bgpPeerState           = 2
	bgpPeerAdminStatus     = 3
	bgpPeerLocalAddress    = 5
	bgpPeerRemoteAS        = 9
	bgpPeerEstablishedTime = 16
	bgpPeerAdminStop       = 1

	// asTrans is the AS number BGP4-MIB reports in place of 4-byte AS numbers, which it cannot hold
	asTrans = 23456
)

// collectBGP reads the device's BGP sessions from the vendor's own tables when it has them, otherwise from BGP4-MIB,
// with the AS number and BGP identifier from BGP4-MIB's scalars when they are set (and not AS_TRANS), else the AS of
// the first session reporting one. Devices without BGP report nothing, which gives an empty result rather than an
// error.
func collectBGP(client *gosnmp.GoSNMP, vendor *vendors.Vendor, interfaces []api.Interface) (*api.BGP, error) {
	scalars, err := snmp.GetRow(client, oidBGPLocalAS, oidBGPIdentifier)

	if err != nil {
		return nil, err
	}

	var bgp *api.BGP

	if vendor.BGP != nil {
		if bgp, err = vendor.BGP(client); err != nil {
			return nil, err
		}
	}

	if bgp == nil || len(bgp.Peers) < 1 {
		if bgp, err = collectBGP4MIBPeers(client); err != nil {
			return nil, err
		}
	}

	if localAS, ok := scalars.Number(0); ok && localAS > 0 && localAS != asTrans {
		bgp.LocalAS = &localAS
	}

	if identifier := scalars.String(1); identifier != "" && identifier != "0.0.0.0" {
		bgp.Identifier = identifier
	}

	for i, peer := range bgp.Peers {
		if item := interfaceInSubnet(interfaces, peer.Address); peer.InterfaceIndex == nil && item != nil {
			bgp.Peers[i].InterfaceIndex = PointerOf(item.Index)
		}
	}

	slices.SortFunc(bgp.Peers, func(a, b api.BGPPeer) int {
		return compareAddresses(a.Address, b.Address)
	})

	// Vendor tables report the local AS per session, which stands in for BGP4-MIB's when it has none or AS_TRANS
	for _, peer := range bgp.Peers {
		if bgp.LocalAS == nil && peer.LocalAS != nil {
			bgp.LocalAS = peer.LocalAS
		}
	}

	return bgp, nil
}

func collectBGP4MIBPeers(client *gosnmp.GoSNMP) (*api.BGP, error) {
	rows, err := snmp.WalkTable(client, oidBGPPeerEntry, bgpPeerIdentifier, bgpPeerState, bgpPeerAdminStatus, bgpPeerLocalAddress, bgpPeerRemoteAS, bgpPeerEstablishedTime)

	if err != nil {
		return nil, err
	}

	return &api.BGP{Peers: bgp4MIBPeers(rows)}, nil
}

// bgp4MIBPeers reads bgpPeerTable rows, indexed by the peer's IPv4 address
func bgp4MIBPeers(rows map[string]snmp.Row) []api.BGPPeer {
	peers := make([]api.BGPPeer, 0, len(rows))

	for index, row := range rows {
		if ip := net.ParseIP(index).To4(); ip == nil || len(strings.Split(index, ".")) != 4 {
			continue
		}

		peer := api.BGPPeer{
			Address:      index,
			RouterID:     bgpRouterID(row.String(bgpPeerIdentifier)),
			LocalAddress: bgpRouterID(row.String(bgpPeerLocalAddress)),
			RemoteAS:     row.Int(bgpPeerRemoteAS),
			State:        api.BGPPeerStates[row.Int(bgpPeerState)],
			AdminEnabled: row.Int(bgpPeerAdminStatus) != bgpPeerAdminStop,
		}

		if seconds, ok := row.Number(bgpPeerEstablishedTime); ok {
			peer.EstablishedTimeSeconds = &seconds
		}

		peers = append(peers, peer)
	}

	return peers
}

// bgpRouterID leaves out 0.0.0.0, which agents report for an identifier or address that is not known yet
func bgpRouterID(value string) string {
	if value == "0.0.0.0" {
		return ""
	}

	return value
}

// compareAddresses orders IP addresses numerically, IPv4 before IPv6
func compareAddresses(a, b string) int {
	ipA, ipB := net.ParseIP(a), net.ParseIP(b)

	if (ipA.To4() == nil) != (ipB.To4() == nil) {
		if ipA.To4() != nil {
			return -1
		}

		return 1
	}

	return slices.Compare(ipA.To16(), ipB.To16())
}
