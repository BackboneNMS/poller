package vendors

import (
	"main/src/api"
	"main/src/cli"
	"main/src/optics"
	"main/src/snmp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// TP-Link JetStream and Omada switch OIDs, checked against the TPLINK-* MIB files in LibreNMS's mibs/tplink
const (
	oidTPLinkCPUEntry               = "1.3.6.1.4.1.11863.6.4.1.1.1.1"  // TPLINK-SYSMONITOR-MIB tpSysMonitorCpuEntry, by unit
	oidTPLinkMemoryEntry            = "1.3.6.1.4.1.11863.6.4.1.2.1.1"  // TPLINK-SYSMONITOR-MIB tpSysMonitorMemoryEntry, by unit
	oidTPLinkHardwareVersion        = "1.3.6.1.4.1.11863.6.1.1.5.0"    // TPLINK-SYSINFO-MIB tpSysInfoHwVersion
	oidTPLinkSoftwareVersion        = "1.3.6.1.4.1.11863.6.1.1.6.0"    // TPLINK-SYSINFO-MIB tpSysInfoSwVersion
	oidTPLinkSerialNumber           = "1.3.6.1.4.1.11863.6.1.1.8.0"    // TPLINK-SYSINFO-MIB tpSysInfoSerialNum
	oidTPLinkDDMStatusEntry         = "1.3.6.1.4.1.11863.6.96.1.7.1.1" // TPLINK-DDMSTATUS-MIB ddmStatusEntry, by ifIndex
	oidTPLinkTransmitThresholdEntry = "1.3.6.1.4.1.11863.6.96.1.5.1.1" // TPLINK-DDMTXPOWTHRESHOLD-MIB, by ifIndex
	oidTPLinkReceiveThresholdEntry  = "1.3.6.1.4.1.11863.6.96.1.6.1.1" // TPLINK-DDMRXPOWTHRESHOLD-MIB, by ifIndex
	tpLinkCPUOneMinuteColumn        = 3                                // tpSysMonitorCpu1Minute, percent
	tpLinkMemoryUtilizationColumn   = 2                                // tpSysMonitorMemoryUtilization, percent
	tpLinkThresholdHighAlarmColumn  = 2
	tpLinkThresholdLowAlarmColumn   = 3
	tpLinkDDMTemperatureColumn      = 2
	tpLinkDDMVoltageColumn          = 3
	tpLinkDDMBiasCurrentColumn      = 4
	tpLinkDDMTransmitPowerColumn    = 5
	tpLinkDDMReceivePowerColumn     = 6
	tpLinkRequestInterval           = 20 * time.Millisecond
	tpLinkMaxRepetitions            = 10
	tpLinkMaxOIDs                   = 1
)

// tpLink covers TP-Link's JetStream and Omada switches. Their SNMP agents are fragile: management freezes, or the switch
// reboots, when one request asks for many objects, and walking the interface tables keeps the management CPU busy (a
// bug TP-Link has acknowledged). Requests therefore name one object at a time, ask for at most 10 rows, and are spaced
// apart, the way net-snmp's snmpbulkwalk (and so LibreNMS) reads them by default.
var tpLink = &Vendor{
	Name:              "TP-Link",
	EnterpriseNumbers: []string{"11863"},
	SNMPLimits:        &snmp.Limits{MaxOIDs: tpLinkMaxOIDs, MaxRepetitions: tpLinkMaxRepetitions, Interval: tpLinkRequestInterval},
	Hardware:          tpLinkHardware,
	Resources:         tpLinkResources,
	Optics:            tpLinkOptics,
	ConfigPlatforms:   []*ConfigPlatform{tpLinkJetStream},
}

// tpLinkJetStream is the Cisco-like command line of JetStream and Omada switches, which pages long output with "Press
// any key to continue (Q to quit)". TP-Link's routers and access points report the same enterprise number but have
// no such command line, so backups for them should be turned off.
var tpLinkJetStream = &ConfigPlatform{
	ID:   "TPLINK_JETSTREAM",
	Name: "TP-Link JetStream / Omada switch",
	Profile: cli.Profile{
		Prompt:   ciscoStylePrompt,
		Enable:   "enable",
		Commands: fixedCommands("show running-config"),
	},
}

// tpLinkNumberPattern matches the start of the text values TP-Link's DDM tables report, such as "-2.34", "0.5012" or
// "35.5 C". Empty cages report text without a number ("--", "N/A").
var tpLinkNumberPattern = regexp.MustCompile(`^\s*(-?\d+(?:\.\d+)?)\s*([A-Za-z]*)`)

// tpLinkHardware reads the serial number and versions from TPLINK-SYSINFO-MIB, and the model from the hardware version,
// which TP-Link reports as the model followed by the hardware revision ("T2600G-28TS 3.0")
func tpLinkHardware(client *gosnmp.GoSNMP, system *api.System) error {
	row, err := snmp.GetRow(client, oidTPLinkHardwareVersion, oidTPLinkSoftwareVersion, oidTPLinkSerialNumber)

	if err != nil {
		return err
	}

	applyTPLinkHardware(system, row.String(0), row.String(1), row.String(2))

	return nil
}

// applyTPLinkHardware fills in what ENTITY-MIB left out
func applyTPLinkHardware(system *api.System, hardwareVersion, softwareVersion, serialNumber string) {
	model, revision := tpLinkModelAndRevision(hardwareVersion)

	if len(system.Model) < 1 {
		system.Model = model
	}

	if len(system.HardwareVersion) < 1 {
		system.HardwareVersion = revision
	}

	if len(system.SoftwareVersion) < 1 {
		system.SoftwareVersion = strings.TrimSpace(softwareVersion)
	}

	if len(system.SerialNumber) < 1 {
		system.SerialNumber = strings.TrimSpace(serialNumber)
	}
}

// tpLinkModelAndRevision splits a hardware version such as "SX3032F 1.0" into the model and revision. A version that
// does not end in a revision number is all revision.
func tpLinkModelAndRevision(hardwareVersion string) (string, string) {
	fields := strings.Fields(hardwareVersion)

	if len(fields) < 2 {
		return "", strings.TrimSpace(hardwareVersion)
	}

	revision := fields[len(fields)-1]

	if _, err := strconv.ParseFloat(revision, 64); err != nil {
		return "", strings.TrimSpace(hardwareVersion)
	}

	return strings.Join(fields[:len(fields)-1], " "), revision
}

// tpLinkResources reads the CPU load (1-minute average, like hrProcessorLoad) and memory utilization of every unit of
// a stack from TPLINK-SYSMONITOR-MIB, averaging the units. Memory is only reported as a percentage.
func tpLinkResources(client *gosnmp.GoSNMP, resources *api.Resources) error {
	if resources.CPULoadPercent == nil {
		rows, err := snmp.WalkTable(client, oidTPLinkCPUEntry, tpLinkCPUOneMinuteColumn)

		if err != nil {
			return err
		}

		resources.CPULoadPercent = averageColumn(rows, tpLinkCPUOneMinuteColumn)
	}

	if resources.MemoryTotalBytes == nil && resources.MemoryUsedPercent == nil {
		rows, err := snmp.WalkTable(client, oidTPLinkMemoryEntry, tpLinkMemoryUtilizationColumn)

		if err != nil {
			return err
		}

		resources.MemoryUsedPercent = averageColumn(rows, tpLinkMemoryUtilizationColumn)
	}

	return nil
}

// averageColumn averages a numeric column over rows, or returns nil when no row has a value
func averageColumn(rows map[string]snmp.Row, column int) *float64 {
	var total, count int64

	for _, row := range rows {
		if value, ok := row.Number(column); ok {
			total += value
			count++
		}
	}

	if count < 1 {
		return nil
	}

	average := float64(total) / float64(count)

	return &average
}

// tpLinkOptics reads TPLINK-DDMSTATUS-MIB ddmStatusTable, indexed by ifIndex, and the receive and transmit alarm
// thresholds. LibreNMS reads these tables without GetBulk, which TP-Link's agents handle badly for them.
func tpLinkOptics(client *gosnmp.GoSNMP, _ []api.Interface) (optics.Readings, error) {
	rows, err := snmp.WalkTableWithoutBulk(
		client,
		oidTPLinkDDMStatusEntry,
		tpLinkDDMTemperatureColumn,
		tpLinkDDMVoltageColumn,
		tpLinkDDMBiasCurrentColumn,
		tpLinkDDMTransmitPowerColumn,
		tpLinkDDMReceivePowerColumn,
	)

	if err != nil || len(rows) < 1 {
		return make(optics.Readings), err
	}

	readings := tpLinkReadings(rows)

	if readings.Count() < 1 {
		return readings, nil
	}

	receiveThresholds, err := snmp.WalkTableWithoutBulk(client, oidTPLinkReceiveThresholdEntry, tpLinkThresholdHighAlarmColumn, tpLinkThresholdLowAlarmColumn)

	if err != nil {
		return readings, err
	}

	transmitThresholds, err := snmp.WalkTableWithoutBulk(client, oidTPLinkTransmitThresholdEntry, tpLinkThresholdHighAlarmColumn, tpLinkThresholdLowAlarmColumn)

	if err != nil {
		return readings, err
	}

	applyTPLinkThresholds(readings, receiveThresholds, true)
	applyTPLinkThresholds(readings, transmitThresholds, false)

	return readings, nil
}

// tpLinkReadings reads ddmStatusTable rows, whose values are text: temperature in degrees Celsius, supply voltage in
// volts, bias current in milliamps and optical power in milliwatts (as LibreNMS reads them), or in dBm when the text
// says so. Ports without a transceiver have no power readings and are left out.
func tpLinkReadings(rows map[string]snmp.Row) optics.Readings {
	readings := make(optics.Readings)

	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		receive, receiveOK := tpLinkPowerDBm(row.String(tpLinkDDMReceivePowerColumn))
		transmit, transmitOK := tpLinkPowerDBm(row.String(tpLinkDDMTransmitPowerColumn))

		if !receiveOK && !transmitOK {
			continue
		}

		if receiveOK {
			readings.Add(interfaceIndex, 0, true, receive)
		}

		if transmitOK {
			readings.Add(interfaceIndex, 0, false, transmit)
		}

		if value, _, ok := tpLinkNumber(row.String(tpLinkDDMTemperatureColumn)); ok {
			readings.SetTemperature(interfaceIndex, value)
		}

		if value, _, ok := tpLinkNumber(row.String(tpLinkDDMVoltageColumn)); ok {
			readings.SetVoltage(interfaceIndex, value)
		}

		if value, _, ok := tpLinkNumber(row.String(tpLinkDDMBiasCurrentColumn)); ok {
			readings.AddBiasCurrent(interfaceIndex, 0, value)
		}
	}

	return readings
}

// applyTPLinkThresholds sets the alarm thresholds of the interfaces that have light levels from a threshold table,
// indexed by ifIndex, in the same units as the readings
func applyTPLinkThresholds(readings optics.Readings, rows map[string]snmp.Row, receive bool) {
	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil || !readings.Has(interfaceIndex) {
			continue
		}

		high, highOK := tpLinkPowerDBm(row.String(tpLinkThresholdHighAlarmColumn))
		low, lowOK := tpLinkPowerDBm(row.String(tpLinkThresholdLowAlarmColumn))

		if highOK && lowOK && low < high {
			readings.SetThresholds(interfaceIndex, receive, low, high)
		}
	}
}

// tpLinkPowerDBm reads an optical power value in dBm, converting it from milliwatts unless the text says dBm
func tpLinkPowerDBm(text string) (float64, bool) {
	value, unit, ok := tpLinkNumber(text)

	switch {
	case !ok:
		return 0, false
	case strings.EqualFold(unit, "dBm"):
		return value, true
	default:
		return optics.MilliwattsToDBm(value), true
	}
}

// tpLinkNumber reads the number at the start of a DDM text value and the unit after it, if any
func tpLinkNumber(text string) (float64, string, bool) {
	match := tpLinkNumberPattern.FindStringSubmatch(text)

	if match == nil {
		return 0, "", false
	}

	value, err := strconv.ParseFloat(match[1], 64)

	return value, match[2], err == nil
}
