package main

import (
	"main/src/dashboard"
	"slices"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func integer(value int) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.Integer, Value: value}
}

func octets(value string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte(value)}
}

func objectID(value string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.ObjectIdentifier, Value: value}
}

func expectOptical(t *testing.T, interfaces []dashboard.Interface, index int, receive, transmit []float64) {
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

func expectThresholds(t *testing.T, interfaces []dashboard.Interface, index int, receive, transmit *dashboard.OpticalThresholds) {
	t.Helper()

	equal := func(a, b *dashboard.OpticalThresholds) bool {
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

func testInterfaces() []dashboard.Interface {
	return []dashboard.Interface{
		{Index: 10, Name: "xe-0/0/0", Description: "xe-0/0/0"},
		{Index: 11, Name: "et-0/0/1", Description: "et-0/0/1"},
		{Index: 12, Name: "sfp-sfpplus1", Description: "sfp-sfpplus1"},
		{Index: 13, Name: "Te1/1/1", Description: "TenGigabitEthernet1/1/1"},
		{Index: 14, Name: "ge-0/0/2", Description: "ge-0/0/2"},
	}
}

func TestJuniperReadings(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(opticalReadings)

	rows := map[string]snmpRow{
		"10": {5: integer(-312), 7: integer(-158), 9: integer(50), 10: integer(-1440), 17: integer(0), 18: integer(0)},
		"11": {5: integer(-999), 7: integer(-999), 9: integer(450), 10: integer(-1060), 17: integer(500), 18: integer(-940)},
		"14": {5: integer(-5000), 7: integer(50)},
	}

	laneRows := map[string]snmpRow{
		"11.1": {6: integer(-210), 8: integer(105)},
		"11.0": {6: integer(-205), 8: integer(110)},
	}

	juniperReadings(rows, laneRows, readings)
	applyOptics(interfaces, readings)

	expectOptical(t, interfaces, 10, []float64{-3.12}, []float64{-1.58})
	expectOptical(t, interfaces, 11, []float64{-2.05, -2.1}, []float64{1.1, 1.05})
	expectOptical(t, interfaces, 14, []float64{-40}, []float64{0.5})

	expectThresholds(t, interfaces, 10, &dashboard.OpticalThresholds{LowDBm: -14.4, HighDBm: 0.5}, nil)
	expectThresholds(t, interfaces, 11, &dashboard.OpticalThresholds{LowDBm: -10.6, HighDBm: 4.5}, &dashboard.OpticalThresholds{LowDBm: -9.4, HighDBm: 5})
	expectThresholds(t, interfaces, 14, nil, nil)
}

func TestMikroTikReadings(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(opticalReadings)

	mikroTikReadings(map[string]snmpRow{
		"3": {2: octets("sfp-sfpplus1"), 9: integer(-2104), 10: integer(-6532)},
	}, interfaces, readings)
	applyOptics(interfaces, readings)

	expectOptical(t, interfaces, 12, []float64{-6.53}, []float64{-2.1})
}

func TestCiscoSensorReadings(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(opticalReadings)

	physicalRows := map[string]snmpRow{
		"1000": {4: integer(1), 5: integer(10), 7: octets("TenGigabitEthernet1/1/1")},
		"1001": {2: octets("Te1/1/1 Receive Power Sensor"), 4: integer(1000), 5: integer(8)},
		"1002": {2: octets("Te1/1/1 Transmit Power Sensor"), 4: integer(1000), 5: integer(8)},
		"1003": {2: octets("Te1/1/1 Temperature Sensor"), 4: integer(1000), 5: integer(8)},
	}

	sensorRows := map[string]snmpRow{
		"1001": {1: integer(sensorTypeDBm), 2: integer(9), 3: integer(1), 4: integer(-45), 5: integer(1), 8: integer(1000)},
		"1002": {1: integer(sensorTypeDBm), 2: integer(9), 3: integer(1), 4: integer(-21), 5: integer(1), 8: integer(1000)},
		"1003": {1: integer(8), 2: integer(9), 3: integer(1), 4: integer(312), 5: integer(1), 8: integer(1000)},
	}

	aliasRows := map[string]snmpRow{
		"1000.0": {2: objectID(".1.3.6.1.2.1.2.2.1.1.13")},
	}

	if !hasOpticalSensors(sensorRows, true) {
		t.Fatal("expected Cisco dBm sensors to be detected")
	}

	thresholdRows := map[string]snmpRow{
		"1001.1": {2: integer(30), 3: integer(thresholdGreaterOrEqual), 4: integer(20)},
		"1001.2": {2: integer(10), 3: integer(thresholdGreaterOrEqual), 4: integer(-10)},
		"1001.3": {2: integer(10), 3: integer(thresholdLessOrEqual), 4: integer(-130)},
		"1001.4": {2: integer(30), 3: integer(thresholdLessOrEqual), 4: integer(-170)},
		"1002.1": {2: integer(30), 3: integer(thresholdGreaterThan), 4: integer(17)},
	}

	entitySensorReadings(sensorRows, true, physicalRows, aliasRows, thresholdRows, interfaces, readings)
	applyOptics(interfaces, readings)

	expectOptical(t, interfaces, 13, []float64{-4.5}, []float64{-2.1})
	expectThresholds(t, interfaces, 13, &dashboard.OpticalThresholds{LowDBm: -17, HighDBm: 2}, nil)
}

func TestStandardSensorReadingsInWatts(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(opticalReadings)

	physicalRows := map[string]snmpRow{
		"20": {4: integer(0), 5: integer(10), 7: octets("et-0/0/1")},
		"21": {4: integer(20), 5: integer(9), 7: octets("Transceiver et-0/0/1")},
		"22": {4: integer(21), 5: integer(8), 7: octets("DOM Rx Power Lane 2")},
		"23": {4: integer(21), 5: integer(8), 7: octets("DOM Rx Power Lane 1")},
		"24": {4: integer(21), 5: integer(8), 7: octets("DOM Tx Power Lane 1")},
		"25": {4: integer(21), 5: integer(8), 7: octets("DOM Tx Power Lane 2")},
	}

	sensorRows := map[string]snmpRow{
		"22": {1: integer(sensorTypeWatts), 2: integer(6), 3: integer(0), 4: integer(500), 5: integer(1)},
		"23": {1: integer(sensorTypeWatts), 2: integer(7), 3: integer(0), 4: integer(1000), 5: integer(1)},
		"24": {1: integer(sensorTypeWatts), 2: integer(7), 3: integer(1), 4: integer(20000), 5: integer(1)},
		"25": {1: integer(sensorTypeWatts), 2: integer(7), 3: integer(0), 4: integer(0), 5: integer(1)},
	}

	if !hasOpticalSensors(sensorRows, false) {
		t.Fatal("expected sensors reporting watts to be detected")
	}

	if hasOpticalSensors(map[string]snmpRow{"1": {1: integer(8), 4: integer(312), 5: integer(1)}}, false) {
		t.Fatal("expected temperature sensors to be ignored")
	}

	entitySensorReadings(sensorRows, false, physicalRows, map[string]snmpRow{}, nil, interfaces, readings)
	applyOptics(interfaces, readings)

	// 1000 µW = 0 dBm, 500 nW = -33.01 dBm, 2000 µW = 3.01 dBm, 0 W = no light
	expectOptical(t, interfaces, 11, []float64{0, -33.01}, []float64{3.01, -40})
}

func TestSensorsWithoutDirectionAreIgnored(t *testing.T) {
	interfaces := testInterfaces()
	readings := make(opticalReadings)

	entitySensorReadings(
		map[string]snmpRow{"30": {1: integer(sensorTypeWatts), 2: integer(9), 3: integer(0), 4: integer(150), 5: integer(1)}},
		false,
		map[string]snmpRow{"30": {4: integer(0), 5: integer(8), 7: octets("PSU 1 Power")}},
		map[string]snmpRow{},
		nil,
		interfaces,
		readings,
	)

	if len(readings) != 0 {
		t.Fatalf("expected power supply sensors to be ignored, got %v", readings)
	}
}
