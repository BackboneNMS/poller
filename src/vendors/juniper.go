package vendors

import (
	"main/src/api"
	"main/src/cli"
	"main/src/optics"
	"main/src/snmp"
	"regexp"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidJuniperDOMEntry     = "1.3.6.1.4.1.2636.3.60.1.1.1.1" // jnxDomCurrentEntry, indexed by ifIndex
	oidJuniperDOMLaneEntry = "1.3.6.1.4.1.2636.3.60.1.2.1.1" // jnxDomCurrentLaneEntry, indexed by ifIndex and lane
)

var juniper = &Vendor{
	Name:              "Juniper",
	EnterpriseNumbers: []string{"2636"},
	Model:             juniperModel,
	Optics:            juniperOptics,
	ConfigPlatforms:   []*ConfigPlatform{juniperJunos},
}

// juniperJunos logs root in to the FreeBSD shell (a "%" prompt), so it starts the CLI first. The commit line changes on
// every commit, and the routing engine line ("{master:0}") is printed before every prompt of a virtual chassis.
var juniperJunos = &ConfigPlatform{
	ID:    "JUNIPER_JUNOS",
	Name:  "Juniper Junos",
	Clean: dropLines(`^## Last commit:`, `^\{\w+(:\d+)?\}$`),
	Profile: cli.Profile{
		Prompt: regexp.MustCompile(`^[\w.\-@:]+[>#%] ?$`),
		Commands: func(prompt string) []string {
			commands := []string{"set cli screen-length 0", "set cli screen-width 0", "show configuration | no-more"}

			if strings.HasSuffix(strings.TrimSpace(prompt), "%") {
				return append([]string{"cli"}, commands...)
			}

			return commands
		},
	},
}

// juniperModel reads a sysDescr such as "Juniper Networks, Inc. ex3300-48p Ethernet Switch, kernel JUNOS 15.1R7.9"
func juniperModel(description string) string {
	if fields := strings.Fields(description); len(fields) > 3 && strings.HasPrefix(description, "Juniper Networks, Inc. ") {
		return strings.ToUpper(fields[3])
	}

	return ""
}

func juniperOptics(client *gosnmp.GoSNMP, _ []api.Interface) (optics.Readings, error) {
	laneRows, err := snmp.WalkTable(client, oidJuniperDOMLaneEntry, 6, 8)

	if err != nil {
		return nil, err
	}

	rows, err := snmp.WalkTable(client, oidJuniperDOMEntry, 5, 7, 9, 10, 17, 18)

	if err != nil {
		return nil, err
	}

	return juniperReadings(rows, laneRows), nil
}

// juniperReadings reads JUNIPER-DOM-MIB, which reports levels in hundredths of a dBm. Multi-lane optics (QSFP) are
// read per lane from the lane table, everything else from the per-interface table. Alarm thresholds only exist in the
// per-interface table and apply to every lane.
func juniperReadings(rows, laneRows map[string]snmp.Row) optics.Readings {
	readings := make(optics.Readings)

	for index, row := range laneRows {
		rawInterface, rawLane, ok := strings.Cut(index, ".")
		interfaceIndex, interfaceErr := strconv.Atoi(rawInterface)
		lane, laneErr := strconv.Atoi(rawLane)

		if !ok || interfaceErr != nil || laneErr != nil {
			continue
		}

		if value, ok := row.Number(6); ok {
			readings.Add(interfaceIndex, lane, true, float64(value)/100)
		}

		if value, ok := row.Number(8); ok {
			readings.Add(interfaceIndex, lane, false, float64(value)/100)
		}
	}

	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		if readings[interfaceIndex] == nil {
			if value, ok := row.Number(5); ok {
				readings.Add(interfaceIndex, 0, true, float64(value)/100)
			}

			if value, ok := row.Number(7); ok {
				readings.Add(interfaceIndex, 0, false, float64(value)/100)
			}
		}

		juniperThresholds(readings, interfaceIndex, true, row, 10, 9)
		juniperThresholds(readings, interfaceIndex, false, row, 18, 17)
	}

	return readings
}

// juniperThresholds reads the low and high alarm thresholds of one light level from their columns in jnxDomCurrentTable
// (receive power in 10 and 9, transmit power in 18 and 17)
func juniperThresholds(readings optics.Readings, interfaceIndex int, receive bool, row snmp.Row, lowColumn, highColumn int) {
	low, lowOK := row.Number(lowColumn)
	high, highOK := row.Number(highColumn)

	if lowOK && highOK {
		readings.SetThresholds(interfaceIndex, receive, float64(low)/100, float64(high)/100)
	}
}
