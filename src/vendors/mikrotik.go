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

const oidMikroTikOpticalEntry = "1.3.6.1.4.1.14988.1.1.19.1.1" // mtxrOpticalEntry

var mikroTik = &Vendor{
	Name:              "MikroTik",
	EnterpriseNumbers: []string{"14988"},
	Model:             mikroTikModel,
	Optics:            mikroTikOptics,
	ConfigPlatforms:   []*ConfigPlatform{mikroTikRouterOS},
}

// mikroTikRouterOS logs in with console options after the username: no colors (c), no terminal detection (t), and a
// wide, tall window (511w, 4098h) so nothing wraps or pages. The export's first line has the time it was made, which
// is cut down to the RouterOS version so upgrades still show up as changes. RouterOS 7 leaves secrets out of exports.
var mikroTikRouterOS = &ConfigPlatform{
	ID:   "MIKROTIK_ROUTEROS",
	Name: "MikroTik RouterOS",
	Clean: func(config string) string {
		return mikroTikExportTime.ReplaceAllString(config, "# $1")
	},
	Profile: cli.Profile{
		Prompt:         regexp.MustCompile(`^\[[^\]]+@[^\]]+\] ?[\w/ -]*> ?$`),
		UsernameSuffix: "+ct511w4098h",
		Newline:        "\r\n",
		Commands:       fixedCommands("/export"),
	},
}

var mikroTikExportTime = regexp.MustCompile(`(?m)^# \S+ \S+ (by RouterOS .*)$`)

// mikroTikModel reads a sysDescr such as "RouterOS CCR2004-16G-2S+"
func mikroTikModel(description string) string {
	if fields := strings.Fields(description); len(fields) > 1 && fields[0] == "RouterOS" {
		return fields[1]
	}

	return ""
}

func mikroTikOptics(client *gosnmp.GoSNMP, interfaces []api.Interface) (optics.Readings, error) {
	rows, err := snmp.WalkTable(client, oidMikroTikOpticalEntry, 2, 9, 10)

	if err != nil {
		return nil, err
	}

	return mikroTikReadings(rows, interfaces), nil
}

// mikroTikReadings reads MIKROTIK-MIB mtxrOpticalTable, which reports levels in thousandths of a dBm. Rows are
// matched to interfaces by name, falling back to the row index as the interface index.
func mikroTikReadings(rows map[string]snmp.Row, interfaces []api.Interface) optics.Readings {
	readings := make(optics.Readings)

	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		if item := optics.FindInterfaceByName(interfaces, row.String(2)); item != nil {
			interfaceIndex = item.Index
		}

		if value, ok := row.Number(10); ok {
			readings.Add(interfaceIndex, 0, true, float64(value)/1000)
		}

		if value, ok := row.Number(9); ok {
			readings.Add(interfaceIndex, 0, false, float64(value)/1000)
		}
	}

	return readings
}
