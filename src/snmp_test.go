package main

import (
	"context"
	"main/src/api"
	"main/src/snmp/snmptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// collectFromAgent runs a discovery against a simulated device serving values on top of a system group and one
// interface (ifIndex 35684352)
func collectFromAgent(t *testing.T, objectID, description string, values map[string]gosnmp.SnmpPDU) *snmpData {
	t.Helper()

	data, _ := discoverFromAgent(t, objectID, description, values)

	return data
}

// discoverFromAgent is collectFromAgent also returning the simulated device, which records the requests it received
func discoverFromAgent(t *testing.T, objectID, description string, values map[string]gosnmp.SnmpPDU) (*snmpData, *snmptest.Agent) {
	t.Helper()

	values["1.3.6.1.2.1.1.1.0"] = snmptest.OctetString(description)
	values["1.3.6.1.2.1.1.2.0"] = snmptest.ObjectID(objectID)
	values["1.3.6.1.2.1.1.3.0"] = snmptest.TimeTicks(8640000)
	values["1.3.6.1.2.1.2.2.1.1.35684352"] = snmptest.Integer(35684352)
	values["1.3.6.1.2.1.2.2.1.2.35684352"] = snmptest.OctetString("1/1/1")
	values["1.3.6.1.2.1.31.1.1.1.1.35684352"] = snmptest.OctetString("1/1/1")

	conf.SNMP.Timeout = 2 * time.Second
	conf.SNMP.Retries = 0

	agent := snmptest.StartAgent(t, values)

	device := api.Device{
		ID:   "device",
		Name: "device",
		Host: "127.0.0.1",
		SNMP: api.DeviceSNMP{Port: agent.Port, Credentials: []api.SNMPCredential{{Version: api.SNMPVersion2C, Community: "public"}}},
	}

	data, err := CollectSNMPData(context.Background(), device, time.Now(), false, true)

	if err != nil {
		t.Fatal(err)
	}

	return data, agent
}

// TestCollectSNMPDataUsesVendorDefinitions checks every vendor hook is wired into a discovery, using Nokia SR OS, which
// has all of them
func TestCollectSNMPDataUsesVendorDefinitions(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.6527.1.3.17", "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia.", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.4.1.6527.3.1.2.1.1.1.0":                snmptest.Gauge(17),
		"1.3.6.1.4.1.6527.3.1.2.1.1.10.0":               snmptest.Gauge(3000),
		"1.3.6.1.4.1.6527.3.1.2.1.1.11.0":               snmptest.Gauge(1000),
		"1.3.6.1.4.1.6527.3.1.2.2.1.8.1.5.1.50331649":   snmptest.OctetString("NS1918F0123"),
		"1.3.6.1.4.1.6527.3.1.2.2.1.8.1.7.1.50331649":   snmptest.Integer(3),
		"1.3.6.1.4.1.6527.3.1.2.2.4.31.1.21.1.35684352": snmptest.Integer(790),
	})

	if system := data.System; system.Manufacturer != "Nokia" || system.Model != "7750 SR" || system.SerialNumber != "NS1918F0123" || system.SoftwareVersion != "20.10.R3" {
		t.Fatalf("expected the vendor's manufacturer, model and hardware hook, got %+v", system)
	}

	if data.Resources.CPULoadPercent == nil || *data.Resources.CPULoadPercent != 17 || data.Resources.MemoryTotalBytes == nil {
		t.Fatalf("expected the vendor's resources hook, got %+v", data.Resources)
	}

	if optical := data.Interfaces[0].Optical; optical == nil || optical.RxPowerDBm[0] != -11.02 {
		t.Fatalf("expected the vendor's optics hook, got %+v", optical)
	}
}

func TestCollectSNMPDataForUnknownVendors(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia.", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.4.1.6527.3.1.2.1.1.1.0": snmptest.Gauge(17),
	})

	if data.System.Manufacturer != "" || data.System.Model != "" || data.Resources.CPULoadPercent != nil {
		t.Fatalf("expected only the standard MIBs to be read, got %+v %+v", data.System, data.Resources)
	}
}

// TestCollectSNMPDataInventory lists the physical entities other than sensors that have a model or serial number or
// belong to an interface, and identifies the interface's transceiver from the innermost of them
func TestCollectSNMPDataInventory(t *testing.T) {
	data := collectFromAgent(t, "1.3.6.1.4.1.99999.1", "Switch", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.47.1.1.1.1.5.1":   snmptest.Integer(3),
		"1.3.6.1.2.1.47.1.1.1.1.7.1":   snmptest.OctetString("Chassis"),
		"1.3.6.1.2.1.47.1.1.1.1.11.1":  snmptest.OctetString("CHS123"),
		"1.3.6.1.2.1.47.1.1.1.1.13.1":  snmptest.OctetString("SW-48"),
		"1.3.6.1.2.1.47.1.1.1.1.4.2":   snmptest.Integer(1),
		"1.3.6.1.2.1.47.1.1.1.1.5.2":   snmptest.Integer(5),
		"1.3.6.1.2.1.47.1.1.1.1.7.2":   snmptest.OctetString("Slot 1"),
		"1.3.6.1.2.1.47.1.1.1.1.4.3":   snmptest.Integer(2),
		"1.3.6.1.2.1.47.1.1.1.1.5.3":   snmptest.Integer(10),
		"1.3.6.1.2.1.47.1.1.1.1.7.3":   snmptest.OctetString("Port 1/1/1"),
		"1.3.6.1.2.1.47.1.1.1.1.2.4":   snmptest.OctetString("SFP-10GBase-LR"),
		"1.3.6.1.2.1.47.1.1.1.1.4.4":   snmptest.Integer(3),
		"1.3.6.1.2.1.47.1.1.1.1.5.4":   snmptest.Integer(10),
		"1.3.6.1.2.1.47.1.1.1.1.7.4":   snmptest.OctetString("Transceiver 1/1/1"),
		"1.3.6.1.2.1.47.1.1.1.1.11.4":  snmptest.OctetString("FNS123"),
		"1.3.6.1.2.1.47.1.1.1.1.12.4":  snmptest.OctetString("FINISAR"),
		"1.3.6.1.2.1.47.1.1.1.1.13.4":  snmptest.OctetString("FTLX1471D3BCL"),
		"1.3.6.1.2.1.47.1.1.1.1.4.5":   snmptest.Integer(4),
		"1.3.6.1.2.1.47.1.1.1.1.5.5":   snmptest.Integer(8),
		"1.3.6.1.2.1.47.1.1.1.1.7.5":   snmptest.OctetString("Transceiver 1/1/1 Temperature"),
		"1.3.6.1.2.1.47.1.1.1.1.11.5":  snmptest.OctetString("SENSOR"),
		"1.3.6.1.2.1.47.1.3.2.1.2.3.0": snmptest.ObjectID(".1.3.6.1.2.1.2.2.1.1.35684352"),
		"1.3.6.1.2.1.47.1.1.1.1.4.6":   snmptest.Integer(1),
		"1.3.6.1.2.1.47.1.1.1.1.5.6":   snmptest.Integer(6),
		"1.3.6.1.2.1.47.1.1.1.1.7.6":   snmptest.OctetString("Power Supply 1"),
		"1.3.6.1.2.1.47.1.1.1.1.11.6":  snmptest.OctetString("PSU123"),
	})

	indexes := make([]int, len(data.Inventory))

	for i, item := range data.Inventory {
		indexes[i] = item.Index
	}

	if !slices.Equal(indexes, []int{1, 3, 4, 6}) {
		t.Fatalf("expected the chassis, port, transceiver and power supply, got %+v", data.Inventory)
	}

	if item := data.Inventory[2]; item.Class != api.InventoryClassPort || item.InterfaceIndex == nil || *item.InterfaceIndex != 35684352 || item.ParentIndex == nil || *item.ParentIndex != 3 {
		t.Fatalf("expected the transceiver to belong to its port's interface, got %+v", item)
	}

	if item := data.Inventory[3]; item.Class != api.InventoryClassPowerSupply || item.InterfaceIndex != nil {
		t.Fatalf("expected the power supply to belong to no interface, got %+v", item)
	}

	expected := api.Transceiver{Type: "SFP-10GBase-LR", Manufacturer: "FINISAR", Model: "FTLX1471D3BCL", SerialNumber: "FNS123"}

	if transceiver := data.Interfaces[0].Transceiver; transceiver == nil || *transceiver != expected {
		t.Fatalf("expected transceiver %+v, got %+v", expected, transceiver)
	}

	if data.System.Model != "SW-48" || data.System.SerialNumber != "CHS123" {
		t.Fatalf("expected the chassis hardware details, got %+v", data.System)
	}
}

// TestCollectSNMPDataKeepsToVendorLimits checks a TP-Link discovery names one object per request after the system
// group identified the vendor, and reads its definitions
func TestCollectSNMPDataKeepsToVendorLimits(t *testing.T) {
	data, agent := discoverFromAgent(t, "1.3.6.1.4.1.11863.5.143", "JetStream 24-Port 10GE SFP+ L2+ Managed Switch", map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.2.2.1.8.35684352":      snmptest.Integer(1),
		"1.3.6.1.4.1.11863.6.1.1.5.0":       snmptest.OctetString("SX3032F 1.0"),
		"1.3.6.1.4.1.11863.6.4.1.2.1.1.2.1": snmptest.Integer(41),
	})

	if data.System.Manufacturer != "TP-Link" || data.System.Model != "SX3032F" {
		t.Fatalf("expected the TP-Link definition, got %+v", data.System)
	}

	if data.Resources.MemoryUsedPercent == nil || *data.Resources.MemoryUsedPercent != 41 {
		t.Fatalf("expected the memory percentage, got %+v", data.Resources)
	}

	requests := agent.Requests()

	for _, request := range requests[1:] {
		if len(request.OIDs) > 1 {
			t.Fatalf("expected one object per request after the system group, got %+v", request)
		}
	}
}

// TestStatusPollReadsInterfaceFallbacksOnlyWhenNeeded checks ifTable's speed and 32-bit counters are only read for
// interfaces without the ifXTable ones
func TestStatusPollReadsInterfaceFallbacksOnlyWhenNeeded(t *testing.T) {
	values := map[string]gosnmp.SnmpPDU{}

	for _, index := range []string{"1", "2", "3", "4", "5"} {
		values["1.3.6.1.2.1.2.2.1.8."+index] = snmptest.Integer(1)
		values["1.3.6.1.2.1.2.2.1.5."+index] = snmptest.Gauge(100_000_000)
		values["1.3.6.1.2.1.2.2.1.10."+index] = snmptest.Gauge(10)
		values["1.3.6.1.2.1.2.2.1.16."+index] = snmptest.Gauge(20)
		values["1.3.6.1.2.1.31.1.1.1.1."+index] = snmptest.OctetString("port " + index)
	}

	// Interfaces 1 to 4 have 64-bit counters and a high speed; interface 5 only has the ifTable values
	for _, index := range []string{"1", "2", "3", "4"} {
		values["1.3.6.1.2.1.31.1.1.1.6."+index] = gosnmp.SnmpPDU{Type: gosnmp.Counter64, Value: uint64(1000)}
		values["1.3.6.1.2.1.31.1.1.1.10."+index] = gosnmp.SnmpPDU{Type: gosnmp.Counter64, Value: uint64(2000)}
		values["1.3.6.1.2.1.31.1.1.1.15."+index] = snmptest.Gauge(10_000)
	}

	agent := snmptest.StartAgent(t, values)

	interfaces, err := collectInterfaces(agent.Client(t), time.Now(), false)

	if err != nil {
		t.Fatal(err)
	}

	if len(interfaces) != 5 || interfaces[0].InOctets != 1000 || interfaces[0].SpeedBitsPerSecond != 10_000_000_000 {
		t.Fatalf("expected the ifXTable values, got %+v", interfaces)
	}

	if last := interfaces[4]; last.InOctets != 10 || last.OutOctets != 20 || last.SpeedBitsPerSecond != 100_000_000 {
		t.Fatalf("expected the ifTable values for the interface without ifXTable ones, got %+v", last)
	}

	for _, request := range agent.Requests() {
		for _, oid := range request.OIDs {
			if request.Type != gosnmp.GetRequest && (strings.HasPrefix(oid, "1.3.6.1.2.1.2.2.1.5") || strings.HasPrefix(oid, "1.3.6.1.2.1.2.2.1.10") || strings.HasPrefix(oid, "1.3.6.1.2.1.2.2.1.16")) {
				t.Fatalf("expected the fallback columns to be read with Get for one interface, not walked: %+v", request)
			}
		}
	}
}
