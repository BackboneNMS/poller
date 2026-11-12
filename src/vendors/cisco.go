package vendors

import (
	"main/src/cli"
	"main/src/optics"
	"main/src/snmp"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidCiscoVTPVLANEntry = "1.3.6.1.4.1.9.9.46.1.3.1.1" // CISCO-VTP-MIB vtpVlanEntry, indexed by VTP domain and VLAN

	ciscoVLANStateOperational = 1
	ciscoVLANTypeEthernet     = 1
)

// cisco reads light levels from CISCO-ENTITY-SENSOR-MIB, Cisco's variant of ENTITY-SENSOR-MIB, which reports dBm
// directly, says which entity (the port) a sensor measures, and has alarm thresholds
var cisco = &Vendor{
	Name:              "Cisco",
	EnterpriseNumbers: []string{"9"},
	SensorTables: []optics.SensorTable{
		{
			EntryOID:             "1.3.6.1.4.1.9.9.91.1.1.1.1",
			DBmType:              14,
			MeasuredEntityColumn: 8,
			ThresholdEntryOID:    "1.3.6.1.4.1.9.9.91.1.2.1.1",
		},
	},
	ForwardingVLANs: ciscoForwardingVLANs,
	ConfigPlatforms: []*ConfigPlatform{ciscoIOSXR, ciscoNXOS, ciscoIOS},
}

// ciscoForwardingVLANs lists the operational Ethernet VLANs from CISCO-VTP-MIB, whose forwarding tables Cisco serves
// one VLAN at a time
func ciscoForwardingVLANs(client *gosnmp.GoSNMP) ([]int, error) {
	rows, err := snmp.WalkTable(client, oidCiscoVTPVLANEntry, 2, 3)

	if err != nil {
		return nil, err
	}

	return ciscoVLANs(rows), nil
}

// ciscoVLANs reads the VLAN IDs out of vtpVlanTable rows (state in column 2, type in column 3), skipping suspended
// VLANs and the FDDI and Token Ring defaults (1002 to 1005)
func ciscoVLANs(rows map[string]snmp.Row) []int {
	vlans := make([]int, 0, len(rows))

	for index, row := range rows {
		_, rawVLAN, ok := strings.Cut(index, ".")
		vlan, err := strconv.Atoi(rawVLAN)

		if !ok || err != nil || row.Int(2) != ciscoVLANStateOperational || row.Int(3) != ciscoVLANTypeEthernet {
			continue
		}

		if !slices.Contains(vlans, vlan) {
			vlans = append(vlans, vlan)
		}
	}

	slices.Sort(vlans)

	return vlans
}

// ciscoIOS covers IOS and IOS XE. The header lines and ntp clock-period (which IOS keeps adjusting) change without
// anyone touching the configuration.
var ciscoIOS = &ConfigPlatform{
	ID:          "CISCO_IOS",
	Name:        "Cisco IOS / IOS XE",
	Description: regexp.MustCompile(`\bIOS\b`),
	Clean: dropLines(
		`^Building configuration`,
		`^Current configuration ?:`,
		`^! (Last configuration change|NVRAM config last updated|No configuration change since last restart)`,
		`^ntp clock-period `,
	),
	Profile: cli.Profile{
		Prompt:   ciscoStylePrompt,
		Enable:   "enable",
		Commands: fixedCommands("terminal length 0", "terminal width 0", "show running-config"),
	},
}

var ciscoNXOS = &ConfigPlatform{
	ID:          "CISCO_NXOS",
	Name:        "Cisco NX-OS",
	Description: regexp.MustCompile(`NX-OS`),
	Clean:       dropLines(`^!Time:`, `^!Running configuration last done at:`),
	Profile: cli.Profile{
		Prompt:   ciscoStylePrompt,
		Commands: fixedCommands("terminal length 0", "show running-config"),
	},
}

// ciscoIOSXR prints the time before every command's output
var ciscoIOSXR = &ConfigPlatform{
	ID:          "CISCO_IOSXR",
	Name:        "Cisco IOS XR",
	Description: regexp.MustCompile(`IOS[ -]XR`),
	Clean: dropLines(
		`^\w{3} \w{3} +\d+ \d{1,2}:\d{2}:\d{2}(\.\d+)? \w+$`,
		`^Building configuration`,
		`^!! Last configuration change`,
	),
	Profile: cli.Profile{
		Prompt:   ciscoStylePrompt,
		Commands: fixedCommands("terminal length 0", "terminal width 0", "show running-config"),
	},
}
