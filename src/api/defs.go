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

	SNMPVersion1  = "1"
	SNMPVersion2C = "2c"
	SNMPVersion3  = "3"
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
// and are sent without credentials.
type Device struct {
	ID                   string     `json:"_id"`
	Name                 string     `json:"name"`
	Host                 string     `json:"host"`
	PingOnly             bool       `json:"pingOnly"`
	SNMP                 DeviceSNMP `json:"snmp"`
	Discover             bool       `json:"discover"`
	DiscoveryRequestedAt *time.Time `json:"discoveryRequestedAt"`
	LastDiscoveredAt     *time.Time `json:"lastDiscoveredAt"`
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

// PollResult is the outcome of polling one device. Discovery is true for a full discovery. A status poll (false)
// reports the system details, interface status and counters, and resources only: interfaces carry just their index,
// name, speed, status and counters, and neighbors are empty, so the polling server merges it into the last discovery.
type PollResult struct {
	DeviceID       string      `json:"deviceID"`
	Discovery      bool        `json:"discovery"`
	PolledAt       time.Time   `json:"polledAt"`
	PollDurationMS int64       `json:"pollDurationMS"`
	Online         bool        `json:"online"`
	Reachable      bool        `json:"reachable"`
	LatencyMS      *float64    `json:"latencyMS"`
	CredentialID   *string     `json:"credentialID"`
	Error          *string     `json:"error"`
	System         *System     `json:"system"`
	Interfaces     []Interface `json:"interfaces"`
	Neighbors      []Neighbor  `json:"neighbors"`
	Resources      *Resources  `json:"resources"`
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
	Index              int        `json:"index"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	Alias              string     `json:"alias"`
	Type               string     `json:"type"`
	MTU                int        `json:"mtu"`
	SpeedBitsPerSecond int64      `json:"speedBitsPerSecond"`
	MACAddress         *string    `json:"macAddress"`
	AdminStatus        string     `json:"adminStatus"`
	OperStatus         string     `json:"operStatus"`
	LastChangedAt      *time.Time `json:"lastChangedAt"`
	Addresses          []string   `json:"addresses"`
	InOctets           int64      `json:"inOctets"`
	OutOctets          int64      `json:"outOctets"`
	InDiscards         int64      `json:"inDiscards"`
	OutDiscards        int64      `json:"outDiscards"`
	InErrors           int64      `json:"inErrors"`
	OutErrors          int64      `json:"outErrors"`
	InBitsPerSecond    *int64     `json:"inBitsPerSecond"`
	OutBitsPerSecond   *int64     `json:"outBitsPerSecond"`
	Optical            *Optical   `json:"optical"`
}

// Optical holds the transceiver light levels of an interface, with one value per lane (a single value for most optics),
// and the alarm thresholds the transceiver defines for them when it reports any.
type Optical struct {
	RxPowerDBm   []float64          `json:"rxPowerDBm"`
	TxPowerDBm   []float64          `json:"txPowerDBm"`
	RxThresholds *OpticalThresholds `json:"rxThresholds"`
	TxThresholds *OpticalThresholds `json:"txThresholds"`
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

type Resources struct {
	CPULoadPercent   *float64  `json:"cpuLoadPercent"`
	MemoryTotalBytes *int64    `json:"memoryTotalBytes"`
	MemoryUsedBytes  *int64    `json:"memoryUsedBytes"`
	Storage          []Storage `json:"storage"`
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
