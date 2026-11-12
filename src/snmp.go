package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"main/src/api"
	"main/src/entity"
	"main/src/snmp"
	"main/src/vendors"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
)

const (
	oidSystemDescription         = "1.3.6.1.2.1.1.1.0"
	oidSystemObjectID            = "1.3.6.1.2.1.1.2.0"
	oidSystemUptime              = "1.3.6.1.2.1.1.3.0"
	oidSystemContact             = "1.3.6.1.2.1.1.4.0"
	oidSystemName                = "1.3.6.1.2.1.1.5.0"
	oidSystemLocation            = "1.3.6.1.2.1.1.6.0"
	oidInterfaceEntry            = "1.3.6.1.2.1.2.2.1"
	oidInterfaceExtensionEntry   = "1.3.6.1.2.1.31.1.1.1"
	oidIPAddressEntry            = "1.3.6.1.2.1.4.20.1"
	oidProcessorEntry            = "1.3.6.1.2.1.25.3.3.1"
	oidStorageEntry              = "1.3.6.1.2.1.25.2.3.1"
	oidStorageTypes              = "1.3.6.1.2.1.25.2.1"
	oidLLDPLocalPortEntry        = "1.0.8802.1.1.2.1.3.7.1"
	oidLLDPRemoteEntry           = "1.0.8802.1.1.2.1.4.1.1"
	oidLLDPRemoteManagementEntry = "1.0.8802.1.1.2.1.4.2.1"
)

func NewSNMPClient(ctx context.Context, target string, port uint16, settings api.SNMPCredential) (*gosnmp.GoSNMP, error) {
	client := &gosnmp.GoSNMP{
		Context:   ctx,
		Target:    target,
		Port:      port,
		Transport: "udp",
		Timeout:   conf.SNMP.Timeout,
		Retries:   conf.SNMP.Retries,
		MaxOids:   gosnmp.MaxOids,
	}

	switch settings.Version {
	case api.SNMPVersion1:
		client.Version = gosnmp.Version1
		client.Community = settings.Community
	case api.SNMPVersion2C:
		client.Version = gosnmp.Version2c
		client.Community = settings.Community
	case api.SNMPVersion3:
		flags, parameters, err := snmpSecurityParameters(settings)

		if err != nil {
			return nil, err
		}

		client.Version = gosnmp.Version3
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = flags
		client.SecurityParameters = parameters
	default:
		return nil, fmt.Errorf("NewSNMPClient: unsupported SNMP version %q, must be 1, 2c or 3", settings.Version)
	}

	return client, nil
}

func snmpSecurityParameters(settings api.SNMPCredential) (gosnmp.SnmpV3MsgFlags, *gosnmp.UsmSecurityParameters, error) {
	if len(settings.Username) < 1 {
		return 0, nil, errors.New("snmpSecurityParameters: a username is required for SNMP version 3")
	}

	parameters := &gosnmp.UsmSecurityParameters{
		UserName:               settings.Username,
		AuthenticationProtocol: gosnmp.NoAuth,
		PrivacyProtocol:        gosnmp.NoPriv,
	}

	if len(settings.AuthProtocol) < 1 {
		return gosnmp.NoAuthNoPriv, parameters, nil
	}

	authProtocol, err := snmpAuthProtocol(settings.AuthProtocol)

	if err != nil {
		return 0, nil, err
	}

	parameters.AuthenticationProtocol = authProtocol
	parameters.AuthenticationPassphrase = settings.AuthPassword

	if len(settings.PrivacyProtocol) < 1 {
		return gosnmp.AuthNoPriv, parameters, nil
	}

	privacyProtocol, err := snmpPrivacyProtocol(settings.PrivacyProtocol)

	if err != nil {
		return 0, nil, err
	}

	parameters.PrivacyProtocol = privacyProtocol
	parameters.PrivacyPassphrase = settings.PrivacyPassword

	return gosnmp.AuthPriv, parameters, nil
}

func snmpAuthProtocol(name string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch strings.ToUpper(name) {
	case "MD5":
		return gosnmp.MD5, nil
	case "SHA":
		return gosnmp.SHA, nil
	case "SHA224":
		return gosnmp.SHA224, nil
	case "SHA256":
		return gosnmp.SHA256, nil
	case "SHA384":
		return gosnmp.SHA384, nil
	case "SHA512":
		return gosnmp.SHA512, nil
	default:
		return 0, fmt.Errorf("snmpAuthProtocol: unsupported authentication protocol %q", name)
	}
}

func snmpPrivacyProtocol(name string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch strings.ToUpper(name) {
	case "DES":
		return gosnmp.DES, nil
	case "AES":
		return gosnmp.AES, nil
	case "AES192":
		return gosnmp.AES192, nil
	case "AES256":
		return gosnmp.AES256, nil
	case "AES192C":
		return gosnmp.AES192C, nil
	case "AES256C":
		return gosnmp.AES256C, nil
	default:
		return 0, fmt.Errorf("snmpPrivacyProtocol: unsupported privacy protocol %q", name)
	}
}

type snmpData struct {
	CredentialID    *string
	System          *api.System
	Interfaces      []api.Interface
	Neighbors       []api.Neighbor
	Inventory       []api.InventoryItem
	ForwardingTable []api.ForwardingEntry
	Resources       *api.Resources
}

// connectSNMP tries each of the device's credentials in order and returns a connected client for the first one the agent
// answers, along with the system details it returned and the credential. Agents silently drop requests with an unknown community, so a wrong
// credential costs one SNMP timeout before the next one is tried. Devices that did not answer on their last poll are
// probed without retries, since most of them will not answer this time either and each retry costs another timeout.
func connectSNMP(ctx context.Context, device api.Device, wasUnreachable bool) (*gosnmp.GoSNMP, *api.System, api.SNMPCredential, error) {
	if len(device.SNMP.Credentials) < 1 {
		return nil, nil, api.SNMPCredential{}, errors.New("connectSNMP: no SNMP credentials are configured for this device")
	}

	var lastErr error

	for _, credential := range device.SNMP.Credentials {
		client, err := NewSNMPClient(ctx, device.Host, device.SNMP.Port, credential)

		if err != nil {
			lastErr = err

			continue
		}

		if err = client.Connect(); err != nil {
			lastErr = err

			continue
		}

		if wasUnreachable {
			client.Retries = 0
		}

		system, err := collectSystem(client)

		if err == nil {
			client.Retries = conf.SNMP.Retries

			return client, system, credential, nil
		}

		client.Conn.Close()

		if ctx.Err() != nil {
			return nil, nil, api.SNMPCredential{}, ctx.Err()
		}

		lastErr = err
	}

	if len(device.SNMP.Credentials) == 1 {
		return nil, nil, api.SNMPCredential{}, lastErr
	}

	return nil, nil, api.SNMPCredential{}, fmt.Errorf("connectSNMP: none of the %d SNMP credentials received a response, last error: %w", len(device.SNMP.Credentials), lastErr)
}

// CollectSNMPData polls a device over SNMP. A status poll (discover false) reads only what changes from poll to poll:
// the system details, interface status and counters, and resources. A discovery also walks the tables that change
// less often (IP addresses, LLDP neighbors, inventory, transceivers and the forwarding table), which is most of the
// work on large devices.
func CollectSNMPData(ctx context.Context, device api.Device, polledAt time.Time, wasUnreachable, discover bool) (*snmpData, error) {
	client, system, credential, err := connectSNMP(ctx, device, wasUnreachable)

	if err != nil {
		return nil, err
	}

	defer client.Conn.Close()

	vendor := vendors.Identify(system.ObjectID)

	interfaces, err := collectInterfaces(client, polledAt.Add(-time.Duration(system.UptimeSeconds)*time.Second), discover)

	if err != nil {
		return nil, err
	}

	data := &snmpData{
		CredentialID: credential.ID,
		System:       system,
		Interfaces:   interfaces,
		Neighbors:    make([]api.Neighbor, 0),
	}

	if discover {
		if data.Neighbors, err = collectNeighbors(client, interfaces); err != nil {
			return nil, err
		}

		entities, err := entity.Walk(client)

		if err != nil {
			return nil, err
		}

		if err = collectOptics(client, vendor, entities, interfaces); err != nil {
			logger.Warn("Failed to collect transceiver diagnostics", "device", device.Name, "host", device.Host, "error", err)
		}

		data.Inventory = collectInventory(entities, interfaces)

		applyInventoryTransceivers(data.Inventory, entities, interfaces)
		applyHardwareDetails(system, data.Inventory, vendor)

		if vendor.Hardware != nil {
			if err = vendor.Hardware(client, system); err != nil {
				logger.Warn("Failed to collect vendor hardware details", "device", device.Name, "host", device.Host, "vendor", vendor.Name, "error", err)
			}
		}

		// Left nil when it fails, so the polling server keeps the forwarding table from the last discovery
		if data.ForwardingTable, err = collectForwardingTable(ctx, client, device, credential, vendor, interfaces); err != nil {
			logger.Warn("Failed to collect forwarding table", "device", device.Name, "host", device.Host, "error", err)
		}
	}

	resources, err := collectResources(client)

	if err != nil {
		return nil, err
	}

	if vendor.Resources != nil && (resources.CPULoadPercent == nil || resources.MemoryTotalBytes == nil) {
		if err = vendor.Resources(client, resources); err != nil {
			logger.Warn("Failed to collect vendor CPU and memory", "device", device.Name, "host", device.Host, "vendor", vendor.Name, "error", err)
		}
	}

	data.Resources = resources

	return data, nil
}

func collectSystem(client *gosnmp.GoSNMP) (*api.System, error) {
	packet, err := client.Get([]string{
		oidSystemDescription,
		oidSystemObjectID,
		oidSystemUptime,
		oidSystemContact,
		oidSystemName,
		oidSystemLocation,
	})

	if err != nil {
		return nil, err
	}

	if packet.Error != gosnmp.NoError {
		return nil, fmt.Errorf("collectSystem: agent responded with error %s", packet.Error)
	}

	system := &api.System{}

	for _, pdu := range packet.Variables {
		switch strings.TrimPrefix(pdu.Name, ".") {
		case oidSystemDescription:
			system.Description = snmp.PDUString(pdu)
		case oidSystemObjectID:
			system.ObjectID = snmp.PDUString(pdu)
		case oidSystemUptime:
			system.UptimeSeconds = snmp.PDUInt(pdu) / 100
		case oidSystemContact:
			system.Contact = snmp.PDUString(pdu)
		case oidSystemName:
			system.Name = snmp.PDUString(pdu)
		case oidSystemLocation:
			system.Location = snmp.PDUString(pdu)
		}
	}

	return system, nil
}

// collectInterfaces reads the interface tables. A status poll reads only the speed, status and counter columns (ifTable)
// and the name and 64-bit counters (ifXTable); a discovery also reads the description, type, MTU, MAC address, alias and
// IP addresses, which the polling server keeps from the last discovery in between.
func collectInterfaces(client *gosnmp.GoSNMP, bootedAt time.Time, discover bool) ([]api.Interface, error) {
	columns, extensionColumns := []int{5, 7, 8, 9, 10, 13, 14, 16, 19, 20}, []int{1, 6, 10, 15}

	if discover {
		columns, extensionColumns = append(columns, 2, 3, 4, 6), append(extensionColumns, 18)
	}

	rows, err := snmp.WalkTable(client, oidInterfaceEntry, columns...)

	if err != nil {
		return nil, err
	}

	extensionRows, err := snmp.WalkTable(client, oidInterfaceExtensionEntry, extensionColumns...)

	if err != nil {
		return nil, err
	}

	addresses := make(map[int][]string)

	if discover {
		if addresses, err = collectIPAddresses(client); err != nil {
			return nil, err
		}
	}

	interfaces := make([]api.Interface, 0, len(rows))

	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		extensionRow := extensionRows[index]
		ianaType := int(row.Int(3))

		item := api.Interface{
			Index:              interfaceIndex,
			Name:               extensionRow.String(1),
			Description:        row.String(2),
			Alias:              extensionRow.String(18),
			Type:               interfaceType(ianaType),
			MTU:                int(row.Int(4)),
			SpeedBitsPerSecond: interfaceSpeed(row, extensionRow),
			MACAddress:         formatMACAddress(row.Bytes(6)),
			AdminStatus:        interfaceStatus(row.Int(7)),
			OperStatus:         interfaceStatus(row.Int(8)),
			LastChangedAt:      interfaceLastChangedAt(bootedAt, row.Int(9)),
			Addresses:          addresses[interfaceIndex],
			InOctets:           rowCounter(extensionRow, 6, row, 10),
			OutOctets:          rowCounter(extensionRow, 10, row, 16),
			InDiscards:         row.Int(13),
			OutDiscards:        row.Int(19),
			InErrors:           row.Int(14),
			OutErrors:          row.Int(20),
		}

		if len(item.Name) < 1 {
			item.Name = item.Description
		}

		if item.Addresses == nil {
			item.Addresses = make([]string, 0)
		}

		interfaces = append(interfaces, item)
	}

	slices.SortFunc(interfaces, func(a, b api.Interface) int {
		return a.Index - b.Index
	})

	return interfaces, nil
}

func collectIPAddresses(client *gosnmp.GoSNMP) (map[int][]string, error) {
	rows, err := snmp.WalkTable(client, oidIPAddressEntry, 2, 3)

	if err != nil {
		return nil, err
	}

	addresses := make(map[int][]string)

	for index, row := range rows {
		ipAddress := net.ParseIP(index).To4()
		mask := net.ParseIP(row.String(3)).To4()

		if ipAddress == nil || mask == nil {
			continue
		}

		prefixLength, _ := net.IPMask(mask).Size()
		interfaceIndex := int(row.Int(2))

		addresses[interfaceIndex] = append(addresses[interfaceIndex], fmt.Sprintf("%s/%d", ipAddress, prefixLength))
	}

	for _, list := range addresses {
		slices.Sort(list)
	}

	return addresses, nil
}

func collectNeighbors(client *gosnmp.GoSNMP, interfaces []api.Interface) ([]api.Neighbor, error) {
	localPorts, err := snmp.WalkTable(client, oidLLDPLocalPortEntry, 2, 3, 4)

	if err != nil {
		return nil, err
	}

	rows, err := snmp.WalkTable(client, oidLLDPRemoteEntry, 4, 5, 6, 7, 8, 9, 10)

	if err != nil {
		return nil, err
	}

	managementAddresses, err := collectLLDPManagementAddresses(client)

	if err != nil {
		return nil, err
	}

	neighbors := make([]api.Neighbor, 0, len(rows))

	for index, row := range rows {
		parts := strings.Split(index, ".")

		if len(parts) != 3 {
			continue
		}

		localPort := localPorts[parts[1]]

		item := api.Neighbor{
			LocalPortID:          lldpIdentifier(localPort.Int(2), localPort.Bytes(3), 3, 4),
			LocalPortDescription: localPort.String(4),
			ChassisID:            lldpIdentifier(row.Int(4), row.Bytes(5), 4, 5),
			PortID:               lldpIdentifier(row.Int(6), row.Bytes(7), 3, 4),
			PortDescription:      row.String(8),
			SystemName:           row.String(9),
			SystemDescription:    row.String(10),
			ManagementAddresses:  managementAddresses[index],
		}

		item.InterfaceIndex = lldpInterfaceIndex(parts[1], item.LocalPortID, item.LocalPortDescription, interfaces)

		if item.ManagementAddresses == nil {
			item.ManagementAddresses = make([]string, 0)
		}

		neighbors = append(neighbors, item)
	}

	slices.SortFunc(neighbors, func(a, b api.Neighbor) int {
		if c := strings.Compare(a.LocalPortID, b.LocalPortID); c != 0 {
			return c
		}

		return strings.Compare(a.SystemName, b.SystemName)
	})

	return neighbors, nil
}

func collectLLDPManagementAddresses(client *gosnmp.GoSNMP) (map[string][]string, error) {
	rows, err := snmp.WalkTable(client, oidLLDPRemoteManagementEntry, 3, 4, 5)

	if err != nil {
		return nil, err
	}

	addresses := make(map[string][]string)

	for index := range rows {
		parts := strings.Split(index, ".")

		if len(parts) < 5 {
			continue
		}

		length, err := strconv.Atoi(parts[4])

		if err != nil || len(parts) != 5+length {
			continue
		}

		octets := make([]byte, 0, length)

		for _, part := range parts[5:] {
			value, err := strconv.ParseUint(part, 10, 8)

			if err != nil {
				break
			}

			octets = append(octets, byte(value))
		}

		if (parts[3] != "1" || len(octets) != net.IPv4len) && (parts[3] != "2" || len(octets) != net.IPv6len) {
			continue
		}

		key := strings.Join(parts[:3], ".")
		ipAddress := net.IP(octets).String()

		if !slices.Contains(addresses[key], ipAddress) {
			addresses[key] = append(addresses[key], ipAddress)
		}
	}

	for _, list := range addresses {
		slices.Sort(list)
	}

	return addresses, nil
}

// collectInventory lists the physical parts of a device: every entity except sensors that has a model or serial number
// or belongs to an interface (ports and the transceivers in them)
func collectInventory(entities *entity.Table, interfaces []api.Interface) []api.InventoryItem {
	inventory := make([]api.InventoryItem, 0)

	for _, index := range snmp.SortedIndexes(entities.Rows) {
		row := entities.Rows[index]
		entityIndex, err := strconv.Atoi(index)

		if err != nil || row.Int(entity.ColumnClass) == entity.ClassSensor {
			continue
		}

		item := api.InventoryItem{
			Index:           entityIndex,
			Class:           inventoryClass(row.Int(entity.ColumnClass)),
			Name:            row.String(entity.ColumnName),
			Description:     row.String(entity.ColumnDescription),
			Manufacturer:    row.String(entity.ColumnManufacturer),
			Model:           row.String(entity.ColumnModel),
			SerialNumber:    row.String(entity.ColumnSerialNumber),
			HardwareVersion: row.String(entity.ColumnHardwareVersion),
			FirmwareVersion: row.String(entity.ColumnFirmwareVersion),
			SoftwareVersion: row.String(entity.ColumnSoftwareVersion),
		}

		if parent := row.Int(entity.ColumnContainedIn); parent > 0 {
			item.ParentIndex = PointerOf(int(parent))
		}

		if interfaceIndex, ok := entities.Interface(index, interfaces); ok {
			item.InterfaceIndex = PointerOf(interfaceIndex)
		}

		if len(item.Model) < 1 && len(item.SerialNumber) < 1 && item.InterfaceIndex == nil {
			continue
		}

		inventory = append(inventory, item)
	}

	return inventory
}

// applyInventoryTransceivers fills in the transceiver of interfaces the vendor's optics tables did not identify, from
// the innermost inventory item belonging to the interface that has a model or serial number (the transceiver, rather
// than the port holding it)
func applyInventoryTransceivers(inventory []api.InventoryItem, entities *entity.Table, interfaces []api.Interface) {
	innermost := make(map[int]api.InventoryItem)
	depths := make(map[int]int)

	for _, item := range inventory {
		if item.InterfaceIndex == nil || item.Class == api.InventoryClassChassis || (len(item.Model) < 1 && len(item.SerialNumber) < 1) {
			continue
		}

		depth := entities.Depth(strconv.Itoa(item.Index))

		if current, ok := depths[*item.InterfaceIndex]; ok && current >= depth {
			continue
		}

		innermost[*item.InterfaceIndex] = item
		depths[*item.InterfaceIndex] = depth
	}

	for i := range interfaces {
		item, ok := innermost[interfaces[i].Index]

		if !ok || interfaces[i].Transceiver != nil {
			continue
		}

		interfaces[i].Transceiver = &api.Transceiver{
			Type:            item.Description,
			Manufacturer:    item.Manufacturer,
			Model:           item.Model,
			SerialNumber:    item.SerialNumber,
			HardwareVersion: item.HardwareVersion,
		}
	}
}

func collectResources(client *gosnmp.GoSNMP) (*api.Resources, error) {
	processors, err := snmp.WalkTable(client, oidProcessorEntry, 2)

	if err != nil {
		return nil, err
	}

	rows, err := snmp.WalkTable(client, oidStorageEntry, 2, 3, 4, 5, 6)

	if err != nil {
		return nil, err
	}

	resources := &api.Resources{
		Storage: make([]api.Storage, 0, len(rows)),
	}

	var (
		processorCount int64
		processorTotal int64
	)

	for _, row := range processors {
		if load, ok := row.Number(2); ok {
			processorCount++
			processorTotal += load
		}
	}

	if processorCount > 0 {
		resources.CPULoadPercent = PointerOf(float64(processorTotal) / float64(processorCount))
	}

	for index, row := range rows {
		storageIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		allocationUnits := row.Int(4)

		resources.Storage = append(resources.Storage, api.Storage{
			Index:       storageIndex,
			Type:        storageType(row.String(2)),
			Description: row.String(3),
			SizeBytes:   row.Int(5) * allocationUnits,
			UsedBytes:   row.Int(6) * allocationUnits,
		})
	}

	slices.SortFunc(resources.Storage, func(a, b api.Storage) int {
		return a.Index - b.Index
	})

	for _, storage := range resources.Storage {
		if storage.Type == api.StorageTypeRAM {
			resources.MemoryTotalBytes = PointerOf(storage.SizeBytes)
			resources.MemoryUsedBytes = PointerOf(storage.UsedBytes)

			break
		}
	}

	return resources, nil
}

func applyHardwareDetails(system *api.System, inventory []api.InventoryItem, vendor *vendors.Vendor) {
	for _, item := range inventory {
		if item.Class != api.InventoryClassChassis || (len(item.Model) < 1 && len(item.SerialNumber) < 1) {
			continue
		}

		system.Manufacturer = item.Manufacturer
		system.Model = item.Model
		system.SerialNumber = item.SerialNumber
		system.HardwareVersion = item.HardwareVersion
		system.FirmwareVersion = item.FirmwareVersion
		system.SoftwareVersion = item.SoftwareVersion

		break
	}

	if len(vendor.Name) > 0 {
		system.Manufacturer = vendor.Name
	}

	if len(system.Model) < 1 && vendor.Model != nil {
		system.Model = vendor.Model(system.Description)
	}
}

func rowCounter(row snmp.Row, column int, fallbackRow snmp.Row, fallbackColumn int) int64 {
	if value, ok := row.Number(column); ok {
		return value
	}

	return fallbackRow.Int(fallbackColumn)
}

func formatMACAddress(value []byte) *string {
	if len(value) != 6 {
		return nil
	}

	return PointerOf(formatHexOctets(value))
}

func formatHexOctets(value []byte) string {
	parts := make([]string, len(value))

	for i, octet := range value {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{octet}))
	}

	return strings.Join(parts, ":")
}

func formatPrintableOrHex(value []byte) string {
	text := strings.TrimRight(string(value), "\x00")

	if !utf8.ValidString(text) || strings.IndexFunc(text, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return formatHexOctets(value)
	}

	return strings.TrimSpace(text)
}

func lldpIdentifier(subtype int64, value []byte, macAddressSubtype, networkAddressSubtype int64) string {
	switch subtype {
	case macAddressSubtype:
		if address := formatMACAddress(value); address != nil {
			return *address
		}
	case networkAddressSubtype:
		if (len(value) == 1+net.IPv4len && value[0] == 1) || (len(value) == 1+net.IPv6len && value[0] == 2) {
			return net.IP(value[1:]).String()
		}
	}

	return formatPrintableOrHex(value)
}

func lldpInterfaceIndex(localPortNumber, localPortID, localPortDescription string, interfaces []api.Interface) *int {
	for _, candidate := range []string{localPortID, localPortDescription} {
		if len(candidate) < 1 {
			continue
		}

		for _, item := range interfaces {
			if candidate == item.Name || candidate == item.Description || candidate == item.Alias {
				return PointerOf(item.Index)
			}
		}
	}

	number, err := strconv.Atoi(localPortNumber)

	if err != nil {
		return nil
	}

	for _, item := range interfaces {
		if item.Index == number {
			return PointerOf(item.Index)
		}
	}

	return nil
}

func interfaceSpeed(row, extensionRow snmp.Row) int64 {
	if highSpeed := extensionRow.Int(15); highSpeed > 0 {
		return highSpeed * 1_000_000
	}

	return row.Int(5)
}

func interfaceLastChangedAt(bootedAt time.Time, ticks int64) *time.Time {
	if ticks <= 0 {
		return nil
	}

	return PointerOf(bootedAt.Add(time.Duration(ticks) * time.Millisecond * 10))
}

func interfaceStatus(value int64) string {
	switch value {
	case 1:
		return api.InterfaceStatusUp
	case 2:
		return api.InterfaceStatusDown
	case 3:
		return api.InterfaceStatusTesting
	case 5:
		return api.InterfaceStatusDormant
	case 6:
		return api.InterfaceStatusNotPresent
	case 7:
		return api.InterfaceStatusLowerLayerDown
	default:
		return api.InterfaceStatusUnknown
	}
}

func interfaceType(ianaType int) string {
	switch ianaType {
	case 6, 62, 69, 117:
		return api.InterfaceTypeEthernet
	case 71:
		return api.InterfaceTypeWireless
	case 24:
		return api.InterfaceTypeLoopback
	case 135, 136:
		return api.InterfaceTypeVLAN
	case 161:
		return api.InterfaceTypeLAG
	case 131:
		return api.InterfaceTypeTunnel
	case 209:
		return api.InterfaceTypeBridge
	case 23, 108:
		return api.InterfaceTypePPP
	case 53:
		return api.InterfaceTypeVirtual
	default:
		return api.InterfaceTypeOther
	}
}

// inventoryClass names an entPhysicalClass value
func inventoryClass(value int64) string {
	switch value {
	case entity.ClassChassis:
		return api.InventoryClassChassis
	case 4:
		return api.InventoryClassBackplane
	case 5:
		return api.InventoryClassContainer
	case 6:
		return api.InventoryClassPowerSupply
	case 7:
		return api.InventoryClassFan
	case entity.ClassModule:
		return api.InventoryClassModule
	case entity.ClassPort:
		return api.InventoryClassPort
	case 11:
		return api.InventoryClassStack
	case 12:
		return api.InventoryClassCPU
	default:
		return api.InventoryClassOther
	}
}

func storageType(oid string) string {
	switch strings.TrimPrefix(oid, oidStorageTypes+".") {
	case "2":
		return api.StorageTypeRAM
	case "3":
		return api.StorageTypeVirtualMemory
	case "4":
		return api.StorageTypeFixedDisk
	case "5":
		return api.StorageTypeRemovableDisk
	case "9":
		return api.StorageTypeFlash
	default:
		return api.StorageTypeOther
	}
}
