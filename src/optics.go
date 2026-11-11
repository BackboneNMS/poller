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
	receive  map[int]float64
	transmit map[int]float64
}

func (r opticalReadings) add(interfaceIndex, lane int, receive bool, dBm float64) {
	if r[interfaceIndex] == nil {
		r[interfaceIndex] = &opticalLanes{receive: make(map[int]float64), transmit: make(map[int]float64)}
	}

	value := math.Round(math.Max(dBm, noLightDBm)*100) / 100

	if receive {
		r[interfaceIndex].receive[lane] = value
	} else {
		r[interfaceIndex].transmit[lane] = value
	}
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

		entitySensorReadings(sensorRows, cisco, physicalRows, aliasRows, interfaces, readings)

		if len(readings) > 0 {
			break
		}
	}

	applyOptics(interfaces, readings)

	return nil
}

// juniperReadings reads JUNIPER-DOM-MIB, which reports levels in hundredths of a dBm. Multi-lane optics (QSFP) are
// read per lane from the lane table, everything else from the per-interface table.
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

		if err != nil || readings[interfaceIndex] != nil {
			continue
		}

		if value, ok := rowNumber(row, 5); ok {
			readings.add(interfaceIndex, 0, true, float64(value)/100)
		}

		if value, ok := rowNumber(row, 7); ok {
			readings.add(interfaceIndex, 0, false, float64(value)/100)
		}
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
// whose name matches the entity's name.
func entitySensorReadings(sensorRows map[string]snmpRow, cisco bool, physicalRows, aliasRows map[string]snmpRow, interfaces []dashboard.Interface, readings opticalReadings) {
	entityInterfaces := aliasInterfaceIndexes(aliasRows)

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
	}
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
			RxPowerDBm: laneValues(lanes.receive),
			TxPowerDBm: laneValues(lanes.transmit),
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
