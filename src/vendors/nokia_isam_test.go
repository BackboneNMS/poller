package vendors

import (
	"main/src/api"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestApplyISAMHolder(t *testing.T) {
	system := &api.System{}

	applyISAMHolder(system, map[string]snmp.Row{
		"1":  {4: snmptest.OctetString("EMPTY")},
		"17": {4: snmptest.OctetString("NFXS-E"), 13: snmptest.OctetString("YP1234567890")},
	})

	if system.Model != "NFXS-E" || system.SerialNumber != "YP1234567890" {
		t.Fatalf("unexpected main holder details %+v", system)
	}

	fallback := &api.System{}

	applyISAMHolder(fallback, map[string]snmp.Row{
		"3": {4: snmptest.OctetString("UNKNOWN"), 13: snmptest.OctetString("AB123")},
		"2": {4: snmptest.OctetString("NFXS-F"), 13: snmptest.OctetString("CD456")},
	})

	if fallback.Model != "NFXS-F" || fallback.SerialNumber != "CD456" {
		t.Fatalf("expected the first holder with a serial number, got %+v", fallback)
	}
}

func TestApplyISAMResources(t *testing.T) {
	resources := &api.Resources{}

	applyISAMResources(
		resources,
		map[string]snmp.Row{"4352": {4: snmptest.Gauge(30)}, "4353": {4: snmptest.Gauge(10)}},
		map[string]snmp.Row{"4352": {1: snmptest.Gauge(2048), 2: snmptest.Gauge(1024)}, "4353": {1: snmptest.Gauge(1024), 2: snmptest.Gauge(512)}, "4354": {1: snmptest.Gauge(0), 2: snmptest.Gauge(0)}},
	)

	if *resources.CPULoadPercent != 20 || *resources.MemoryTotalBytes != 3072*1024*1024 || *resources.MemoryUsedBytes != 1536*1024*1024 {
		t.Fatalf("unexpected ISAM resources cpu %v total %v used %v", *resources.CPULoadPercent, *resources.MemoryTotalBytes, *resources.MemoryUsedBytes)
	}

	kept := &api.Resources{}
	percent := 5.0
	kept.CPULoadPercent = &percent

	applyISAMResources(kept, map[string]snmp.Row{"1": {4: snmptest.Gauge(90)}}, nil)

	if *kept.CPULoadPercent != 5 {
		t.Fatal("expected a CPU load from HOST-RESOURCES-MIB to be kept")
	}
}

// TestNokiaISAMHooks runs the ISAM hooks against a simulated 7360 FX
func TestNokiaISAMHooks(t *testing.T) {
	client := snmptest.NewClient(t, map[string]gosnmp.SnmpPDU{
		oidISAMSoftwareVersion:              snmptest.OctetString("R6.2.04"),
		oidISAMCPULoadEntry + ".4.4352":     snmptest.Gauge(40),
		oidISAMCPULoadEntry + ".4.4353":     snmptest.Gauge(20),
		oidISAMMemoryUsageEntry + ".1.4352": snmptest.Gauge(2048),
		oidISAMMemoryUsageEntry + ".2.4352": snmptest.Gauge(1024),
		oidISAMHolderEntry + ".4.17":        snmptest.OctetString("NFXS-E"),
		oidISAMHolderEntry + ".13.17":       snmptest.OctetString("YP1234567890"),
		"1.3.6.1.6.3.1.1.4.1.0":             snmptest.ObjectID("1.3.6.1.6.3.1.1.5.1"),
	})

	system := &api.System{}

	if err := nokiaISAM.Hardware(client, system); err != nil {
		t.Fatal(err)
	}

	if system.Model != "NFXS-E" || system.SerialNumber != "YP1234567890" || system.SoftwareVersion != "R6.2.04" {
		t.Fatalf("unexpected hardware details %+v", system)
	}

	resources := &api.Resources{}

	if err := nokiaISAM.Resources(client, resources); err != nil {
		t.Fatal(err)
	}

	if *resources.CPULoadPercent != 30 || *resources.MemoryTotalBytes != 2048*1024*1024 || *resources.MemoryUsedBytes != 1024*1024*1024 {
		t.Fatalf("unexpected resources %+v", resources)
	}
}
