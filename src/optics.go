package main

import (
	"main/src/dashboard"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidEntityAliasMappingEntry = "1.3.6.1.2.1.47.1.3.2.1"
	oidEntitySensorEntry       = "1.3.6.1.2.1.99.1.1.1"
	oidCiscoEntitySensorEntry  = "1.3.6.1.4.1.9.9.91.1.1.1.1"
	oidCiscoSensorThreshold    = "1.3.6.1.4.1.9.9.91.1.2.1.1"
	oidJuniperDOMEntry         = "1.3.6.1.4.1.2636.3.60.1.1.1.1"
	oidJuniperDOMLaneEntry     = "1.3.6.1.4.1.2636.3.60.1.2.1.1"
	oidMikroTikOpticalEntry    = "1.3.6.1.4.1.14988.1.1.19.1.1"
	oidInterfaceIndexColumn    = "1.3.6.1.2.1.2.2.1.1."

	enterpriseCisco    = "9"
	enterpriseJuniper  = "2636"
	enterpriseMikroTik = "14988"

	sensorTypeWatts = 6
	sensorTypeDBm   = 14
	sensorStatusOK  = 1
	sensorScaleUnit = 9

	thresholdLessThan       = 1
	thresholdLessOrEqual    = 2
	thresholdGreaterThan    = 3
	thresholdGreaterOrEqual = 4

	// noLightDBm is the floor for reported light levels; transceivers report anything from -40 dBm to -inf without light
	noLightDBm     = -40.0
	maxEntityDepth = 16
)

var (
	sensorReceivePattern  = regexp.MustCompile(`(?i)\b(rx|receive|received|input)\b`)
	sensorTransmitPattern = regexp.MustCompile(`(?i)\b(tx|transmit|output)\b`)
	sensorLanePattern     = regexp.MustCompile(`(?i)\blane\s*(\d+)`)
)

// opticalReadings holds light levels by interface index, then by lane (0 when the optic has a single lane)
type opticalReadings map[int]*opticalLanes

type opticalLanes struct {
	receive            map[int]float64
	transmit           map[int]float64
	receiveThresholds  *dashboard.OpticalThresholds
	transmitThresholds *dashboard.OpticalThresholds
}

func (r opticalReadings) add(interfaceIndex, lane int, receive bool, dBm float64) {
	if r[interfaceIndex] == nil {
		r[interfaceIndex] = &opticalLanes{receive: make(map[int]float64), transmit: make(map[int]float64)}
	}

	value := roundDBm(math.Max(dBm, noLightDBm))

	if receive {
		r[interfaceIndex].receive[lane] = value
	} else {
		r[interfaceIndex].transmit[lane] = value
	}
}

// setThresholds records the alarm thresholds of an interface that has light levels. Empty ranges are ignored, since
// that is how most optics without thresholds report them (as zeros).
func (r opticalReadings) setThresholds(interfaceIndex int, receive bool, lowDBm, highDBm float64) {
	lanes := r[interfaceIndex]

	if lanes == nil || lowDBm >= highDBm {
		return
	}

	thresholds := &dashboard.OpticalThresholds{LowDBm: roundDBm(lowDBm), HighDBm: roundDBm(highDBm)}

	if receive {
		lanes.receiveThresholds = thresholds
	} else {
		lanes.transmitThresholds = thresholds
	}
}

func roundDBm(value float64) float64 {
	return math.Round(value*100) / 100
}

// collectOptics adds transceiver light levels to the interfaces they belong to. Vendors expose them differently:
// Juniper and MikroTik in their own tables indexed by interface, everyone else as sensors in the ENTITY-SENSOR-MIB
// (or Cisco's variant of it) that have to be traced through the ENTITY-MIB hierarchy back to an interface.
func collectOptics(client *gosnmp.GoSNMP, enterpriseNumber string, physicalRows map[string]snmpRow, interfaces []dashboard.Interface) error {
	readings := make(opticalReadings)

	switch enterpriseNumber {
	case enterpriseJuniper:
		laneRows, err := walkTable(client, oidJuniperDOMLaneEntry)

		if err != nil {
			return err
		}

		rows, err := walkTable(client, oidJuniperDOMEntry)

		if err != nil {
			return err
		}

		juniperReadings(rows, laneRows, readings)
	case enterpriseMikroTik:
		rows, err := walkTable(client, oidMikroTikOpticalEntry)

		if err != nil {
			return err
		}

		mikroTikReadings(rows, interfaces, readings)
	}

	if len(readings) > 0 {
		applyOptics(interfaces, readings)

		return nil
	}

	sensorTables := []string{oidEntitySensorEntry}

	if enterpriseNumber == enterpriseCisco {
		sensorTables = []string{oidCiscoEntitySensorEntry, oidEntitySensorEntry}
	}

	var aliasRows map[string]snmpRow

	for _, oid := range sensorTables {
		cisco := oid == oidCiscoEntitySensorEntry
		sensorRows, err := walkTable(client, oid)

		if err != nil {
			return err
		}

		if !hasOpticalSensors(sensorRows, cisco) {
			continue
		}

		if aliasRows == nil {
			if aliasRows, err = walkTable(client, oidEntityAliasMappingEntry); err != nil {
				return err
			}
		}

		var thresholdRows map[string]snmpRow

		if cisco {
			if thresholdRows, err = walkTable(client, oidCiscoSensorThreshold); err != nil {
				return err
			}
		}

		entitySensorReadings(sensorRows, cisco, physicalRows, aliasRows, thresholdRows, interfaces, readings)

		if len(readings) > 0 {
			break
		}
	}

	applyOptics(interfaces, readings)

	return nil
}

// juniperReadings reads JUNIPER-DOM-MIB, which reports levels in hundredths of a dBm. Multi-lane optics (QSFP) are
// read per lane from the lane table, everything else from the per-interface table. Alarm thresholds only exist in the
// per-interface table and apply to every lane.
func juniperReadings(rows, laneRows map[string]snmpRow, readings opticalReadings) {
	for index, row := range laneRows {
		rawInterface, rawLane, ok := strings.Cut(index, ".")
		interfaceIndex, interfaceErr := strconv.Atoi(rawInterface)
		lane, laneErr := strconv.Atoi(rawLane)

		if !ok || interfaceErr != nil || laneErr != nil {
			continue
		}

		if value, ok := rowNumber(row, 6); ok {
			readings.add(interfaceIndex, lane, true, float64(value)/100)
		}

		if value, ok := rowNumber(row, 8); ok {
			readings.add(interfaceIndex, lane, false, float64(value)/100)
		}
	}

	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		if readings[interfaceIndex] == nil {
			if value, ok := rowNumber(row, 5); ok {
				readings.add(interfaceIndex, 0, true, float64(value)/100)
			}

			if value, ok := rowNumber(row, 7); ok {
				readings.add(interfaceIndex, 0, false, float64(value)/100)
			}
		}

		juniperThresholds(readings, interfaceIndex, true, row, 10, 9)
		juniperThresholds(readings, interfaceIndex, false, row, 18, 17)
	}
}

// juniperThresholds reads the low and high alarm thresholds of one light level from their columns in jnxDomCurrentTable
// (receive power in 10 and 9, transmit power in 18 and 17)
func juniperThresholds(readings opticalReadings, interfaceIndex int, receive bool, row snmpRow, lowColumn, highColumn int) {
	low, lowOK := rowNumber(row, lowColumn)
	high, highOK := rowNumber(row, highColumn)

	if lowOK && highOK {
		readings.setThresholds(interfaceIndex, receive, float64(low)/100, float64(high)/100)
	}
}

// mikroTikReadings reads MIKROTIK-MIB mtxrOpticalTable, which reports levels in thousandths of a dBm. Rows are
// matched to interfaces by name, falling back to the row index as the interface index.
func mikroTikReadings(rows map[string]snmpRow, interfaces []dashboard.Interface, readings opticalReadings) {
	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		if item := findInterfaceByName(interfaces, rowString(row, 2)); item != nil {
			interfaceIndex = item.Index
		}

		if value, ok := rowNumber(row, 10); ok {
			readings.add(interfaceIndex, 0, true, float64(value)/1000)
		}

		if value, ok := rowNumber(row, 9); ok {
			readings.add(interfaceIndex, 0, false, float64(value)/1000)
		}
	}
}

func hasOpticalSensors(sensorRows map[string]snmpRow, cisco bool) bool {
	for _, row := range sensorRows {
		if _, ok := sensorDBm(row, cisco); ok {
			return true
		}
	}

	return false
}

// sensorDBm returns the value of an optical power sensor in dBm. Cisco reports optical power as dBm directly, while
// the standard ENTITY-SENSOR-MIB has no dBm type so optics report watts, or a dBm value in "other" units.
func sensorDBm(row snmpRow, cisco bool) (float64, bool) {
	if rowInt(row, 5) != sensorStatusOK {
		return 0, false
	}

	value, ok := rowNumber(row, 4)

	if !ok {
		return 0, false
	}

	return sensorValueDBm(row, value, cisco)
}

// sensorValueDBm converts a raw value of an optical power sensor, either its reading or one of its thresholds, to dBm
// using the sensor's type, scale and precision
func sensorValueDBm(row snmpRow, value int64, cisco bool) (float64, bool) {
	magnitude := float64(value) * math.Pow(10, float64(3*(rowInt(row, 2)-sensorScaleUnit))) / math.Pow(10, float64(rowInt(row, 3)))

	switch {
	case cisco && rowInt(row, 1) == sensorTypeDBm:
		return magnitude, true
	case rowInt(row, 1) == sensorTypeWatts:
		if magnitude <= 0 {
			return noLightDBm, true
		}

		return 10 * math.Log10(magnitude*1000), true
	case !cisco && strings.Contains(strings.ToLower(rowString(row, 6)), "dbm"):
		return magnitude, true
	default:
		return 0, false
	}
}

// entitySensorReadings matches optical power sensors to interfaces. A sensor is optical when its type is power and
// its name says whether it measures received or transmitted light. It belongs to the interface that the sensor (or
// the entity Cisco says it measures, or the closest entity containing it) is mapped to in entAliasMappingTable, or
// whose name matches the entity's name. Alarm thresholds are only available from Cisco's threshold table.
func entitySensorReadings(sensorRows map[string]snmpRow, cisco bool, physicalRows, aliasRows, thresholdRows map[string]snmpRow, interfaces []dashboard.Interface, readings opticalReadings) {
	entityInterfaces := aliasInterfaceIndexes(aliasRows)
	sensorThresholds := groupSensorThresholds(thresholdRows)

	for index, row := range sensorRows {
		dBm, ok := sensorDBm(row, cisco)

		if !ok {
			continue
		}

		entity := physicalRows[index]
		label := rowString(entity, 7) + " " + rowString(entity, 2)
		receive := sensorReceivePattern.MatchString(label)

		if !receive && !sensorTransmitPattern.MatchString(label) {
			continue
		}

		start := index

		if measured := rowInt(row, 8); cisco && measured > 0 {
			start = strconv.FormatInt(measured, 10)
		}

		interfaceIndex, ok := resolveEntityInterface(start, physicalRows, entityInterfaces, interfaces)

		if !ok {
			continue
		}

		lane := 0

		if match := sensorLanePattern.FindStringSubmatch(label); match != nil {
			lane, _ = strconv.Atoi(match[1])
		}

		readings.add(interfaceIndex, lane, receive, dBm)

		if low, high, ok := sensorThresholdsDBm(row, cisco, sensorThresholds[index]); ok {
			readings.setThresholds(interfaceIndex, receive, low, high)
		}
	}
}

// groupSensorThresholds groups the rows of CISCO-ENTITY-SENSOR-MIB entSensorThresholdTable, indexed by sensor and then
// threshold, by the sensor they belong to
func groupSensorThresholds(thresholdRows map[string]snmpRow) map[string][]snmpRow {
	result := make(map[string][]snmpRow)

	for index, row := range thresholdRows {
		sensorIndex, _, _ := strings.Cut(index, ".")
		result[sensorIndex] = append(result[sensorIndex], row)
	}

	return result
}

// sensorThresholdsDBm returns the alarm thresholds of a sensor: the lowest value it alarms below and the highest value it
// alarms above. Warning thresholds sit inside those, so the outermost pair are the alarm thresholds.
func sensorThresholdsDBm(row snmpRow, cisco bool, thresholdRows []snmpRow) (float64, float64, bool) {
	low, high := math.Inf(1), math.Inf(-1)

	for _, threshold := range thresholdRows {
		raw, ok := rowNumber(threshold, 4)

		if !ok {
			continue
		}

		value, ok := sensorValueDBm(row, raw, cisco)

		if !ok {
			continue
		}

		switch rowInt(threshold, 3) {
		case thresholdLessThan, thresholdLessOrEqual:
			low = math.Min(low, value)
		case thresholdGreaterThan, thresholdGreaterOrEqual:
			high = math.Max(high, value)
		}
	}

	return low, high, !math.IsInf(low, 0) && !math.IsInf(high, 0)
}

// aliasInterfaceIndexes maps physical entities to interface indexes using entAliasMappingIdentifier, which points at
// the ifIndex column of the interface table
func aliasInterfaceIndexes(aliasRows map[string]snmpRow) map[string]int {
	result := make(map[string]int)

	for index, row := range aliasRows {
		entityIndex, _, _ := strings.Cut(index, ".")
		rawInterface, ok := strings.CutPrefix(rowString(row, 2), oidInterfaceIndexColumn)

		if !ok {
			continue
		}

		if interfaceIndex, err := strconv.Atoi(rawInterface); err == nil {
			result[entityIndex] = interfaceIndex
		}
	}

	return result
}

func resolveEntityInterface(entityIndex string, physicalRows map[string]snmpRow, entityInterfaces map[string]int, interfaces []dashboard.Interface) (int, bool) {
	for depth := 0; depth < maxEntityDepth && entityIndex != "0" && len(entityIndex) > 0; depth++ {
		if interfaceIndex, ok := entityInterfaces[entityIndex]; ok {
			return interfaceIndex, true
		}

		if item := findInterfaceByName(interfaces, rowString(physicalRows[entityIndex], 7)); item != nil {
			return item.Index, true
		}

		entityIndex = strconv.FormatInt(rowInt(physicalRows[entityIndex], 4), 10)
	}

	return 0, false
}

func findInterfaceByName(interfaces []dashboard.Interface, name string) *dashboard.Interface {
	if len(name) < 1 {
		return nil
	}

	for i := range interfaces {
		if strings.EqualFold(interfaces[i].Name, name) || strings.EqualFold(interfaces[i].Description, name) {
			return &interfaces[i]
		}
	}

	return nil
}

func applyOptics(interfaces []dashboard.Interface, readings opticalReadings) {
	for i := range interfaces {
		lanes := readings[interfaces[i].Index]

		if lanes == nil {
			continue
		}

		interfaces[i].Optical = &dashboard.Optical{
			RxPowerDBm:   laneValues(lanes.receive),
			TxPowerDBm:   laneValues(lanes.transmit),
			RxThresholds: lanes.receiveThresholds,
			TxThresholds: lanes.transmitThresholds,
		}
	}
}

func laneValues(values map[int]float64) []float64 {
	lanes := make([]int, 0, len(values))

	for lane := range values {
		lanes = append(lanes, lane)
	}

	slices.Sort(lanes)

	result := make([]float64, len(lanes))

	for i, lane := range lanes {
		result[i] = values[lane]
	}

	return result
}
