package vendors

import (
	"main/src/api"
	"main/src/optics"
	"main/src/snmp"
	"regexp"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// Nokia SR OS routers and switches (7750 SR, 7250 IXR, 7210 SAS and others), formerly Alcatel-Lucent TiMetra, implement
// neither HOST-RESOURCES-MIB nor ENTITY-MIB hardware or ENTITY-SENSOR-MIB optics, so TIMETRA-SYSTEM-MIB,
// TIMETRA-CHASSIS-MIB and TIMETRA-PORT-MIB are read instead. SR Linux shares the enterprise number; where it does not
// answer these tables, the standard MIBs are used as for any other device.
const (
	oidNokiaCPUUsage              = "1.3.6.1.4.1.6527.3.1.2.1.1.1.0"  // sgiCpuUsage, percent
	oidNokiaKbMemoryAvailable     = "1.3.6.1.4.1.6527.3.1.2.1.1.10.0" // sgiKbMemoryAvailable, kilobytes not allocated to pools
	oidNokiaKbMemoryPoolAllocated = "1.3.6.1.4.1.6527.3.1.2.1.1.11.0" // sgiKbMemoryPoolAllocated, kilobytes allocated to pools
	oidNokiaHardwareEntry         = "1.3.6.1.4.1.6527.3.1.2.2.1.8.1"  // tmnxHwEntry, indexed by chassis and hardware index
	oidNokiaDDMEntry              = "1.3.6.1.4.1.6527.3.1.2.2.4.31.1" // tmnxDigitalDiagMonitorEntry, indexed by chassis and port ID
	oidNokiaDDMLaneEntry          = "1.3.6.1.4.1.6527.3.1.2.2.4.66.1" // tmnxDDMLaneEntry, indexed by chassis, port ID and lane

	// tmnxHwEntry columns, and the tmnxHwClass values of the chassis and of control processor cards
	nokiaHardwareSerialNumber    = 5
	nokiaHardwareClass           = 7
	nokiaHardwareFirmwareVersion = 33
	nokiaHardwareClassChassis    = 3
	nokiaHardwareClassCPM        = 9
)

// nokiaDescriptionPattern reads an SR OS sysDescr, such as "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c)
// 2000-2020 Nokia.", into the software version (20.10.R3) and the model (7750 SR). Older releases name the vendor
// ALCATEL or Alcatel-Lucent.
var nokiaDescriptionPattern = regexp.MustCompile(`^TiMOS-[A-Z]+-(\S+)\s+\S+\s+(?i:nokia|alcatel(?:-lucent)?)\s+(.+?)\s+Copyright`)

var nokiaSROS = &Vendor{
	Name:              "Nokia",
	EnterpriseNumbers: []string{"6527"},
	Model:             nokiaModel,
	Hardware:          nokiaHardware,
	Resources:         nokiaResources,
	Optics:            nokiaOptics,
}

func nokiaDescription(description string) (version, model string) {
	match := nokiaDescriptionPattern.FindStringSubmatch(description)

	if match == nil {
		return "", ""
	}

	return match[1], match[2]
}

func nokiaModel(description string) string {
	_, model := nokiaDescription(description)

	return model
}

// nokiaHardware reads the software version from the sysDescr, and the chassis serial number and the control
// processor's firmware version from TIMETRA-CHASSIS-MIB
func nokiaHardware(client *gosnmp.GoSNMP, system *api.System) error {
	if version, _ := nokiaDescription(system.Description); len(system.SoftwareVersion) < 1 {
		system.SoftwareVersion = version
	}

	rows, err := snmp.WalkTable(client, oidNokiaHardwareEntry, nokiaHardwareSerialNumber, nokiaHardwareClass, nokiaHardwareFirmwareVersion)

	if err != nil {
		return err
	}

	applyNokiaHardware(system, rows)

	return nil
}

func applyNokiaHardware(system *api.System, rows map[string]snmp.Row) {
	for _, index := range snmp.SortedIndexes(rows) {
		row := rows[index]

		switch row.Int(nokiaHardwareClass) {
		case nokiaHardwareClassChassis:
			if len(system.SerialNumber) < 1 {
				system.SerialNumber = row.String(nokiaHardwareSerialNumber)
			}
		case nokiaHardwareClassCPM:
			if len(system.FirmwareVersion) < 1 {
				system.FirmwareVersion = row.String(nokiaHardwareFirmwareVersion)
			}
		}
	}
}

// nokiaResources reads the CPU load and memory from TIMETRA-SYSTEM-MIB. Memory allocated to pools counts as used, and
// the total is that plus the memory still available.
func nokiaResources(client *gosnmp.GoSNMP, resources *api.Resources) error {
	row, err := snmp.GetRow(client, oidNokiaCPUUsage, oidNokiaKbMemoryAvailable, oidNokiaKbMemoryPoolAllocated)

	if err != nil {
		return err
	}

	if load, ok := row.Number(0); ok && resources.CPULoadPercent == nil {
		percent := float64(load)
		resources.CPULoadPercent = &percent
	}

	available, availableOK := row.Number(1)
	allocated, allocatedOK := row.Number(2)

	if availableOK && allocatedOK && resources.MemoryTotalBytes == nil {
		total, used := (available+allocated)*1024, allocated*1024
		resources.MemoryTotalBytes, resources.MemoryUsedBytes = &total, &used
	}

	return nil
}

func nokiaOptics(client *gosnmp.GoSNMP, _ []api.Interface) (optics.Readings, error) {
	laneRows, err := snmp.WalkTable(client, oidNokiaDDMLaneEntry, 12, 14, 16, 17, 19, 21)

	if err != nil {
		return nil, err
	}

	rows, err := snmp.WalkTable(client, oidNokiaDDMEntry, 16, 18, 20, 21, 23, 25)

	if err != nil {
		return nil, err
	}

	return nokiaReadings(rows, laneRows), nil
}

// nokiaReadings reads TIMETRA-PORT-MIB, which reports light levels and thresholds in tenths of a microwatt, indexed by
// chassis and port ID (the port's ifIndex on SR OS). Multi-lane optics are read per lane from the lane table, everything
// else from the per-port table.
func nokiaReadings(rows, laneRows map[string]snmp.Row) optics.Readings {
	readings := make(optics.Readings)

	for index, row := range laneRows {
		parts := strings.Split(index, ".")

		if len(parts) != 3 {
			continue
		}

		interfaceIndex, interfaceErr := strconv.Atoi(parts[1])
		lane, laneErr := strconv.Atoi(parts[2])

		if interfaceErr != nil || laneErr != nil {
			continue
		}

		addNokiaReadings(readings, interfaceIndex, lane, row, 17, 19, 21, 12, 14, 16)
	}

	for index, row := range rows {
		parts := strings.Split(index, ".")

		if len(parts) != 2 {
			continue
		}

		interfaceIndex, err := strconv.Atoi(parts[1])

		if err != nil || readings[interfaceIndex] != nil {
			continue
		}

		addNokiaReadings(readings, interfaceIndex, 0, row, 21, 23, 25, 16, 18, 20)
	}

	return readings
}

// addNokiaReadings adds one row's receive and transmit levels, each with its low and high alarm thresholds, from the
// given columns
func addNokiaReadings(readings optics.Readings, interfaceIndex, lane int, row snmp.Row, rx, rxLow, rxHigh, tx, txLow, txHigh int) {
	if value, ok := row.Number(rx); ok {
		readings.Add(interfaceIndex, lane, true, tenthMicrowattsToDBm(value))
		nokiaThresholds(readings, interfaceIndex, true, row, rxLow, rxHigh)
	}

	if value, ok := row.Number(tx); ok {
		readings.Add(interfaceIndex, lane, false, tenthMicrowattsToDBm(value))
		nokiaThresholds(readings, interfaceIndex, false, row, txLow, txHigh)
	}
}

func nokiaThresholds(readings optics.Readings, interfaceIndex int, receive bool, row snmp.Row, lowColumn, highColumn int) {
	low, lowOK := row.Number(lowColumn)
	high, highOK := row.Number(highColumn)

	if lowOK && highOK && low > 0 {
		readings.SetThresholds(interfaceIndex, receive, tenthMicrowattsToDBm(low), tenthMicrowattsToDBm(high))
	}
}

func tenthMicrowattsToDBm(value int64) float64 {
	return optics.MilliwattsToDBm(float64(value) / 10000)
}
