package main

import (
	"cmp"
	"fmt"
	"main/src/api"
	"main/src/snmp"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidOSPFRouterID        = "1.3.6.1.2.1.14.1.1.0"    // OSPF-MIB ospfRouterId
	oidOSPFNeighborEntry   = "1.3.6.1.2.1.14.10.1"     // OSPF-MIB ospfNbrEntry, indexed by neighbor address and address-less ifIndex
	oidOSPFv3RouterID      = "1.3.6.1.2.1.191.1.1.1.0" // OSPFV3-MIB ospfv3RouterId
	oidOSPFv3NeighborEntry = "1.3.6.1.2.1.191.1.9.1"   // OSPFV3-MIB ospfv3NbrEntry, indexed by ifIndex, instance ID and router ID

	// ospfNbrEntry columns
	ospfNeighborRouterID = 3
	ospfNeighborState    = 6

	// ospfv3NbrEntry columns
	ospfv3NeighborAddressType = 4
	ospfv3NeighborAddress     = 5
	ospfv3NeighborState       = 8
)

// ospfNeighborStates names ospfNbrState and ospfv3NbrState values, which both MIBs number the same way
var ospfNeighborStates = map[int64]string{
	1: api.OSPFNeighborStateDown,
	2: api.OSPFNeighborStateAttempt,
	3: api.OSPFNeighborStateInit,
	4: api.OSPFNeighborStateTwoWay,
	5: api.OSPFNeighborStateExchangeStart,
	6: api.OSPFNeighborStateExchange,
	7: api.OSPFNeighborStateLoading,
	8: api.OSPFNeighborStateFull,
}

// collectOSPF reads the device's OSPF router IDs and neighbors from OSPF-MIB (OSPFv2) and OSPFV3-MIB (OSPFv3). Devices
// without OSPF report none of either, which gives an empty result rather than an error.
func collectOSPF(client *gosnmp.GoSNMP, interfaces []api.Interface) (*api.OSPF, error) {
	routerIDs, err := snmp.GetRow(client, oidOSPFRouterID, oidOSPFv3RouterID)

	if err != nil {
		return nil, err
	}

	rows, err := snmp.WalkTable(client, oidOSPFNeighborEntry, ospfNeighborRouterID, ospfNeighborState)

	if err != nil {
		return nil, fmt.Errorf("collectOSPF: OSPFv2 neighbors: %w", err)
	}

	v3Rows, err := snmp.WalkTable(client, oidOSPFv3NeighborEntry, ospfv3NeighborAddressType, ospfv3NeighborAddress, ospfv3NeighborState)

	if err != nil {
		return nil, fmt.Errorf("collectOSPF: OSPFv3 neighbors: %w", err)
	}

	// Concat gives nil when both are empty, which would be sent as null instead of an empty list
	neighbors := append(make([]api.OSPFNeighbor, 0), slices.Concat(ospfNeighbors(rows, interfaces), ospfv3Neighbors(v3Rows))...)

	slices.SortFunc(neighbors, func(a, b api.OSPFNeighbor) int {
		return cmp.Or(cmp.Compare(a.Version, b.Version), cmp.Compare(valueOr(a.InterfaceIndex, 0), valueOr(b.InterfaceIndex, 0)), strings.Compare(a.RouterID, b.RouterID))
	})

	return &api.OSPF{RouterIDs: ospfRouterIDs(routerIDs), Neighbors: neighbors}, nil
}

// ospfRouterIDs lists the OSPFv2 and OSPFv3 router IDs, once each, leaving out 0.0.0.0 (no router ID)
func ospfRouterIDs(row snmp.Row) []string {
	ids := make([]string, 0, 2)

	for _, id := range []string{row.String(0), ospfv3RouterID(row.Int(1))} {
		if id != "" && id != "0.0.0.0" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	return ids
}

// ospfv3RouterID writes an OSPFv3 router ID, a 32-bit number, in the dotted form OSPFv2 router IDs use
func ospfv3RouterID(value int64) string {
	if value <= 0 || value > 0xFFFFFFFF {
		return ""
	}

	return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value)).String()
}

// ospfNeighbors reads ospfNbrTable rows, indexed by the neighbor's address and, on unnumbered links, the ifIndex of the
// interface it is reached through. On numbered links that interface is the one with an address in the neighbor's
// subnet.
func ospfNeighbors(rows map[string]snmp.Row, interfaces []api.Interface) []api.OSPFNeighbor {
	neighbors := make([]api.OSPFNeighbor, 0, len(rows))

	for index, row := range rows {
		parts := strings.Split(index, ".")

		if len(parts) != 5 {
			continue
		}

		address := strings.Join(parts[:4], ".")
		addressLessIndex, err := strconv.Atoi(parts[4])

		if err != nil || net.ParseIP(address) == nil {
			continue
		}

		neighbor := api.OSPFNeighbor{
			Version:  2,
			RouterID: row.String(ospfNeighborRouterID),
			Address:  address,
			State:    ospfNeighborStates[row.Int(ospfNeighborState)],
		}

		if addressLessIndex > 0 {
			neighbor.InterfaceIndex = PointerOf(addressLessIndex)
		} else if item := interfaceInSubnet(interfaces, address); item != nil {
			neighbor.InterfaceIndex = PointerOf(item.Index)
		}

		neighbors = append(neighbors, neighbor)
	}

	return neighbors
}

// ospfv3Neighbors reads ospfv3NbrTable rows, indexed by the ifIndex of the interface the neighbor is reached through,
// the OSPFv3 instance and the neighbor's router ID
func ospfv3Neighbors(rows map[string]snmp.Row) []api.OSPFNeighbor {
	neighbors := make([]api.OSPFNeighbor, 0, len(rows))

	for index, row := range rows {
		parts := strings.Split(index, ".")

		if len(parts) != 3 {
			continue
		}

		interfaceIndex, interfaceErr := strconv.Atoi(parts[0])
		routerID, routerErr := strconv.ParseInt(parts[2], 10, 64)

		if interfaceErr != nil || routerErr != nil {
			continue
		}

		neighbor := api.OSPFNeighbor{
			Version:        3,
			RouterID:       ospfv3RouterID(routerID),
			InterfaceIndex: PointerOf(interfaceIndex),
			State:          ospfNeighborStates[row.Int(ospfv3NeighborState)],
		}

		// InetAddressType 1 (IPv4) and 2 (IPv6), whose addresses are 4 and 16 octets
		if address := row.Bytes(ospfv3NeighborAddress); (row.Int(ospfv3NeighborAddressType) == 1 && len(address) == 4) || (row.Int(ospfv3NeighborAddressType) == 2 && len(address) == 16) {
			neighbor.Address = net.IP(address).String()
		}

		neighbors = append(neighbors, neighbor)
	}

	return neighbors
}

// interfaceInSubnet finds the interface with an address (written "address/prefix length") in the most specific subnet
// containing an address, or nil. The most specific one is the link itself; broader ones (such as a /8 on an internal
// management interface) only contain it by chance.
func interfaceInSubnet(interfaces []api.Interface, address string) *api.Interface {
	ip := net.ParseIP(address)

	var (
		best       *api.Interface
		bestLength = -1
	)

	for i, item := range interfaces {
		for _, assigned := range item.Addresses {
			_, network, err := net.ParseCIDR(assigned)

			if err != nil || !network.Contains(ip) {
				continue
			}

			if length, _ := network.Mask.Size(); length > bestLength {
				best, bestLength = &interfaces[i], length
			}
		}
	}

	return best
}
