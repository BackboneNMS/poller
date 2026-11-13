package vendors

import (
	"main/src/api"
	"main/src/optics"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"math"
	"testing"

	"github.com/gosnmp/gosnmp"
)

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

func TestTenthMicrowattsToDBm(t *testing.T) {
	// The worked example in TIMETRA-PORT-MIB: 790 tenths of a microwatt is -11.0 dBm
	if value := math.Round(tenthMicrowattsToDBm(790)*100) / 100; value != -11.02 {
		t.Fatalf("expected -11.02 dBm, got %v", value)
	}

	if value := tenthMicrowattsToDBm(10000); value != 0 {
		t.Fatalf("expected 1 mW to be 0 dBm, got %v", value)
	}

	if value := tenthMicrowattsToDBm(0); value != optics.NoLightDBm {
		t.Fatalf("expected no light to be %v, got %v", optics.NoLightDBm, value)
	}
}

func TestNokiaReadings(t *testing.T) {
	readings := nokiaReadings(
		map[string]snmp.Row{
			// A single-lane SFP: rx 790 (-11.02 dBm), tx 5000 (-3.01 dBm), with thresholds
			"1.35684352": {21: snmptest.Integer(790), 23: snmptest.Integer(100), 25: snmptest.Integer(20000), 16: snmptest.Integer(5000), 18: snmptest.Integer(1000), 20: snmptest.Integer(15000)},
			// A QSFP, which also has a per-port row that the lanes take precedence over
			"1.35717120": {21: snmptest.Integer(1), 16: snmptest.Integer(1)},
			// An optic without light or thresholds
			"1.35749888": {21: snmptest.Integer(0), 23: snmptest.Integer(0), 25: snmptest.Integer(0), 16: snmptest.Integer(0)},
		},
		map[string]snmp.Row{
			"1.35717120.1": {17: snmptest.Integer(10000), 19: snmptest.Integer(500), 21: snmptest.Integer(30000), 12: snmptest.Integer(10000)},
			"1.35717120.2": {17: snmptest.Integer(5000), 12: snmptest.Integer(10000)},
		},
	)

	expectLevels(t, readings, 35684352, []float64{-11.02}, []float64{-3.01}, &api.OpticalThresholds{LowDBm: -20, HighDBm: 3.01}, &api.OpticalThresholds{LowDBm: -10, HighDBm: 1.76})
	expectLevels(t, readings, 35717120, []float64{0, -3.01}, []float64{0, 0}, &api.OpticalThresholds{LowDBm: -13.01, HighDBm: 4.77}, nil)
	expectLevels(t, readings, 35749888, []float64{optics.NoLightDBm}, []float64{optics.NoLightDBm}, nil, nil)
}

func TestApplyNokiaHardware(t *testing.T) {
	system := &api.System{}

	applyNokiaHardware(system, map[string]snmp.Row{
		"1.50331649":  {7: snmptest.Integer(nokiaHardwareClassChassis), 5: snmptest.OctetString("NS1234567890")},
		"1.83886081":  {7: snmptest.Integer(nokiaHardwareClassCPM), 5: snmptest.OctetString("NS0000000001"), 33: snmptest.OctetString("TiMOS-L-20.10.R3")},
		"1.150994977": {7: snmptest.Integer(13), 5: snmptest.OctetString("SFP-SERIAL")},
	})

	if system.SerialNumber != "NS1234567890" || system.FirmwareVersion != "TiMOS-L-20.10.R3" {
		t.Fatalf("unexpected hardware details %+v", system)
	}
}

// TestNokiaSROSHooks runs the SR OS hooks against a simulated 7750 SR
func TestNokiaSROSHooks(t *testing.T) {
	client := snmptest.NewClient(t, map[string]gosnmp.SnmpPDU{
		oidNokiaCPUUsage:                          snmptest.Gauge(17),
		oidNokiaKbMemoryAvailable:                 snmptest.Gauge(3000000),
		oidNokiaKbMemoryPoolAllocated:             snmptest.Gauge(1000000),
		oidNokiaHardwareEntry + ".5.1.50331649":   snmptest.OctetString("NS1918F0123"),
		oidNokiaHardwareEntry + ".7.1.50331649":   snmptest.Integer(nokiaHardwareClassChassis),
		oidNokiaHardwareEntry + ".33.1.83886081":  snmptest.OctetString("TiMOS-L-20.10.R3"),
		oidNokiaHardwareEntry + ".7.1.83886081":   snmptest.Integer(nokiaHardwareClassCPM),
		oidNokiaDDMEntry + ".21.1.35684352":       snmptest.Integer(790),
		oidNokiaDDMEntry + ".16.1.35684352":       snmptest.Integer(5000),
		oidNokiaDDMLaneEntry + ".17.1.35717120.1": snmptest.Integer(10000),
		oidNokiaDDMLaneEntry + ".12.1.35717120.1": snmptest.Integer(10000),
		"1.3.6.1.6.3.1.1.4.1.0":                   snmptest.ObjectID("1.3.6.1.6.3.1.1.5.1"),
	})

	system := &api.System{Description: "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia."}

	if err := nokiaSROS.Hardware(client, system); err != nil {
		t.Fatal(err)
	}

	if system.SoftwareVersion != "20.10.R3" || system.SerialNumber != "NS1918F0123" || system.FirmwareVersion != "TiMOS-L-20.10.R3" {
		t.Fatalf("unexpected hardware details %+v", system)
	}

	resources := &api.Resources{}

	if err := nokiaSROS.Resources(client, resources); err != nil {
		t.Fatal(err)
	}

	if *resources.CPULoadPercent != 17 || *resources.MemoryTotalBytes != 4000000*1024 || *resources.MemoryUsedBytes != 1000000*1024 {
		t.Fatalf("unexpected resources cpu %v total %v used %v", *resources.CPULoadPercent, *resources.MemoryTotalBytes, *resources.MemoryUsedBytes)
	}

	readings, err := nokiaSROS.Optics(client, nil)

	if err != nil {
		t.Fatal(err)
	}

	expectLevels(t, readings, 35684352, []float64{-11.02}, []float64{-3.01}, nil, nil)
	expectLevels(t, readings, 35717120, []float64{0}, []float64{0}, nil, nil)
}

func TestNokiaEncapVLAN(t *testing.T) {
	for value, expected := range map[int64]int{
		0:                       -1,   // null
		3340:                    3340, // dot1q
		4095:                    -1,   // dot1q '*'
		1<<31 | 100:             -1,   // dot1q connection profile
		20<<16 | 300:            300,  // QinQ, the outer tag
		0x0FFF<<16 | 1<<15 | 40: -1,   // QinQ connection profile on the outer tag
		1<<31 | 0x0FFF:          -1,   // '*.null'
	} {
		vlan := -1

		if result := nokiaEncapVLAN(value); result != nil {
			vlan = *result
		}

		if vlan != expected {
			t.Errorf("%#x: expected VLAN %d, got %d", value, expected, vlan)
		}
	}
}

// TestNokiaBGPPeers reads tBgpPeerNgTable, whose index carries the peer address with its length
func TestNokiaBGPPeers(t *testing.T) {
	peers := nokiaBGPPeers(
		map[string]snmp.Row{
			"1.1.4.10.81.255.109": {6: snmptest.Integer(2), 7: snmptest.OctetString("KC"), 13: snmptest.OctetString(string([]byte{10, 81, 255, 110})), 59: snmptest.Integer(6), 65: snmptest.Gauge(65000), 66: snmptest.Gauge(65000)},
			"1.1.4.192.0.2.9":     {6: snmptest.Integer(1), 59: snmptest.Integer(1), 66: snmptest.Gauge(3356)},
		},
		map[string]snmp.Row{
			"1.1.4.10.81.255.109": {177: snmptest.Gauge(7200), 181: snmptest.IPAddress("100.78.0.4")},
		},
	)

	byAddress := make(map[string]api.BGPPeer)

	for _, peer := range peers {
		byAddress[peer.Address] = peer
	}

	kc := byAddress["10.81.255.109"]

	if kc.Description != "KC" || kc.RouterID != "100.78.0.4" || kc.LocalAddress != "10.81.255.110" || kc.State != api.BGPPeerStateEstablished || !kc.AdminEnabled || kc.RemoteAS != 65000 || kc.LocalAS == nil || *kc.LocalAS != 65000 || kc.EstablishedTimeSeconds == nil || *kc.EstablishedTimeSeconds != 7200 {
		t.Fatalf("unexpected session %+v", kc)
	}

	if shut := byAddress["192.0.2.9"]; shut.AdminEnabled || shut.State != api.BGPPeerStateIdle || shut.RemoteAS != 3356 || shut.EstablishedTimeSeconds != nil {
		t.Fatalf("unexpected shut down session %+v", shut)
	}
}
