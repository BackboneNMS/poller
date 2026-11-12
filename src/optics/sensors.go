package optics

import (
	"main/src/api"
	"main/src/snmp"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidEntityAliasMappingEntry = "1.3.6.1.2.1.47.1.3.2.1"
	oidInterfaceIndexColumn    = "1.3.6.1.2.1.2.2.1.1."

	// Sensor table columns shared by ENTITY-SENSOR-MIB and the vendor variants laid out like it
	sensorColumnType      = 1
	sensorColumnScale     = 2
	sensorColumnPrecision = 3
	sensorColumnValue     = 4
	sensorColumnStatus    = 5

	sensorTypeWatts = 6
	sensorStatusOK  = 1
	sensorScaleUnit = 9

	// Threshold table columns and relations, as in CISCO-ENTITY-SENSOR-MIB entSensorThresholdTable
	thresholdColumnRelation = 3
	thresholdColumnValue    = 4
	thresholdLessThan       = 1
	thresholdLessOrEqual    = 2
	thresholdGreaterThan    = 3
	thresholdGreaterOrEqual = 4

	maxEntityDepth = 16
)

var (
	sensorReceivePattern  = regexp.MustCompile(`(?i)\b(rx|receive|received|input)\b`)
	sensorTransmitPattern = regexp.MustCompile(`(?i)\b(tx|transmit|output)\b`)
	sensorLanePattern     = regexp.MustCompile(`(?i)\blane\s*(\d+)`)
)

// SensorTable describes a sensor table laid out like ENTITY-SENSOR-MIB entPhySensorTable: indexed by entPhysicalIndex,
// with the sensor type, scale, precision, value and status in columns 1 to 5. Vendors extend it in different ways, so
// the optional fields say which extensions a table has.
type SensorTable struct {
	EntryOID string
	// UnitsColumn holds a units label, which marks sensors of type "other" that report dBm
	UnitsColumn int
	// DBmType is a sensor type value that reports dBm directly, which the standard MIB lacks
	DBmType int
	// MeasuredEntityColumn holds the entity a sensor measures, followed instead of the sensor's own entity to find
	// its interface
	MeasuredEntityColumn int
	// ThresholdEntryOID is a table of alarm thresholds indexed by sensor and threshold, with the relation in column 3
	// and the value in column 4
	ThresholdEntryOID string
}

// StandardSensors is ENTITY-SENSOR-MIB entPhySensorTable, which has no dBm type, so optics report watts, or dBm as
// "other" with a units label
var StandardSensors = SensorTable{EntryOID: "1.3.6.1.2.1.99.1.1.1", UnitsColumn: 6}

func (t SensorTable) columns() []int {
	columns := []int{sensorColumnType, sensorColumnScale, sensorColumnPrecision, sensorColumnValue, sensorColumnStatus}

	for _, column := range []int{t.UnitsColumn, t.MeasuredEntityColumn} {
		if column > 0 {
			columns = append(columns, column)
		}
	}

	return columns
}

// ReadSensors reads light levels from the first of tables with optical power sensors that match an interface. Sensors
// are traced through the ENTITY-MIB hierarchy in physicalRows (entPhysicalTable columns 2, 4 and 7) back to an
// interface.
func ReadSensors(client *gosnmp.GoSNMP, tables []SensorTable, physicalRows map[string]snmp.Row, interfaces []api.Interface) (Readings, error) {
	readings := make(Readings)

	var aliasRows map[string]snmp.Row

	for _, table := range tables {
		sensorRows, err := snmp.WalkTable(client, table.EntryOID, table.columns()...)

		if err != nil {
			return readings, err
		}

		if !hasOpticalSensors(table, sensorRows) {
			continue
		}

		if aliasRows == nil {
			if aliasRows, err = snmp.WalkTable(client, oidEntityAliasMappingEntry, 2); err != nil {
				return readings, err
			}
		}

		var thresholdRows map[string]snmp.Row

		if len(table.ThresholdEntryOID) > 0 {
			if thresholdRows, err = snmp.WalkTable(client, table.ThresholdEntryOID, thresholdColumnRelation, thresholdColumnValue); err != nil {
				return readings, err
			}
		}

		sensorReadings(table, sensorRows, physicalRows, aliasRows, thresholdRows, interfaces, readings)

		if len(readings) > 0 {
			break
		}
	}

	return readings, nil
}

func hasOpticalSensors(table SensorTable, sensorRows map[string]snmp.Row) bool {
	for _, row := range sensorRows {
		if _, ok := sensorDBm(table, row); ok {
			return true
		}
	}

	return false
}

// sensorDBm returns the value of an optical power sensor in dBm
func sensorDBm(table SensorTable, row snmp.Row) (float64, bool) {
	if row.Int(sensorColumnStatus) != sensorStatusOK {
		return 0, false
	}

	value, ok := row.Number(sensorColumnValue)

	if !ok {
		return 0, false
	}

	return sensorValueDBm(table, row, value)
}

// sensorValueDBm converts a raw value of an optical power sensor, either its reading or one of its thresholds, to dBm
// using the sensor's type, scale and precision
func sensorValueDBm(table SensorTable, row snmp.Row, value int64) (float64, bool) {
	magnitude := float64(value) * math.Pow(10, float64(3*(row.Int(sensorColumnScale)-sensorScaleUnit))) / math.Pow(10, float64(row.Int(sensorColumnPrecision)))
	sensorType := row.Int(sensorColumnType)

	switch {
	case table.DBmType > 0 && sensorType == int64(table.DBmType):
		return magnitude, true
	case sensorType == sensorTypeWatts:
		return MilliwattsToDBm(magnitude * 1000), true
	case table.UnitsColumn > 0 && strings.Contains(strings.ToLower(row.String(table.UnitsColumn)), "dbm"):
		return magnitude, true
	default:
		return 0, false
	}
}

// sensorReadings matches optical power sensors to interfaces. A sensor is optical when its type is power and its name
// says whether it measures received or transmitted light. It belongs to the interface that the sensor (or the entity it
// measures, or the closest entity containing it) is mapped to in entAliasMappingTable, or whose name matches the
// entity's name.
func sensorReadings(table SensorTable, sensorRows, physicalRows, aliasRows, thresholdRows map[string]snmp.Row, interfaces []api.Interface, readings Readings) {
	entityInterfaces := aliasInterfaceIndexes(aliasRows)
	sensorThresholds := groupSensorThresholds(thresholdRows)

	for index, row := range sensorRows {
		dBm, ok := sensorDBm(table, row)

		if !ok {
			continue
		}

		entity := physicalRows[index]
		label := entity.String(7) + " " + entity.String(2)
		receive := sensorReceivePattern.MatchString(label)

		if !receive && !sensorTransmitPattern.MatchString(label) {
			continue
		}

		start := index

		if table.MeasuredEntityColumn > 0 {
			if measured := row.Int(table.MeasuredEntityColumn); measured > 0 {
				start = strconv.FormatInt(measured, 10)
			}
		}

		interfaceIndex, ok := resolveEntityInterface(start, physicalRows, entityInterfaces, interfaces)

		if !ok {
			continue
		}

		lane := 0

		if match := sensorLanePattern.FindStringSubmatch(label); match != nil {
			lane, _ = strconv.Atoi(match[1])
		}

		readings.Add(interfaceIndex, lane, receive, dBm)

		if low, high, ok := sensorThresholdsDBm(table, row, sensorThresholds[index]); ok {
			readings.SetThresholds(interfaceIndex, receive, low, high)
		}
	}
}

// groupSensorThresholds groups the rows of a threshold table, indexed by sensor and then threshold, by the sensor they
// belong to
func groupSensorThresholds(thresholdRows map[string]snmp.Row) map[string][]snmp.Row {
	result := make(map[string][]snmp.Row)

	for index, row := range thresholdRows {
		sensorIndex, _, _ := strings.Cut(index, ".")
		result[sensorIndex] = append(result[sensorIndex], row)
	}

	return result
}

// sensorThresholdsDBm returns the alarm thresholds of a sensor: the lowest value it alarms below and the highest value it
// alarms above. Warning thresholds sit inside those, so the outermost pair are the alarm thresholds.
func sensorThresholdsDBm(table SensorTable, row snmp.Row, thresholdRows []snmp.Row) (float64, float64, bool) {
	low, high := math.Inf(1), math.Inf(-1)

	for _, threshold := range thresholdRows {
		raw, ok := threshold.Number(thresholdColumnValue)

		if !ok {
			continue
		}

		value, ok := sensorValueDBm(table, row, raw)

		if !ok {
			continue
		}

		switch threshold.Int(thresholdColumnRelation) {
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
func aliasInterfaceIndexes(aliasRows map[string]snmp.Row) map[string]int {
	result := make(map[string]int)

	for index, row := range aliasRows {
		entityIndex, _, _ := strings.Cut(index, ".")
		rawInterface, ok := strings.CutPrefix(row.String(2), oidInterfaceIndexColumn)

		if !ok {
			continue
		}

		if interfaceIndex, err := strconv.Atoi(rawInterface); err == nil {
			result[entityIndex] = interfaceIndex
		}
	}

	return result
}

func resolveEntityInterface(entityIndex string, physicalRows map[string]snmp.Row, entityInterfaces map[string]int, interfaces []api.Interface) (int, bool) {
	for depth := 0; depth < maxEntityDepth && entityIndex != "0" && len(entityIndex) > 0; depth++ {
		if interfaceIndex, ok := entityInterfaces[entityIndex]; ok {
			return interfaceIndex, true
		}

		if item := FindInterfaceByName(interfaces, physicalRows[entityIndex].String(7)); item != nil {
			return item.Index, true
		}

		entityIndex = strconv.FormatInt(physicalRows[entityIndex].Int(4), 10)
	}

	return 0, false
}
