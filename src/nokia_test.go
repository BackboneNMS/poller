package main

import (
	"main/src/api"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func gauge(value uint) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Type: gosnmp.Gauge32, Value: value}
}

func TestNokiaDescription(t *testing.T) {
	for description, expected := range map[string][2]string{
		"TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia.\r\nAll rights reserved.":      {"20.10.R3", "7750 SR"},
		"TiMOS-B-21.9.R1 both/mpc Nokia SAS-Mxp 22F2C 4SFP+ 7210 Copyright (c) 2000-2021 Nokia.":                 {"21.9.R1", "SAS-Mxp 22F2C 4SFP+ 7210"},
		"TiMOS-C-16.0.R6 cpm/hops64 ALCATEL SR 7750 Copyright (c) 2000-2018 Nokia.":                              {"16.0.R6", "SR 7750"},
		"TiMOS-B-13.0.R4 both/hops Alcatel-Lucent SAS-M 24F 2XFP-1 7210 Copyright (c) 2000-2015 Alcatel-Lucent.": {"13.0.R4", "SAS-M 24F 2XFP-1 7210"},
		"Linux core-1 5.15.0": {"", ""},
	} {
		if version, model := nokiaDescription(description); version != expected[0] || model != expected[1] {
			t.Errorf("%q: expected %v, got %q %q", description, expected, version, model)
		}
	}
}

func TestNokiaDetection(t *testing.T) {
	system := &api.System{ObjectID: "1.3.6.1.4.1.6527.1.3.17", Description: "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia."}

	applyHardwareDetails(system, nil)

	if system.Manufacturer != "Nokia" || system.Model != "7750 SR" {
		t.Fatalf("unexpected SR OS details %+v", system)
	}

	if manufacturer := enterpriseManufacturer(objectIDEnterpriseNumber("1.3.6.1.4.1.637.61.1")); manufacturer != "Nokia" {
		t.Fatalf("expected ISAM to be Nokia, got %q", manufacturer)
	}
}

func TestTenthMicrowattsToDBm(t *testing.T) {
	// The worked example in TIMETRA-PORT-MIB: 790 tenths of a microwatt is -11.0 dBm
	if value := roundDBm(tenthMicrowattsToDBm(790)); value != -11.02 {
		t.Fatalf("expected -11.02 dBm, got %v", value)
	}

	if value := tenthMicrowattsToDBm(10000); value != 0 {
		t.Fatalf("expected 1 mW to be 0 dBm, got %v", value)
	}

	if value := tenthMicrowattsToDBm(0); value != noLightDBm {
		t.Fatalf("expected no light to be %v, got %v", noLightDBm, value)
	}
}

func TestNokiaReadings(t *testing.T) {
	interfaces := []api.Interface{{Index: 35684352}, {Index: 35717120}, {Index: 35749888}}
	readings := make(opticalReadings)

	nokiaReadings(
		map[string]snmpRow{
			// A single-lane SFP: rx 790 (-11.02 dBm), tx 5000 (-3.01 dBm), with thresholds
			"1.35684352": {21: integer(790), 23: integer(100), 25: integer(20000), 16: integer(5000), 18: integer(1000), 20: integer(15000)},
			// A QSFP, which also has a per-port row that the lanes take precedence over
			"1.35717120": {21: integer(1), 16: integer(1)},
			// An optic without light or thresholds
			"1.35749888": {21: integer(0), 23: integer(0), 25: integer(0), 16: integer(0)},
		},
		map[string]snmpRow{
			"1.35717120.1": {17: integer(10000), 19: integer(500), 21: integer(30000), 12: integer(10000)},
			"1.35717120.2": {17: integer(5000), 12: integer(10000)},
		},
		readings,
	)

	applyOptics(interfaces, readings)

	expectOptical(t, interfaces, 35684352, []float64{-11.02}, []float64{-3.01})
	expectOptical(t, interfaces, 35717120, []float64{0, -3.01}, []float64{0, 0})
	expectOptical(t, interfaces, 35749888, []float64{noLightDBm}, []float64{noLightDBm})

	if thresholds := interfaces[0].Optical.RxThresholds; thresholds == nil || thresholds.LowDBm != -20 || thresholds.HighDBm != 3.01 {
		t.Fatalf("unexpected rx thresholds %+v", thresholds)
	}

	if thresholds := interfaces[1].Optical.RxThresholds; thresholds == nil || thresholds.LowDBm != -13.01 || thresholds.HighDBm != 4.77 {
		t.Fatalf("unexpected lane rx thresholds %+v", thresholds)
	}

	if interfaces[2].Optical.RxThresholds != nil {
		t.Fatal("expected zero thresholds to be ignored")
	}
}

func TestApplyNokiaHardware(t *testing.T) {
	system := &api.System{}

	applyNokiaHardware(system, map[string]snmpRow{
		"1.50331649":  {7: integer(nokiaHardwareClassChassis), 5: octets("NS1234567890")},
		"1.83886081":  {7: integer(nokiaHardwareClassCPM), 5: octets("NS0000000001"), 33: octets("TiMOS-L-20.10.R3")},
		"1.150994977": {7: integer(13), 5: octets("SFP-SERIAL")},
	})

	if system.SerialNumber != "NS1234567890" || system.FirmwareVersion != "TiMOS-L-20.10.R3" {
		t.Fatalf("unexpected hardware details %+v", system)
	}
}

func TestApplyISAMHolder(t *testing.T) {
	system := &api.System{}

	applyISAMHolder(system, map[string]snmpRow{
		"1":  {4: octets("EMPTY")},
		"17": {4: octets("NFXS-E"), 13: octets("YP1234567890")},
	})

	if system.Model != "NFXS-E" || system.SerialNumber != "YP1234567890" {
		t.Fatalf("unexpected main holder details %+v", system)
	}

	fallback := &api.System{}

	applyISAMHolder(fallback, map[string]snmpRow{"3": {4: octets("UNKNOWN"), 13: octets("AB123")}, "2": {4: octets("NFXS-F"), 13: octets("CD456")}})

	if fallback.Model != "NFXS-F" || fallback.SerialNumber != "CD456" {
		t.Fatalf("expected the first holder with a serial number, got %+v", fallback)
	}
}

func TestApplyISAMResources(t *testing.T) {
	resources := &api.Resources{}

	applyISAMResources(
		resources,
		map[string]snmpRow{"4352": {4: gauge(30)}, "4353": {4: gauge(10)}},
		map[string]snmpRow{"4352": {1: gauge(2048), 2: gauge(1024)}, "4353": {1: gauge(1024), 2: gauge(512)}, "4354": {1: gauge(0), 2: gauge(0)}},
	)

	if *resources.CPULoadPercent != 20 || *resources.MemoryTotalBytes != 3072*1024*1024 || *resources.MemoryUsedBytes != 1536*1024*1024 {
		t.Fatalf("unexpected ISAM resources cpu %v total %v used %v", *resources.CPULoadPercent, *resources.MemoryTotalBytes, *resources.MemoryUsedBytes)
	}

	kept := &api.Resources{CPULoadPercent: PointerOf(5.0)}

	applyISAMResources(kept, map[string]snmpRow{"1": {4: gauge(90)}}, nil)

	if *kept.CPULoadPercent != 5 {
		t.Fatal("expected a CPU load from HOST-RESOURCES-MIB to be kept")
	}
}
