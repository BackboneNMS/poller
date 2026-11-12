package optics

import (
	"main/src/api"
	"main/src/entity"
	"main/src/snmp"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	// Sensor table columns shared by ENTITY-SENSOR-MIB and the vendor variants laid out like it
	sensorColumnType      = 1
	sensorColumnScale     = 2
	sensorColumnPrecision = 3
	sensorColumnValue     = 4
	sensorColumnStatus    = 5

	sensorTypeVoltsDC = 4
	sensorTypeAmperes = 5
	sensorTypeWatts   = 6
	sensorTypeCelsius = 8
	sensorStatusOK    = 1
	sensorScaleUnit   = 9

	// Threshold table columns and relations, as in CISCO-ENTITY-SENSOR-MIB entSensorThresholdTable
	thresholdColumnRelation = 3
	thresholdColumnValue    = 4
	thresholdLessThan       = 1
	thresholdLessOrEqual    = 2
	thresholdGreaterThan    = 3
	thresholdGreaterOrEqual = 4
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

// ReadSensors reads transceiver diagnostics from the first of tables with optical power sensors that match an
// interface. Sensors are traced through the device's physical entities back to an interface.
func ReadSensors(client *gosnmp.GoSNMP, tables []SensorTable, entities *entity.Table, interfaces []api.Interface) (Readings, error) {
	readings := make(Readings)

	for _, table := range tables {
		sensorRows, err := snmp.WalkTable(client, table.EntryOID, table.columns()...)

		if err != nil {
			return readings, err
		}

		if !hasOpticalSensors(table, sensorRows) {
			continue
		}

		var thresholdRows map[string]snmp.Row

		if len(table.ThresholdEntryOID) > 0 {
			if thresholdRows, err = snmp.WalkTable(client, table.ThresholdEntryOID, thresholdColumnRelation, thresholdColumnValue); err != nil {
				return readings, err
			}
		}

		sensorReadings(table, sensorRows, entities, thresholdRows, interfaces, readings)

		if readings.Count() > 0 {
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
	magnitude := sensorMagnitude(row, value)
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

// sensorMagnitude converts a raw sensor value to its unit (watts, volts, amperes or degrees) using the sensor's scale
// and precision
func sensorMagnitude(row snmp.Row, value int64) float64 {
	return float64(value) * math.Pow(10, float64(3*(row.Int(sensorColumnScale)-sensorScaleUnit))) / math.Pow(10, float64(row.Int(sensorColumnPrecision)))
}

// sensorReadings matches optical power sensors to interfaces. A sensor is optical when its type is power and its name
// says whether it measures received or transmitted light. It belongs to the interface that the sensor (or the entity it
// measures, or the closest entity containing it) is mapped to in entAliasMappingTable, or whose name matches the
// entity's name. The temperature, voltage and current sensors of interfaces with light levels are then read as their
// transceiver's temperature, supply voltage and laser bias current.
func sensorReadings(table SensorTable, sensorRows map[string]snmp.Row, entities *entity.Table, thresholdRows map[string]snmp.Row, interfaces []api.Interface, readings Readings) {
	sensorThresholds := groupSensorThresholds(thresholdRows)

	for index, row := range sensorRows {
		dBm, ok := sensorDBm(table, row)

		if !ok {
			continue
		}

		label := sensorLabel(entities, index)
		receive := sensorReceivePattern.MatchString(label)

		if !receive && !sensorTransmitPattern.MatchString(label) {
			continue
		}

		interfaceIndex, ok := sensorInterface(table, index, row, entities, interfaces)

		if !ok {
			continue
		}

		readings.Add(interfaceIndex, sensorLane(label), receive, dBm)

		if low, high, ok := sensorThresholdsDBm(table, row, sensorThresholds[index]); ok {
			readings.SetThresholds(interfaceIndex, receive, low, high)
		}
	}

	for index, row := range sensorRows {
		sensorType := row.Int(sensorColumnType)
		value, ok := row.Number(sensorColumnValue)

		if !ok || row.Int(sensorColumnStatus) != sensorStatusOK || (sensorType != sensorTypeCelsius && sensorType != sensorTypeVoltsDC && sensorType != sensorTypeAmperes) {
			continue
		}

		interfaceIndex, ok := sensorInterface(table, index, row, entities, interfaces)

		if !ok || !readings.Has(interfaceIndex) {
			continue
		}

		magnitude := sensorMagnitude(row, value)

		switch sensorType {
		case sensorTypeCelsius:
			readings.SetTemperature(interfaceIndex, magnitude)
		case sensorTypeVoltsDC:
			readings.SetVoltage(interfaceIndex, magnitude)
		case sensorTypeAmperes:
			readings.AddBiasCurrent(interfaceIndex, sensorLane(sensorLabel(entities, index)), magnitude*1000)
		}
	}
}

// sensorLabel is what a sensor's entity is called, its name and description, which say what the sensor measures
func sensorLabel(entities *entity.Table, index string) string {
	row := entities.Rows[index]

	return row.String(entity.ColumnName) + " " + row.String(entity.ColumnDescription)
}

// sensorLane is the lane a sensor's label names, or 0
func sensorLane(label string) int {
	if match := sensorLanePattern.FindStringSubmatch(label); match != nil {
		lane, _ := strconv.Atoi(match[1])

		return lane
	}

	return 0
}

// sensorInterface finds the interface a sensor belongs to, starting from the entity it measures when the table says
func sensorInterface(table SensorTable, index string, row snmp.Row, entities *entity.Table, interfaces []api.Interface) (int, bool) {
	start := index

	if table.MeasuredEntityColumn > 0 {
		if measured := row.Int(table.MeasuredEntityColumn); measured > 0 {
			start = strconv.FormatInt(measured, 10)
		}
	}

	return entities.Interface(start, interfaces)
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
