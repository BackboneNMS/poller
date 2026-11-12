package api

import "time"

const (
	InterfaceStatusUp             = "UP"
	InterfaceStatusDown           = "DOWN"
	InterfaceStatusTesting        = "TESTING"
	InterfaceStatusUnknown        = "UNKNOWN"
	InterfaceStatusDormant        = "DORMANT"
	InterfaceStatusNotPresent     = "NOT_PRESENT"
	InterfaceStatusLowerLayerDown = "LOWER_LAYER_DOWN"

	InterfaceTypeEthernet = "ETHERNET"
	InterfaceTypeWireless = "WIRELESS"
	InterfaceTypeLoopback = "LOOPBACK"
	InterfaceTypeVLAN     = "VLAN"
	InterfaceTypeLAG      = "LAG"
	InterfaceTypeTunnel   = "TUNNEL"
	InterfaceTypeBridge   = "BRIDGE"
	InterfaceTypePPP      = "PPP"
	InterfaceTypeVirtual  = "VIRTUAL"
	InterfaceTypeOther    = "OTHER"

	StorageTypeRAM           = "RAM"
	StorageTypeVirtualMemory = "VIRTUAL_MEMORY"
	StorageTypeFixedDisk     = "FIXED_DISK"
	StorageTypeRemovableDisk = "REMOVABLE_DISK"
	StorageTypeFlash         = "FLASH"
	StorageTypeOther         = "OTHER"

	InventoryClassChassis     = "CHASSIS"
	InventoryClassBackplane   = "BACKPLANE"
	InventoryClassContainer   = "CONTAINER"
	InventoryClassPowerSupply = "POWER_SUPPLY"
	InventoryClassFan         = "FAN"
	InventoryClassModule      = "MODULE"
	InventoryClassPort        = "PORT"
	InventoryClassStack       = "STACK"
	InventoryClassCPU         = "CPU"
	InventoryClassOther       = "OTHER"

	SNMPVersion1  = "1"
	SNMPVersion2C = "2c"
	SNMPVersion3  = "3"

	ConfigProtocolSSH    = "SSH"
	ConfigProtocolTelnet = "TELNET"
)

type DeviceList struct {
	PollerID       string   `json:"pollerID"`
	OrganizationID string   `json:"organizationID"`
	PollInterval   int      `json:"pollInterval"`
	Devices        []Device `json:"devices"`
}

// Device is a device for the poller to poll. Every device gets a status poll each cycle; Discover asks for a full
// discovery instead, which also walks the tables that rarely change (neighbors, addresses, inventory, optics).
// DiscoveryRequestedAt is set while someone has asked for a rediscovery from the dashboard, and LastDiscoveredAt orders
// the queue so the devices discovered longest ago go first. PingOnly devices are only pinged, never queried over SNMP,
// and are sent without credentials. Priority marks devices that were down or unknown at their last poll, which the
// poller polls before the rest of the cycle so outages and recoveries show up sooner. ConfigBackup is only set when
// the device's configuration is due to be backed up.
type Device struct {
	ID                   string        `json:"_id"`
	Name                 string        `json:"name"`
	Host                 string        `json:"host"`
	PingOnly             bool          `json:"pingOnly"`
	SNMP                 DeviceSNMP    `json:"snmp"`
	Discover             bool          `json:"discover"`
	DiscoveryRequestedAt *time.Time    `json:"discoveryRequestedAt"`
	LastDiscoveredAt     *time.Time    `json:"lastDiscoveredAt"`
	Priority             bool          `json:"priority"`
	ConfigBackup         *ConfigBackup `json:"configBackup"`
}

type DeviceSNMP struct {
	Port        uint16           `json:"port"`
	Credentials []SNMPCredential `json:"credentials"`
}

// SNMPCredential is one set of credentials to try, in the order the polling server sends them. ID is nil for credentials that
// belong to a single device, and set for the organization's shared credentials.
type SNMPCredential struct {
	ID              *string `json:"id"`
	Version         string  `json:"version"`
	Community       string  `json:"community"`
	Username        string  `json:"username"`
	AuthProtocol    string  `json:"authProtocol"`
	AuthPassword    string  `json:"authPassword"`
	PrivacyProtocol string  `json:"privacyProtocol"`
	PrivacyPassword string  `json:"privacyPassword"`
}

// ConfigBackup asks for a device's configuration to be read over SSH or Telnet. Platform is the ID of the command line
// to use, or empty to detect it from the ObjectID and Description (sysObjectID and sysDescr) of the last discovery.
// HostKey is the SSH host key fingerprint recorded at the first backup, which the device must still present, or empty
// to accept and record any. RequestedAt is set while someone has asked for a backup from the dashboard, and
// LastAttemptAt orders the queue so the devices backed up longest ago go first.
type ConfigBackup struct {
	Platform      string          `json:"platform"`
	ObjectID      string          `json:"objectID"`
	Description   string          `json:"description"`
	Protocol      string          `json:"protocol"`
	Port          uint16          `json:"port"`
	HostKey       string          `json:"hostKey"`
	Credentials   []CLICredential `json:"credentials"`
	RequestedAt   *time.Time      `json:"requestedAt"`
	LastAttemptAt *time.Time      `json:"lastAttemptAt"`
}

// CLICredential is one login to try, in the order the polling server sends them. ID is nil for a login that belongs
// to a single device, and set for the organization's shared logins.
type CLICredential struct {
	ID             *string `json:"id"`
	Username       string  `json:"username"`
	Password       string  `json:"password"`
	EnablePassword string  `json:"enablePassword"`
}

// ConfigResult is the outcome of backing up one device's configuration: Config on success, Error otherwise. Platform
// is the ID of the command line used, CredentialID the shared login that worked, and HostKey the SSH host key
// fingerprint the device presented (nil over Telnet, or when the connection failed before one was seen).
type ConfigResult struct {
	DeviceID     string    `json:"deviceID"`
	StartedAt    time.Time `json:"startedAt"`
	DurationMS   int64     `json:"durationMS"`
	Platform     *string   `json:"platform"`
	CredentialID *string   `json:"credentialID"`
	HostKey      *string   `json:"hostKey"`
	Config       *string   `json:"config"`
	Error        *string   `json:"error"`
}

// PollResult is the outcome of polling one device. Discovery is true for a full discovery. A status poll (false)
// reports the system details, interface status and counters, and resources only: interfaces carry just their index,
// name, speed, status and counters, and neighbors are empty, so the polling server merges it into the last discovery.
// Inventory and ForwardingTable are nil when they were not collected (status polls, or a discovery that failed to read
// them), which leaves the stored ones alone, and empty when the device has none.
type PollResult struct {
	DeviceID        string            `json:"deviceID"`
	Discovery       bool              `json:"discovery"`
	PolledAt        time.Time         `json:"polledAt"`
	PollDurationMS  int64             `json:"pollDurationMS"`
	Online          bool              `json:"online"`
	Reachable       bool              `json:"reachable"`
	LatencyMS       *float64          `json:"latencyMS"`
	CredentialID    *string           `json:"credentialID"`
	Error           *string           `json:"error"`
	System          *System           `json:"system"`
	Interfaces      []Interface       `json:"interfaces"`
	Neighbors       []Neighbor        `json:"neighbors"`
	Inventory       []InventoryItem   `json:"inventory"`
	ForwardingTable []ForwardingEntry `json:"forwardingTable"`
	Resources       *Resources        `json:"resources"`
}

type System struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ObjectID        string `json:"objectID"`
	Contact         string `json:"contact"`
	Location        string `json:"location"`
	UptimeSeconds   int64  `json:"uptimeSeconds"`
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	SerialNumber    string `json:"serialNumber"`
	HardwareVersion string `json:"hardwareVersion"`
	FirmwareVersion string `json:"firmwareVersion"`
	SoftwareVersion string `json:"softwareVersion"`
}

type Interface struct {
	Index              int          `json:"index"`
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	Alias              string       `json:"alias"`
	Type               string       `json:"type"`
	MTU                int          `json:"mtu"`
	SpeedBitsPerSecond int64        `json:"speedBitsPerSecond"`
	MACAddress         *string      `json:"macAddress"`
	AdminStatus        string       `json:"adminStatus"`
	OperStatus         string       `json:"operStatus"`
	LastChangedAt      *time.Time   `json:"lastChangedAt"`
	Addresses          []string     `json:"addresses"`
	InOctets           int64        `json:"inOctets"`
	OutOctets          int64        `json:"outOctets"`
	InDiscards         int64        `json:"inDiscards"`
	OutDiscards        int64        `json:"outDiscards"`
	InErrors           int64        `json:"inErrors"`
	OutErrors          int64        `json:"outErrors"`
	InBitsPerSecond    *int64       `json:"inBitsPerSecond"`
	OutBitsPerSecond   *int64       `json:"outBitsPerSecond"`
	Optical            *Optical     `json:"optical"`
	Transceiver        *Transceiver `json:"transceiver"`
}

// Optical holds the diagnostics of an interface's transceiver: its light levels with one value per lane (a single
// value for most optics), the alarm thresholds the transceiver defines for them when it reports any, and its
// temperature, supply voltage and laser bias current (per lane) when the device reports them.
type Optical struct {
	RxPowerDBm         []float64          `json:"rxPowerDBm"`
	TxPowerDBm         []float64          `json:"txPowerDBm"`
	RxThresholds       *OpticalThresholds `json:"rxThresholds"`
	TxThresholds       *OpticalThresholds `json:"txThresholds"`
	TemperatureCelsius *float64           `json:"temperatureCelsius"`
	VoltageVolts       *float64           `json:"voltageVolts"`
	BiasCurrentMA      []float64          `json:"biasCurrentMA"`
}

// Transceiver identifies the transceiver plugged into an interface. Type is what kind it is as the device describes
// it, such as a form factor ("SFP+") or a description ("SFP-10GBase-LR"); Model is the vendor's part number.
type Transceiver struct {
	Type            string   `json:"type"`
	Manufacturer    string   `json:"manufacturer"`
	Model           string   `json:"model"`
	SerialNumber    string   `json:"serialNumber"`
	HardwareVersion string   `json:"hardwareVersion"`
	WavelengthNM    *float64 `json:"wavelengthNM"`
}

// OpticalThresholds are the low and high alarm thresholds of a light level, shared by every lane of the transceiver.
type OpticalThresholds struct {
	LowDBm  float64 `json:"lowDBm"`
	HighDBm float64 `json:"highDBm"`
}

type Neighbor struct {
	InterfaceIndex       *int     `json:"interfaceIndex"`
	LocalPortID          string   `json:"localPortID"`
	LocalPortDescription string   `json:"localPortDescription"`
	ChassisID            string   `json:"chassisID"`
	PortID               string   `json:"portID"`
	PortDescription      string   `json:"portDescription"`
	SystemName           string   `json:"systemName"`
	SystemDescription    string   `json:"systemDescription"`
	ManagementAddresses  []string `json:"managementAddresses"`
}

// InventoryItem is a physical part of a device from ENTITY-MIB: a chassis, module, port, transceiver, power supply or
// fan. ParentIndex is the index of the item containing it (which may not be listed itself), and InterfaceIndex the
// interface it belongs to, for ports and the transceivers in them.
type InventoryItem struct {
	Index           int    `json:"index"`
	ParentIndex     *int   `json:"parentIndex"`
	Class           string `json:"class"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	SerialNumber    string `json:"serialNumber"`
	HardwareVersion string `json:"hardwareVersion"`
	FirmwareVersion string `json:"firmwareVersion"`
	SoftwareVersion string `json:"softwareVersion"`
	InterfaceIndex  *int   `json:"interfaceIndex"`
}

// ForwardingEntry is a MAC address the device learned on one of its interfaces (its bridge forwarding database), in
// a VLAN when the device reports which one
type ForwardingEntry struct {
	MACAddress     string `json:"macAddress"`
	InterfaceIndex int    `json:"interfaceIndex"`
	VLAN           *int   `json:"vlan"`
}

// Resources are a device's CPU load, memory and storage. MemoryUsedPercent is only set for devices that report memory
// as a percentage without sizes (TP-Link); otherwise it is worked out from the sizes.
type Resources struct {
	CPULoadPercent    *float64  `json:"cpuLoadPercent"`
	MemoryTotalBytes  *int64    `json:"memoryTotalBytes"`
	MemoryUsedBytes   *int64    `json:"memoryUsedBytes"`
	MemoryUsedPercent *float64  `json:"memoryUsedPercent"`
	Storage           []Storage `json:"storage"`
}

type Storage struct {
	Index       int    `json:"index"`
	Type        string `json:"type"`
	Description string `json:"description"`
	SizeBytes   int64  `json:"sizeBytes"`
	UsedBytes   int64  `json:"usedBytes"`
}

type submitResultsBody struct {
	Results []PollResult `json:"results"`
}

type submitConfigResultsBody struct {
	Results []ConfigResult `json:"results"`
}

type latestVersionResponse struct {
	Version string `json:"version"`
}
