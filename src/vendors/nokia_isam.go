package vendors

import (
	"main/src/api"
	"main/src/cli"
	"main/src/snmp"
	"regexp"

	"github.com/gosnmp/gosnmp"
)

// Nokia ISAM access nodes and OLTs (7302, 7330, 7360 FX), formerly Alcatel-Lucent, implement neither HOST-RESOURCES-MIB
// nor ENTITY-MIB hardware, so ASAM-SYSTEM-MIB and ASAM-EQUIP-MIB are read instead
const (
	oidISAMSoftwareVersion  = "1.3.6.1.4.1.637.61.1.9.28.1.0" // swEtsiVersion
	oidISAMCPULoadEntry     = "1.3.6.1.4.1.637.61.1.9.29.1.1" // cpuLoadEntry, one row per board slot
	oidISAMMemoryUsageEntry = "1.3.6.1.4.1.637.61.1.9.29.2.1" // memUsageEntry, one row per board slot
	oidISAMHolderEntry      = "1.3.6.1.4.1.637.61.1.23.2.1"   // eqptHolderEntry: the system, racks and shelves

	// eqptHolderEntry columns, and the holder that monitoring tools read the shelf from
	isamHolderActualType   = 4
	isamHolderSerialNumber = 13
	isamMainHolder         = "17"
)

var nokiaISAM = &Vendor{
	Name:              "Nokia",
	EnterpriseNumbers: []string{"637"},
	Hardware:          isamHardware,
	Resources:         isamResources,
	ConfigPlatforms:   []*ConfigPlatform{nokiaISAMConfig},
}

// nokiaISAMConfig turns off alarm messages and paging (batch mode) before printing the configuration as flat
// commands. Its prompt is the shelf type and user, such as "typ:isadmin>#".
var nokiaISAMConfig = &ConfigPlatform{
	ID:   "NOKIA_ISAM",
	Name: "Nokia ISAM",
	Profile: cli.Profile{
		Prompt:   regexp.MustCompile(`^[\w.\-]+:[\w.\-]+[>#]+ ?$`),
		Commands: fixedCommands("environment inhibit-alarms mode batch terminal-timeout timeout:360", "info configure flat"),
	},
}

// isamHardware reads the shelf type and serial number, and the software version
func isamHardware(client *gosnmp.GoSNMP, system *api.System) error {
	rows, err := snmp.WalkTable(client, oidISAMHolderEntry, isamHolderActualType, isamHolderSerialNumber)

	if err != nil {
		return err
	}

	applyISAMHolder(system, rows)

	version, err := snmp.GetRow(client, oidISAMSoftwareVersion)

	if err != nil {
		return err
	}

	if len(system.SoftwareVersion) < 1 {
		system.SoftwareVersion = version.String(0)
	}

	return nil
}

// applyISAMHolder reads the shelf type and serial number from the main holder, or the first holder that has a serial
// number when there is no main holder
func applyISAMHolder(system *api.System, rows map[string]snmp.Row) {
	row, ok := rows[isamMainHolder]

	if !ok || len(row.String(isamHolderSerialNumber)) < 1 {
		for _, index := range snmp.SortedIndexes(rows) {
			if len(rows[index].String(isamHolderSerialNumber)) > 0 {
				row, ok = rows[index], true

				break
			}
		}
	}

	if !ok {
		return
	}

	if holderType := row.String(isamHolderActualType); len(system.Model) < 1 && holderType != "EMPTY" && holderType != "UNKNOWN" {
		system.Model = holderType
	}

	if len(system.SerialNumber) < 1 {
		system.SerialNumber = row.String(isamHolderSerialNumber)
	}
}

// isamResources reads the CPU load, averaged over every board, and the memory, totalled over every board
func isamResources(client *gosnmp.GoSNMP, resources *api.Resources) error {
	loads, err := snmp.WalkTable(client, oidISAMCPULoadEntry, 4)

	if err != nil {
		return err
	}

	memory, err := snmp.WalkTable(client, oidISAMMemoryUsageEntry, 1, 2)

	if err != nil {
		return err
	}

	applyISAMResources(resources, loads, memory)

	return nil
}

func applyISAMResources(resources *api.Resources, loads, memory map[string]snmp.Row) {
	var loadCount, loadTotal, totalMegabytes, usedMegabytes int64

	for _, row := range loads {
		if load, ok := row.Number(4); ok {
			loadCount++
			loadTotal += load
		}
	}

	for _, row := range memory {
		total, totalOK := row.Number(1)
		used, usedOK := row.Number(2)

		if totalOK && usedOK && total > 0 {
			totalMegabytes += total
			usedMegabytes += used
		}
	}

	if loadCount > 0 && resources.CPULoadPercent == nil {
		percent := float64(loadTotal) / float64(loadCount)
		resources.CPULoadPercent = &percent
	}

	if totalMegabytes > 0 && resources.MemoryTotalBytes == nil {
		total, used := totalMegabytes*1024*1024, usedMegabytes*1024*1024
		resources.MemoryTotalBytes, resources.MemoryUsedBytes = &total, &used
	}
}
