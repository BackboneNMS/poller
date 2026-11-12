package main

import (
	"cmp"
	"context"
	"fmt"
	"main/src/api"
	"main/src/snmp"
	"main/src/vendors"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidBridgePortEntry  = "1.3.6.1.2.1.17.1.4.1"     // BRIDGE-MIB dot1dBasePortEntry, indexed by bridge port
	oidBridgeFDBEntry   = "1.3.6.1.2.1.17.4.3.1"     // BRIDGE-MIB dot1dTpFdbEntry, indexed by MAC address
	oidQBridgeFDBEntry  = "1.3.6.1.2.1.17.7.1.2.2.1" // Q-BRIDGE-MIB dot1qTpFdbEntry, indexed by FDB ID and MAC address
	oidQBridgeVLANEntry = "1.3.6.1.2.1.17.7.1.4.2.1" // Q-BRIDGE-MIB dot1qVlanCurrentEntry, indexed by time mark and VLAN
)

// collectForwardingTable reads the MAC addresses the device learned on each of its interfaces: from Q-BRIDGE-MIB,
// which has them by VLAN, otherwise from BRIDGE-MIB, one VLAN context at a time for vendors that need it (Cisco).
// Only the port column is walked, since the cost of a walk grows with the values read and forwarding tables can be
// large. Entries on ports that are not interfaces (the device's own addresses, on port 0) are left out.
func collectForwardingTable(ctx context.Context, client *gosnmp.GoSNMP, device api.Device, credential api.SNMPCredential, vendor *vendors.Vendor, interfaces []api.Interface) ([]api.ForwardingEntry, error) {
	rows, err := snmp.WalkTable(client, oidQBridgeFDBEntry, 2)

	if err != nil {
		return nil, err
	}

	if len(rows) > 0 {
		portRows, err := snmp.WalkTable(client, oidBridgePortEntry, 2)

		if err != nil {
			return nil, err
		}

		vlanRows, err := snmp.WalkTable(client, oidQBridgeVLANEntry, 3)

		if err != nil {
			return nil, err
		}

		return qBridgeEntries(rows, fdbVLANs(vlanRows), bridgePortInterfaces(portRows, interfaces)), nil
	}

	if vendor.ForwardingVLANs != nil {
		vlans, err := vendor.ForwardingVLANs(client)

		if err != nil {
			return nil, err
		}

		if len(vlans) > 0 {
			return collectVLANForwardingTables(ctx, device, credential, vendor, vlans, interfaces)
		}
	}

	return readBridgeForwardingTable(client, nil, interfaces)
}

// collectVLANForwardingTables reads the BRIDGE-MIB forwarding table of each VLAN in its own context. The first VLAN
// that cannot be read fails the whole table, since it usually means the device does not allow VLAN contexts for these
// credentials, and every remaining VLAN would cost another timeout.
func collectVLANForwardingTables(ctx context.Context, device api.Device, credential api.SNMPCredential, vendor *vendors.Vendor, vlans []int, interfaces []api.Interface) ([]api.ForwardingEntry, error) {
	entries := make([]api.ForwardingEntry, 0)

	for _, vlan := range vlans {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		vlanEntries, err := func() ([]api.ForwardingEntry, error) {
			client, err := vlanContextClient(ctx, device, credential, vendor, vlan)

			if err != nil {
				return nil, err
			}

			defer client.Conn.Close()

			return readBridgeForwardingTable(client, &vlan, interfaces)
		}()

		if err != nil {
			return nil, fmt.Errorf("collectVLANForwardingTables: VLAN %d: %w", vlan, err)
		}

		entries = append(entries, vlanEntries...)
	}

	sortForwardingEntries(entries)

	return entries, nil
}

// vlanContextClient connects to the context holding one VLAN's bridge tables: "community@vlan" on SNMPv1 and v2c,
// and context "vlan-N" on v3, keeping to the vendor's request limits
func vlanContextClient(ctx context.Context, device api.Device, credential api.SNMPCredential, vendor *vendors.Vendor, vlan int) (*gosnmp.GoSNMP, error) {
	if credential.Version != api.SNMPVersion3 {
		credential.Community += "@" + strconv.Itoa(vlan)
	}

	client, err := NewSNMPClient(ctx, device.Host, device.SNMP.Port, credential)

	if err != nil {
		return nil, err
	}

	if credential.Version == api.SNMPVersion3 {
		client.ContextName = "vlan-" + strconv.Itoa(vlan)
	}

	snmp.ApplyLimits(client, vendor.Limits())

	if err = client.Connect(); err != nil {
		return nil, err
	}

	return client, nil
}

// readBridgeForwardingTable reads the BRIDGE-MIB forwarding table the client's context serves, all of it in vlan
func readBridgeForwardingTable(client *gosnmp.GoSNMP, vlan *int, interfaces []api.Interface) ([]api.ForwardingEntry, error) {
	rows, err := snmp.WalkTable(client, oidBridgeFDBEntry, 2)

	if err != nil || len(rows) < 1 {
		return make([]api.ForwardingEntry, 0), err
	}

	portRows, err := snmp.WalkTable(client, oidBridgePortEntry, 2)

	if err != nil {
		return nil, err
	}

	return bridgeEntries(rows, vlan, bridgePortInterfaces(portRows, interfaces)), nil
}

// bridgePortInterfaces maps bridge port numbers to interface indexes from dot1dBasePortIfIndex. Devices without that
// table number their bridge ports by interface index, so every interface is then mapped to itself.
func bridgePortInterfaces(portRows map[string]snmp.Row, interfaces []api.Interface) map[int64]int {
	ports := make(map[int64]int, max(len(portRows), len(interfaces)))

	for index, row := range portRows {
		port, err := strconv.ParseInt(index, 10, 64)

		if err == nil && row.Int(2) > 0 {
			ports[port] = int(row.Int(2))
		}
	}

	if len(portRows) > 0 {
		return ports
	}

	for _, item := range interfaces {
		ports[int64(item.Index)] = item.Index
	}

	return ports
}

// fdbVLANs maps filtering database IDs to the VLAN using them, from dot1qVlanFdbId. When VLANs share a database
// (shared VLAN learning), the lowest VLAN is used.
func fdbVLANs(vlanRows map[string]snmp.Row) map[int64]int {
	vlans := make(map[int64]int, len(vlanRows))

	for index, row := range vlanRows {
		_, rawVLAN, ok := strings.Cut(index, ".")
		vlan, err := strconv.Atoi(rawVLAN)
		fdbID, present := row.Number(3)

		if !ok || err != nil || !present {
			continue
		}

		if current, ok := vlans[fdbID]; !ok || vlan < current {
			vlans[fdbID] = vlan
		}
	}

	return vlans
}

// qBridgeEntries reads dot1qTpFdbTable rows, indexed by filtering database ID and MAC address. Without a VLAN table
// the database ID is taken to be the VLAN, as it is on most switches.
func qBridgeEntries(rows map[string]snmp.Row, vlans map[int64]int, ports map[int64]int) []api.ForwardingEntry {
	entries := make([]api.ForwardingEntry, 0, len(rows))

	for index, row := range rows {
		rawFDBID, rawAddress, _ := strings.Cut(index, ".")
		fdbID, err := strconv.ParseInt(rawFDBID, 10, 64)
		address, ok := macAddressFromOID(rawAddress)
		interfaceIndex, mapped := ports[row.Int(2)]

		if err != nil || !ok || !mapped {
			continue
		}

		entry := api.ForwardingEntry{MACAddress: address, InterfaceIndex: interfaceIndex}

		if vlan, ok := vlans[fdbID]; ok {
			entry.VLAN = PointerOf(vlan)
		} else if len(vlans) < 1 && fdbID > 0 {
			entry.VLAN = PointerOf(int(fdbID))
		}

		entries = append(entries, entry)
	}

	sortForwardingEntries(entries)

	return entries
}

// bridgeEntries reads dot1dTpFdbTable rows, indexed by MAC address
func bridgeEntries(rows map[string]snmp.Row, vlan *int, ports map[int64]int) []api.ForwardingEntry {
	entries := make([]api.ForwardingEntry, 0, len(rows))

	for index, row := range rows {
		address, ok := macAddressFromOID(index)
		interfaceIndex, mapped := ports[row.Int(2)]

		if !ok || !mapped {
			continue
		}

		entries = append(entries, api.ForwardingEntry{MACAddress: address, InterfaceIndex: interfaceIndex, VLAN: vlan})
	}

	sortForwardingEntries(entries)

	return entries
}

// macAddressFromOID reads a MAC address written as six decimal OID parts, as in "0.17.34.51.68.85"
func macAddressFromOID(value string) (string, bool) {
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

	return formatHexOctets(octets), true
}

func sortForwardingEntries(entries []api.ForwardingEntry) {
	slices.SortFunc(entries, func(a, b api.ForwardingEntry) int {
		return cmp.Or(
			cmp.Compare(a.InterfaceIndex, b.InterfaceIndex),
			cmp.Compare(valueOr(a.VLAN, 0), valueOr(b.VLAN, 0)),
			strings.Compare(a.MACAddress, b.MACAddress),
		)
	})
}

func valueOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}

	return *value
}
