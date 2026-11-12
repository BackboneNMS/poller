package main

import (
	"main/src/api"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// Nokia equipment comes in two families with their own MIBs, since neither implements HOST-RESOURCES-MIB and SR OS
// does not use ENTITY-MIB for hardware or ENTITY-SENSOR-MIB for optics:
//   - SR OS routers and switches (7750 SR, 7250 IXR, 7210 SAS and others), formerly Alcatel-Lucent TiMetra, under
//     enterprise 6527 (TIMETRA-SYSTEM-MIB, TIMETRA-CHASSIS-MIB, TIMETRA-PORT-MIB). SR Linux shares the enterprise number;
//     where it does not answer these tables, the standard MIBs are used as for any other device.
//   - ISAM access nodes and OLTs (7302, 7330, 7360 FX), formerly Alcatel-Lucent, under enterprise 637 (ASAM-SYSTEM-MIB,
//     ASAM-EQUIP-MIB).
const (
	enterpriseNokiaSROS = "6527"
	enterpriseNokiaISAM = "637"

	oidNokiaCPUUsage              = "1.3.6.1.4.1.6527.3.1.2.1.1.1.0"  // sgiCpuUsage, percent
	oidNokiaKbMemoryAvailable     = "1.3.6.1.4.1.6527.3.1.2.1.1.10.0" // sgiKbMemoryAvailable, kilobytes not allocated to pools
	oidNokiaKbMemoryPoolAllocated = "1.3.6.1.4.1.6527.3.1.2.1.1.11.0" // sgiKbMemoryPoolAllocated, kilobytes allocated to pools
	oidNokiaHardwareEntry         = "1.3.6.1.4.1.6527.3.1.2.2.1.8.1"  // tmnxHwEntry, indexed by chassis and hardware index
	oidNokiaDDMEntry              = "1.3.6.1.4.1.6527.3.1.2.2.4.31.1" // tmnxDigitalDiagMonitorEntry, indexed by chassis and port ID
	oidNokiaDDMLaneEntry          = "1.3.6.1.4.1.6527.3.1.2.2.4.66.1" // tmnxDDMLaneEntry, indexed by chassis, port ID and lane
	oidISAMSoftwareVersion        = "1.3.6.1.4.1.637.61.1.9.28.1.0"   // swEtsiVersion
	oidISAMCPULoadEntry           = "1.3.6.1.4.1.637.61.1.9.29.1.1"   // cpuLoadEntry, one row per board slot
	oidISAMMemoryUsageEntry       = "1.3.6.1.4.1.637.61.1.9.29.2.1"   // memUsageEntry, one row per board slot
	oidISAMHolderEntry            = "1.3.6.1.4.1.637.61.1.23.2.1"     // eqptHolderEntry: the system, racks and shelves

	// tmnxHwEntry columns, and the tmnxHwClass values of the chassis and of control processor cards
	nokiaHardwareSerialNumber    = 5
	nokiaHardwareClass           = 7
	nokiaHardwareFirmwareVersion = 33
	nokiaHardwareClassChassis    = 3
	nokiaHardwareClassCPM        = 9

	// eqptHolderEntry columns, and the holder that monitoring tools read the shelf from
	isamHolderActualType   = 4
	isamHolderSerialNumber = 13
	isamMainHolder         = "17"
)

// nokiaDescriptionPattern reads an SR OS sysDescr, such as "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c)
// 2000-2020 Nokia.", into the software version (20.10.R3) and the model (7750 SR). Older releases name the vendor
// ALCATEL or Alcatel-Lucent.
var nokiaDescriptionPattern = regexp.MustCompile(`^TiMOS-[A-Z]+-(\S+)\s+\S+\s+(?i:nokia|alcatel(?:-lucent)?)\s+(.+?)\s+Copyright`)

func isNokia(enterpriseNumber string) bool {
	return enterpriseNumber == enterpriseNokiaSROS || enterpriseNumber == enterpriseNokiaISAM
}

func nokiaDescription(description string) (version, model string) {
	match := nokiaDescriptionPattern.FindStringSubmatch(description)

	if match == nil {
		return "", ""
	}

	return match[1], match[2]
}

// getRow reads scalar values into a row keyed by their position in oids, so they can be read with rowNumber and
// rowString. Objects the agent does not have are left out.
func getRow(client *gosnmp.GoSNMP, oids ...string) (snmpRow, error) {
	packet, err := client.Get(oids)

	if err != nil {
		return nil, err
	}

	row := make(snmpRow, len(oids))

	if packet.Error != gosnmp.NoError {
		return row, nil
	}

	for _, pdu := range packet.Variables {
		if position := slices.Index(oids, strings.TrimPrefix(pdu.Name, ".")); position >= 0 {
			row[position] = pdu
		}
	}

	return row, nil
}

// collectNokiaHardware fills in the hardware details ENTITY-MIB did not provide: the software version and model from
// the sysDescr and the chassis serial number and firmware version from TIMETRA-CHASSIS-MIB on SR OS, or the shelf type,
// serial number and software version on ISAM
func collectNokiaHardware(client *gosnmp.GoSNMP, enterpriseNumber string, system *api.System) error {
	switch enterpriseNumber {
	case enterpriseNokiaSROS:
		if version, _ := nokiaDescription(system.Description); len(system.SoftwareVersion) < 1 {
			system.SoftwareVersion = version
		}

		rows, err := walkTable(client, oidNokiaHardwareEntry, nokiaHardwareSerialNumber, nokiaHardwareClass, nokiaHardwareFirmwareVersion)

		if err != nil {
			return err
		}

		applyNokiaHardware(system, rows)
	case enterpriseNokiaISAM:
		rows, err := walkTable(client, oidISAMHolderEntry, isamHolderActualType, isamHolderSerialNumber)

		if err != nil {
			return err
		}

		applyISAMHolder(system, rows)

		version, err := getRow(client, oidISAMSoftwareVersion)

		if err != nil {
			return err
		}

		if len(system.SoftwareVersion) < 1 {
			system.SoftwareVersion = rowString(version, 0)
		}
	}

	return nil
}

func applyNokiaHardware(system *api.System, rows map[string]snmpRow) {
	for _, index := range sortedRowIndexes(rows) {
		row := rows[index]

		switch rowInt(row, nokiaHardwareClass) {
		case nokiaHardwareClassChassis:
			if len(system.SerialNumber) < 1 {
				system.SerialNumber = rowString(row, nokiaHardwareSerialNumber)
			}
		case nokiaHardwareClassCPM:
			if len(system.FirmwareVersion) < 1 {
				system.FirmwareVersion = rowString(row, nokiaHardwareFirmwareVersion)
			}
		}
	}
}

// applyISAMHolder reads the shelf type and serial number from the main holder, or the first holder that has a serial
// number when there is no main holder
func applyISAMHolder(system *api.System, rows map[string]snmpRow) {
	row, ok := rows[isamMainHolder]

	if !ok || len(rowString(row, isamHolderSerialNumber)) < 1 {
		for _, index := range sortedRowIndexes(rows) {
			if len(rowString(rows[index], isamHolderSerialNumber)) > 0 {
				row, ok = rows[index], true

				break
			}
		}
	}

	if !ok {
		return
	}

	if holderType := rowString(row, isamHolderActualType); len(system.Model) < 1 && holderType != "EMPTY" && holderType != "UNKNOWN" {
		system.Model = holderType
	}

	if len(system.SerialNumber) < 1 {
		system.SerialNumber = rowString(row, isamHolderSerialNumber)
	}
}

// collectNokiaResources fills in the CPU load and memory HOST-RESOURCES-MIB did not provide. On ISAM, the CPU load is
// the average of every board and the memory is the total of every board.
func collectNokiaResources(client *gosnmp.GoSNMP, enterpriseNumber string, resources *api.Resources) error {
	switch enterpriseNumber {
	case enterpriseNokiaSROS:
		row, err := getRow(client, oidNokiaCPUUsage, oidNokiaKbMemoryAvailable, oidNokiaKbMemoryPoolAllocated)

		if err != nil {
			return err
		}

		if load, ok := rowNumber(row, 0); ok && resources.CPULoadPercent == nil {
			resources.CPULoadPercent = PointerOf(float64(load))
		}

		available, availableOK := rowNumber(row, 1)
		allocated, allocatedOK := rowNumber(row, 2)

		if availableOK && allocatedOK && resources.MemoryTotalBytes == nil {
			resources.MemoryTotalBytes = PointerOf((available + allocated) * 1024)
			resources.MemoryUsedBytes = PointerOf(allocated * 1024)
		}
	case enterpriseNokiaISAM:
		loads, err := walkTable(client, oidISAMCPULoadEntry, 4)

		if err != nil {
			return err
		}

		memory, err := walkTable(client, oidISAMMemoryUsageEntry, 1, 2)

		if err != nil {
			return err
		}

		applyISAMResources(resources, loads, memory)
	}

	return nil
}

func applyISAMResources(resources *api.Resources, loads, memory map[string]snmpRow) {
	var loadCount, loadTotal, totalMegabytes, usedMegabytes int64

	for _, row := range loads {
		if load, ok := rowNumber(row, 4); ok {
			loadCount++
			loadTotal += load
		}
	}

	for _, row := range memory {
		total, totalOK := rowNumber(row, 1)
		used, usedOK := rowNumber(row, 2)

		if totalOK && usedOK && total > 0 {
			totalMegabytes += total
			usedMegabytes += used
		}
	}

	if loadCount > 0 && resources.CPULoadPercent == nil {
		resources.CPULoadPercent = PointerOf(float64(loadTotal) / float64(loadCount))
	}

	if totalMegabytes > 0 && resources.MemoryTotalBytes == nil {
		resources.MemoryTotalBytes = PointerOf(totalMegabytes * 1024 * 1024)
		resources.MemoryUsedBytes = PointerOf(usedMegabytes * 1024 * 1024)
	}
}

// nokiaReadings reads TIMETRA-PORT-MIB, which reports light levels and thresholds in tenths of a microwatt, indexed by
// chassis and port ID (the port's ifIndex on SR OS). Multi-lane optics are read per lane from the lane table, everything
// else from the per-port table.
func nokiaReadings(rows, laneRows map[string]snmpRow, readings opticalReadings) {
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
}

// addNokiaReadings adds one row's receive and transmit levels, each with its low and high alarm thresholds, from the
// given columns
func addNokiaReadings(readings opticalReadings, interfaceIndex, lane int, row snmpRow, rx, rxLow, rxHigh, tx, txLow, txHigh int) {
	if value, ok := rowNumber(row, rx); ok {
		readings.add(interfaceIndex, lane, true, tenthMicrowattsToDBm(value))
		nokiaThresholds(readings, interfaceIndex, true, row, rxLow, rxHigh)
	}

	if value, ok := rowNumber(row, tx); ok {
		readings.add(interfaceIndex, lane, false, tenthMicrowattsToDBm(value))
		nokiaThresholds(readings, interfaceIndex, false, row, txLow, txHigh)
	}
}

func nokiaThresholds(readings opticalReadings, interfaceIndex int, receive bool, row snmpRow, lowColumn, highColumn int) {
	low, lowOK := rowNumber(row, lowColumn)
	high, highOK := rowNumber(row, highColumn)

	if lowOK && highOK && low > 0 {
		readings.setThresholds(interfaceIndex, receive, tenthMicrowattsToDBm(low), tenthMicrowattsToDBm(high))
	}
}

// tenthMicrowattsToDBm converts optical power in tenths of a microwatt to dBm. No light at all reads as the floor
// noLightDBm.
func tenthMicrowattsToDBm(value int64) float64 {
	if value <= 0 {
		return noLightDBm
	}

	return 10 * math.Log10(float64(value)/10000)
}

func sortedRowIndexes(rows map[string]snmpRow) []string {
	indexes := make([]string, 0, len(rows))

	for index := range rows {
		indexes = append(indexes, index)
	}

	slices.SortFunc(indexes, compareOIDs)

	return indexes
}
