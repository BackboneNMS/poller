package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"main/src/api"
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
	oidEnterprises               = "1.3.6.1.4.1"
	oidSystemDescription         = "1.3.6.1.2.1.1.1.0"
	oidSystemObjectID            = "1.3.6.1.2.1.1.2.0"
	oidSystemUptime              = "1.3.6.1.2.1.1.3.0"
	oidSystemContact             = "1.3.6.1.2.1.1.4.0"
	oidSystemName                = "1.3.6.1.2.1.1.5.0"
	oidSystemLocation            = "1.3.6.1.2.1.1.6.0"
	oidInterfaceEntry            = "1.3.6.1.2.1.2.2.1"
	oidInterfaceExtensionEntry   = "1.3.6.1.2.1.31.1.1.1"
	oidIPAddressEntry            = "1.3.6.1.2.1.4.20.1"
	oidPhysicalEntityEntry       = "1.3.6.1.2.1.47.1.1.1.1"
	oidProcessorEntry            = "1.3.6.1.2.1.25.3.3.1"
	oidStorageEntry              = "1.3.6.1.2.1.25.2.3.1"
	oidStorageTypes              = "1.3.6.1.2.1.25.2.1"
	oidLLDPLocalPortEntry        = "1.0.8802.1.1.2.1.3.7.1"
	oidLLDPRemoteEntry           = "1.0.8802.1.1.2.1.4.1.1"
	oidLLDPRemoteManagementEntry = "1.0.8802.1.1.2.1.4.2.1"

	inventoryClassChassis = "CHASSIS"
	inventoryClassOther   = "OTHER"
)

type snmpRow map[int]gosnmp.SnmpPDU

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
	CredentialID *string
	System       *api.System
	Interfaces   []api.Interface
	Neighbors    []api.Neighbor
	Resources    *api.Resources
}

type inventoryItem struct {
	Class           string
	Manufacturer    string
	Model           string
	SerialNumber    string
	HardwareVersion string
	FirmwareVersion string
	SoftwareVersion string
}

// connectSNMP tries each of the device's credentials in order and returns a connected client for the first one the agent
// answers, along with the system details it returned. Agents silently drop requests with an unknown community, so a wrong
// credential costs one SNMP timeout before the next one is tried. Devices that did not answer on their last poll are
// probed without retries, since most of them will not answer this time either and each retry costs another timeout.
func connectSNMP(ctx context.Context, device api.Device, wasUnreachable bool) (*gosnmp.GoSNMP, *api.System, *string, error) {
	if len(device.SNMP.Credentials) < 1 {
		return nil, nil, nil, errors.New("connectSNMP: no SNMP credentials are configured for this device")
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

			return client, system, credential.ID, nil
		}

		client.Conn.Close()

		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}

		lastErr = err
	}

	if len(device.SNMP.Credentials) == 1 {
		return nil, nil, nil, lastErr
	}

	return nil, nil, nil, fmt.Errorf("connectSNMP: none of the %d SNMP credentials received a response, last error: %w", len(device.SNMP.Credentials), lastErr)
}

// CollectSNMPData polls a device over SNMP. A status poll (discover false) reads only what changes from poll to poll:
// the system details, interface status and counters, and resources. A discovery also walks the tables that rarely
// change (IP addresses, LLDP neighbors, inventory and transceiver optics), which is most of the work on large devices.
func CollectSNMPData(ctx context.Context, device api.Device, polledAt time.Time, wasUnreachable, discover bool) (*snmpData, error) {
	client, system, credentialID, err := connectSNMP(ctx, device, wasUnreachable)

	if err != nil {
		return nil, err
	}

	defer client.Conn.Close()

	interfaces, err := collectInterfaces(client, polledAt.Add(-time.Duration(system.UptimeSeconds)*time.Second), discover)

	if err != nil {
		return nil, err
	}

	neighbors := make([]api.Neighbor, 0)

	if discover {
		if neighbors, err = collectNeighbors(client, interfaces); err != nil {
			return nil, err
		}

		physicalRows, err := walkTable(client, oidPhysicalEntityEntry, 2, 4, 5, 7, 8, 9, 10, 11, 12, 13)

		if err != nil {
			return nil, err
		}

		if err = collectOptics(client, objectIDEnterpriseNumber(system.ObjectID), physicalRows, interfaces); err != nil {
			logger.Warn("Failed to collect transceiver light levels", "device", device.Name, "host", device.Host, "error", err)
		}

		applyHardwareDetails(system, collectInventory(physicalRows))
	}

	resources, err := collectResources(client)

	if err != nil {
		return nil, err
	}

	return &snmpData{
		CredentialID: credentialID,
		System:       system,
		Interfaces:   interfaces,
		Neighbors:    neighbors,
		Resources:    resources,
	}, nil
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
			system.Description = pduString(pdu)
		case oidSystemObjectID:
			system.ObjectID = pduString(pdu)
		case oidSystemUptime:
			system.UptimeSeconds = pduInt(pdu) / 100
		case oidSystemContact:
			system.Contact = pduString(pdu)
		case oidSystemName:
			system.Name = pduString(pdu)
		case oidSystemLocation:
			system.Location = pduString(pdu)
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

	rows, err := walkTable(client, oidInterfaceEntry, columns...)

	if err != nil {
		return nil, err
	}

	extensionRows, err := walkTable(client, oidInterfaceExtensionEntry, extensionColumns...)

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
		ianaType := int(rowInt(row, 3))

		item := api.Interface{
			Index:              interfaceIndex,
			Name:               rowString(extensionRow, 1),
			Description:        rowString(row, 2),
			Alias:              rowString(extensionRow, 18),
			Type:               interfaceType(ianaType),
			MTU:                int(rowInt(row, 4)),
			SpeedBitsPerSecond: interfaceSpeed(row, extensionRow),
			MACAddress:         formatMACAddress(rowBytes(row, 6)),
			AdminStatus:        interfaceStatus(rowInt(row, 7)),
			OperStatus:         interfaceStatus(rowInt(row, 8)),
			LastChangedAt:      interfaceLastChangedAt(bootedAt, rowInt(row, 9)),
			Addresses:          addresses[interfaceIndex],
			InOctets:           rowCounter(extensionRow, 6, row, 10),
			OutOctets:          rowCounter(extensionRow, 10, row, 16),
			InDiscards:         rowInt(row, 13),
			OutDiscards:        rowInt(row, 19),
			InErrors:           rowInt(row, 14),
			OutErrors:          rowInt(row, 20),
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
	rows, err := walkTable(client, oidIPAddressEntry, 2, 3)

	if err != nil {
		return nil, err
	}

	addresses := make(map[int][]string)

	for index, row := range rows {
		ipAddress := net.ParseIP(index).To4()
		mask := net.ParseIP(rowString(row, 3)).To4()

		if ipAddress == nil || mask == nil {
			continue
		}

		prefixLength, _ := net.IPMask(mask).Size()
		interfaceIndex := int(rowInt(row, 2))

		addresses[interfaceIndex] = append(addresses[interfaceIndex], fmt.Sprintf("%s/%d", ipAddress, prefixLength))
	}

	for _, list := range addresses {
		slices.Sort(list)
	}

	return addresses, nil
}

func collectNeighbors(client *gosnmp.GoSNMP, interfaces []api.Interface) ([]api.Neighbor, error) {
	localPorts, err := walkTable(client, oidLLDPLocalPortEntry, 2, 3, 4)

	if err != nil {
		return nil, err
	}

	rows, err := walkTable(client, oidLLDPRemoteEntry, 4, 5, 6, 7, 8, 9, 10)

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
			LocalPortID:          lldpIdentifier(rowInt(localPort, 2), rowBytes(localPort, 3), 3, 4),
			LocalPortDescription: rowString(localPort, 4),
			ChassisID:            lldpIdentifier(rowInt(row, 4), rowBytes(row, 5), 4, 5),
			PortID:               lldpIdentifier(rowInt(row, 6), rowBytes(row, 7), 3, 4),
			PortDescription:      rowString(row, 8),
			SystemName:           rowString(row, 9),
			SystemDescription:    rowString(row, 10),
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
	rows, err := walkTable(client, oidLLDPRemoteManagementEntry, 3, 4, 5)

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

func collectInventory(rows map[string]snmpRow) []inventoryItem {
	indexes := make([]int, 0, len(rows))

	for index := range rows {
		if entityIndex, err := strconv.Atoi(index); err == nil {
			indexes = append(indexes, entityIndex)
		}
	}

	slices.Sort(indexes)

	inventory := make([]inventoryItem, 0)

	for _, entityIndex := range indexes {
		row := rows[strconv.Itoa(entityIndex)]

		item := inventoryItem{
			Class:           inventoryClass(rowInt(row, 5)),
			Manufacturer:    rowString(row, 12),
			Model:           rowString(row, 13),
			SerialNumber:    rowString(row, 11),
			HardwareVersion: rowString(row, 8),
			FirmwareVersion: rowString(row, 9),
			SoftwareVersion: rowString(row, 10),
		}

		if len(item.Model) < 1 && len(item.SerialNumber) < 1 {
			continue
		}

		inventory = append(inventory, item)
	}

	return inventory
}

func collectResources(client *gosnmp.GoSNMP) (*api.Resources, error) {
	processors, err := walkTable(client, oidProcessorEntry, 2)

	if err != nil {
		return nil, err
	}

	rows, err := walkTable(client, oidStorageEntry, 2, 3, 4, 5, 6)

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
		if load, ok := rowNumber(row, 2); ok {
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

		allocationUnits := rowInt(row, 4)

		resources.Storage = append(resources.Storage, api.Storage{
			Index:       storageIndex,
			Type:        storageType(rowString(row, 2)),
			Description: rowString(row, 3),
			SizeBytes:   rowInt(row, 5) * allocationUnits,
			UsedBytes:   rowInt(row, 6) * allocationUnits,
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

func applyHardwareDetails(system *api.System, inventory []inventoryItem) {
	for _, item := range inventory {
		if item.Class != inventoryClassChassis {
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

	enterpriseNumber := objectIDEnterpriseNumber(system.ObjectID)

	if manufacturer := enterpriseManufacturer(enterpriseNumber); len(manufacturer) > 0 {
		system.Manufacturer = manufacturer
	}

	if len(system.Model) < 1 {
		system.Model = descriptionModel(enterpriseNumber, system.Description)
	}
}

func objectIDEnterpriseNumber(objectID string) string {
	suffix, ok := strings.CutPrefix(objectID, oidEnterprises+".")

	if !ok {
		return ""
	}

	number, _, _ := strings.Cut(suffix, ".")

	return number
}

func enterpriseManufacturer(enterpriseNumber string) string {
	switch enterpriseNumber {
	case "9":
		return "Cisco"
	case "11", "232":
		return "Hewlett Packard"
	case "161", "17713":
		return "Cambium"
	case "2509":
		return "Aviat"
	case "2636":
		return "Juniper"
	case "3808":
		return "CyberPower"
	case "14988":
		return "MikroTik"
	case "41112":
		return "Ubiquiti"
	case "46242":
		return "Netonix"
	case "52642":
		return "FiberStore"
	default:
		return ""
	}
}

func descriptionModel(enterpriseNumber, description string) string {
	fields := strings.Fields(description)

	switch {
	case enterpriseNumber == "14988" && len(fields) > 1 && fields[0] == "RouterOS":
		return fields[1]
	case enterpriseNumber == "46242" && len(fields) > 1 && fields[0] == "Netonix":
		return fields[1]
	case enterpriseNumber == "2636" && len(fields) > 3 && strings.HasPrefix(description, "Juniper Networks, Inc. "):
		return strings.ToUpper(fields[3])
	default:
		return ""
	}
}

func rowString(row snmpRow, column int) string {
	pdu, ok := row[column]

	if !ok {
		return ""
	}

	return pduString(pdu)
}

func rowBytes(row snmpRow, column int) []byte {
	pdu, ok := row[column]

	if !ok || pdu.Type != gosnmp.OctetString {
		return nil
	}

	value, _ := pdu.Value.([]byte)

	return value
}

func rowInt(row snmpRow, column int) int64 {
	value, _ := rowNumber(row, column)

	return value
}

func rowNumber(row snmpRow, column int) (int64, bool) {
	pdu, ok := row[column]

	if !ok {
		return 0, false
	}

	switch pdu.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64, gosnmp.Uinteger32:
		return pduInt(pdu), true
	default:
		return 0, false
	}
}

func rowCounter(row snmpRow, column int, fallbackRow snmpRow, fallbackColumn int) int64 {
	if value, ok := rowNumber(row, column); ok {
		return value
	}

	return rowInt(fallbackRow, fallbackColumn)
}

func pduString(pdu gosnmp.SnmpPDU) string {
	switch pdu.Type {
	case gosnmp.OctetString:
		value, _ := pdu.Value.([]byte)

		return strings.TrimSpace(strings.ToValidUTF8(strings.TrimRight(string(value), "\x00"), ""))
	case gosnmp.ObjectIdentifier, gosnmp.IPAddress:
		value, _ := pdu.Value.(string)

		return strings.TrimPrefix(value, ".")
	default:
		return ""
	}
}

func pduInt(pdu gosnmp.SnmpPDU) int64 {
	return gosnmp.ToBigInt(pdu.Value).Int64()
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

func interfaceSpeed(row, extensionRow snmpRow) int64 {
	if highSpeed := rowInt(extensionRow, 15); highSpeed > 0 {
		return highSpeed * 1_000_000
	}

	return rowInt(row, 5)
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

func inventoryClass(value int64) string {
	if value == 3 {
		return inventoryClassChassis
	}

	return inventoryClassOther
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
