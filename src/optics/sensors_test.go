package optics

import (
	"main/src/api"
	"main/src/entity"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"slices"
	"testing"
)

// ciscoStyle has every extension of SensorTable, laid out like CISCO-ENTITY-SENSOR-MIB
var ciscoStyle = SensorTable{EntryOID: "1.3.6.1.4.1.9.9.91.1.1.1.1", DBmType: 14, MeasuredEntityColumn: 8, ThresholdEntryOID: "1.3.6.1.4.1.9.9.91.1.2.1.1"}

func expectOptical(t *testing.T, interfaces []api.Interface, index int, receive, transmit []float64) {
	t.Helper()

	for _, item := range interfaces {
		if item.Index != index {
			continue
		}

		if item.Optical == nil {
			t.Fatalf("interface %d: expected optical levels, got none", index)
		}

		if !slices.Equal(item.Optical.RxPowerDBm, receive) || !slices.Equal(item.Optical.TxPowerDBm, transmit) {
			t.Fatalf("interface %d: expected rx %v tx %v, got rx %v tx %v", index, receive, transmit, item.Optical.RxPowerDBm, item.Optical.TxPowerDBm)
		}

		return
	}

	t.Fatalf("interface %d not found", index)
}

func expectThresholds(t *testing.T, interfaces []api.Interface, index int, receive, transmit *api.OpticalThresholds) {
	t.Helper()

	equal := func(a, b *api.OpticalThresholds) bool {
		return a == nil && b == nil || a != nil && b != nil && *a == *b
	}

	for _, item := range interfaces {
		if item.Index != index {
			continue
		}

		if item.Optical == nil {
			t.Fatalf("interface %d: expected optical levels, got none", index)
		}

		if !equal(item.Optical.RxThresholds, receive) || !equal(item.Optical.TxThresholds, transmit) {
			t.Fatalf("interface %d: expected thresholds rx %+v tx %+v, got rx %+v tx %+v", index, receive, transmit, item.Optical.RxThresholds, item.Optical.TxThresholds)
		}

		return
	}

	t.Fatalf("interface %d not found", index)
}

func testInterfaces() []api.Interface {
	return []api.Interface{
		{Index: 10, Name: "xe-0/0/0", Description: "xe-0/0/0"},
		{Index: 11, Name: "et-0/0/1", Description: "et-0/0/1"},
		{Index: 12, Name: "sfp-sfpplus1", Description: "sfp-sfpplus1"},
		{Index: 13, Name: "Te1/1/1", Description: "TenGigabitEthernet1/1/1"},
		{Index: 14, Name: "ge-0/0/2", Description: "ge-0/0/2"},
	}
}

func TestDBmSensorsWithMeasuredEntityAndThresholds(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(Readings)

	physicalRows := map[string]snmp.Row{
		"1000": {4: snmptest.Integer(1), 5: snmptest.Integer(10), 7: snmptest.OctetString("TenGigabitEthernet1/1/1")},
		"1001": {2: snmptest.OctetString("Te1/1/1 Receive Power Sensor"), 4: snmptest.Integer(1000), 5: snmptest.Integer(8)},
		"1002": {2: snmptest.OctetString("Te1/1/1 Transmit Power Sensor"), 4: snmptest.Integer(1000), 5: snmptest.Integer(8)},
		"1003": {2: snmptest.OctetString("Te1/1/1 Temperature Sensor"), 4: snmptest.Integer(1000), 5: snmptest.Integer(8)},
		"1004": {2: snmptest.OctetString("Te1/1/1 Supply Voltage Sensor"), 4: snmptest.Integer(1000), 5: snmptest.Integer(8)},
		"1005": {2: snmptest.OctetString("Te1/1/1 Bias Current Sensor"), 4: snmptest.Integer(1000), 5: snmptest.Integer(8)},
		"2000": {2: snmptest.OctetString("Chassis Temperature Sensor"), 4: snmptest.Integer(1), 5: snmptest.Integer(8)},
	}

	sensorRows := map[string]snmp.Row{
		"1001": {1: snmptest.Integer(14), 2: snmptest.Integer(9), 3: snmptest.Integer(1), 4: snmptest.Integer(-45), 5: snmptest.Integer(1), 8: snmptest.Integer(1000)},
		"1002": {1: snmptest.Integer(14), 2: snmptest.Integer(9), 3: snmptest.Integer(1), 4: snmptest.Integer(-21), 5: snmptest.Integer(1), 8: snmptest.Integer(1000)},
		"1003": {1: snmptest.Integer(8), 2: snmptest.Integer(9), 3: snmptest.Integer(1), 4: snmptest.Integer(312), 5: snmptest.Integer(1), 8: snmptest.Integer(1000)},
		"1004": {1: snmptest.Integer(4), 2: snmptest.Integer(8), 3: snmptest.Integer(0), 4: snmptest.Integer(3291), 5: snmptest.Integer(1), 8: snmptest.Integer(1000)},
		"1005": {1: snmptest.Integer(5), 2: snmptest.Integer(8), 3: snmptest.Integer(1), 4: snmptest.Integer(64), 5: snmptest.Integer(1), 8: snmptest.Integer(1000)},
		"2000": {1: snmptest.Integer(8), 2: snmptest.Integer(9), 3: snmptest.Integer(0), 4: snmptest.Integer(45), 5: snmptest.Integer(1)},
	}

	aliasRows := map[string]snmp.Row{
		"1000.0": {2: snmptest.ObjectID(".1.3.6.1.2.1.2.2.1.1.13")},
	}

	if !hasOpticalSensors(ciscoStyle, sensorRows) {
		t.Fatal("expected Cisco dBm sensors to be detected")
	}

	thresholdRows := map[string]snmp.Row{
		"1001.1": {2: snmptest.Integer(30), 3: snmptest.Integer(thresholdGreaterOrEqual), 4: snmptest.Integer(20)},
		"1001.2": {2: snmptest.Integer(10), 3: snmptest.Integer(thresholdGreaterOrEqual), 4: snmptest.Integer(-10)},
		"1001.3": {2: snmptest.Integer(10), 3: snmptest.Integer(thresholdLessOrEqual), 4: snmptest.Integer(-130)},
		"1001.4": {2: snmptest.Integer(30), 3: snmptest.Integer(thresholdLessOrEqual), 4: snmptest.Integer(-170)},
		"1002.1": {2: snmptest.Integer(30), 3: snmptest.Integer(thresholdGreaterThan), 4: snmptest.Integer(17)},
	}

	sensorReadings(ciscoStyle, sensorRows, entity.NewTable(physicalRows, aliasRows), thresholdRows, interfaces, readings)
	Apply(interfaces, readings)

	expectOptical(t, interfaces, 13, []float64{-4.5}, []float64{-2.1})
	expectThresholds(t, interfaces, 13, &api.OpticalThresholds{LowDBm: -17, HighDBm: 2}, nil)

	// 3291 mV and 6.4 mA (milli scale), 31.2 °C; the chassis sensor belongs to no interface
	optical := interfaces[3].Optical

	if optical.TemperatureCelsius == nil || *optical.TemperatureCelsius != 31.2 || optical.VoltageVolts == nil || *optical.VoltageVolts != 3.291 || !slices.Equal(optical.BiasCurrentMA, []float64{6.4}) {
		t.Fatalf("expected the transceiver's temperature, voltage and bias current, got %+v", optical)
	}

	for _, item := range interfaces {
		if item.Index != 13 && item.Optical != nil {
			t.Fatalf("expected only interface 13 to have diagnostics, got %+v on %d", item.Optical, item.Index)
		}
	}
}

func TestStandardSensorReadingsInWatts(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(Readings)

	physicalRows := map[string]snmp.Row{
		"20": {4: snmptest.Integer(0), 5: snmptest.Integer(10), 7: snmptest.OctetString("et-0/0/1")},
		"21": {4: snmptest.Integer(20), 5: snmptest.Integer(9), 7: snmptest.OctetString("Transceiver et-0/0/1")},
		"22": {4: snmptest.Integer(21), 5: snmptest.Integer(8), 7: snmptest.OctetString("DOM Rx Power Lane 2")},
		"23": {4: snmptest.Integer(21), 5: snmptest.Integer(8), 7: snmptest.OctetString("DOM Rx Power Lane 1")},
		"24": {4: snmptest.Integer(21), 5: snmptest.Integer(8), 7: snmptest.OctetString("DOM Tx Power Lane 1")},
		"25": {4: snmptest.Integer(21), 5: snmptest.Integer(8), 7: snmptest.OctetString("DOM Tx Power Lane 2")},
	}

	sensorRows := map[string]snmp.Row{
		"22": {1: snmptest.Integer(sensorTypeWatts), 2: snmptest.Integer(6), 3: snmptest.Integer(0), 4: snmptest.Integer(500), 5: snmptest.Integer(1)},
		"23": {1: snmptest.Integer(sensorTypeWatts), 2: snmptest.Integer(7), 3: snmptest.Integer(0), 4: snmptest.Integer(1000), 5: snmptest.Integer(1)},
		"24": {1: snmptest.Integer(sensorTypeWatts), 2: snmptest.Integer(7), 3: snmptest.Integer(1), 4: snmptest.Integer(20000), 5: snmptest.Integer(1)},
		"25": {1: snmptest.Integer(sensorTypeWatts), 2: snmptest.Integer(7), 3: snmptest.Integer(0), 4: snmptest.Integer(0), 5: snmptest.Integer(1)},
	}

	if !hasOpticalSensors(StandardSensors, sensorRows) {
		t.Fatal("expected sensors reporting watts to be detected")
	}

	if hasOpticalSensors(StandardSensors, map[string]snmp.Row{"1": {1: snmptest.Integer(8), 4: snmptest.Integer(312), 5: snmptest.Integer(1)}}) {
		t.Fatal("expected temperature sensors to be ignored")
	}

	sensorReadings(StandardSensors, sensorRows, entity.NewTable(physicalRows, nil), nil, interfaces, readings)
	Apply(interfaces, readings)

	// 1000 µW = 0 dBm, 500 nW = -33.01 dBm, 2000 µW = 3.01 dBm, 0 W = no light
	expectOptical(t, interfaces, 11, []float64{0, -33.01}, []float64{3.01, -40})
}

func TestSensorsWithoutDirectionAreIgnored(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(Readings)

	sensorReadings(
		StandardSensors,
		map[string]snmp.Row{"30": {1: snmptest.Integer(sensorTypeWatts), 2: snmptest.Integer(9), 3: snmptest.Integer(0), 4: snmptest.Integer(150), 5: snmptest.Integer(1)}},
		entity.NewTable(map[string]snmp.Row{"30": {4: snmptest.Integer(0), 5: snmptest.Integer(8), 7: snmptest.OctetString("PSU 1 Power")}}, nil),
		nil,
		interfaces,
		readings,
	)

	if len(readings) != 0 {
		t.Fatalf("expected power supply sensors to be ignored, got %v", readings)
	}
}
