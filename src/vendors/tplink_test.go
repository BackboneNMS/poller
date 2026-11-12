package vendors

import (
	"main/src/api"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"math"
	"strconv"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestTPLinkModelAndRevision(t *testing.T) {
	for _, test := range []struct {
		hardwareVersion string
		model           string
		revision        string
	}{
		{"SX3032F 1.0", "SX3032F", "1.0"},
		{"T2600G-28TS 3.0", "T2600G-28TS", "3.0"},
		{"TL-SG3428X UPS 1.20", "TL-SG3428X UPS", "1.20"},
		{"3.0", "", "3.0"},
		{"SX3032F", "", "SX3032F"},
		{"", "", ""},
	} {
		model, revision := tpLinkModelAndRevision(test.hardwareVersion)

		if model != test.model || revision != test.revision {
			t.Errorf("%q: expected %q %q, got %q %q", test.hardwareVersion, test.model, test.revision, model, revision)
		}
	}
}

func TestApplyTPLinkHardwareKeepsEntityDetails(t *testing.T) {
	system := &api.System{Model: "Chassis Model", SerialNumber: "FROM-ENTITY-MIB"}

	applyTPLinkHardware(system, "SX3032F 1.0", "1.0.3 Build 20240510 Rel.66219", "2235123000123")

	if system.Model != "Chassis Model" || system.SerialNumber != "FROM-ENTITY-MIB" || system.HardwareVersion != "1.0" || system.SoftwareVersion != "1.0.3 Build 20240510 Rel.66219" {
		t.Fatalf("expected only missing details to be filled in, got %+v", system)
	}
}

func TestTPLinkPowerDBm(t *testing.T) {
	for text, expected := range map[string]float64{
		"1.0000":    0,
		"0.5012":    -3,
		"0.1":       -10,
		"-2.34 dBm": -2.34,
		"-7.5dBm":   -7.5,
	} {
		if value, ok := tpLinkPowerDBm(text); !ok || math.Abs(value-expected) > 0.01 {
			t.Errorf("%q: expected %v dBm, got %v (ok %v)", text, expected, value, ok)
		}
	}

	for _, text := range []string{"", "--", "N/A"} {
		if _, ok := tpLinkPowerDBm(text); ok {
			t.Errorf("%q: expected no reading", text)
		}
	}
}

func TestTPLinkHooks(t *testing.T) {
	ddm := func(column int, ifIndex string) string {
		return oidTPLinkDDMStatusEntry + "." + strconv.Itoa(column) + "." + ifIndex
	}

	agent := snmptest.StartAgent(t, map[string]gosnmp.SnmpPDU{
		oidTPLinkHardwareVersion:                     snmptest.OctetString("SX3032F 1.0"),
		oidTPLinkSoftwareVersion:                     snmptest.OctetString("1.0.3 Build 20240510 Rel.66219"),
		oidTPLinkSerialNumber:                        snmptest.OctetString("2235123000123"),
		oidTPLinkCPUEntry + ".3.1":                   snmptest.Integer(12),
		oidTPLinkCPUEntry + ".3.2":                   snmptest.Integer(18),
		oidTPLinkMemoryEntry + ".2.1":                snmptest.Integer(41),
		ddm(tpLinkDDMTemperatureColumn, "49153"):     snmptest.OctetString("35.5"),
		ddm(tpLinkDDMVoltageColumn, "49153"):         snmptest.OctetString("3.29"),
		ddm(tpLinkDDMBiasCurrentColumn, "49153"):     snmptest.OctetString("6.4"),
		ddm(tpLinkDDMTransmitPowerColumn, "49153"):   snmptest.OctetString("0.5012"),
		ddm(tpLinkDDMReceivePowerColumn, "49153"):    snmptest.OctetString("0.1000"),
		ddm(tpLinkDDMTemperatureColumn, "49154"):     snmptest.OctetString("--"),
		ddm(tpLinkDDMTransmitPowerColumn, "49154"):   snmptest.OctetString("--"),
		ddm(tpLinkDDMReceivePowerColumn, "49154"):    snmptest.OctetString("--"),
		oidTPLinkReceiveThresholdEntry + ".2.49153":  snmptest.OctetString("1.0000"),
		oidTPLinkReceiveThresholdEntry + ".3.49153":  snmptest.OctetString("0.0100"),
		oidTPLinkTransmitThresholdEntry + ".2.49153": snmptest.OctetString("1.0000"),
		oidTPLinkTransmitThresholdEntry + ".3.49153": snmptest.OctetString("0.1000"),
		oidTPLinkTransmitThresholdEntry + ".2.49154": snmptest.OctetString("1.0000"),
		oidTPLinkTransmitThresholdEntry + ".3.49154": snmptest.OctetString("0.1000"),
		oidTPLinkDDMStatusEntry + ".7.49153":         snmptest.OctetString("Yes"),
		oidTPLinkMemoryEntry + ".3.1":                snmptest.Integer(99),
		"1.3.6.1.4.1.11863.6.96.1.8.1":               snmptest.OctetString("after the DDM tables"),
	})

	client := agent.Client(t)

	snmp.ApplyLimits(client, tpLink.Limits())

	system := &api.System{}

	if err := tpLink.Hardware(client, system); err != nil {
		t.Fatal(err)
	}

	if system.Model != "SX3032F" || system.HardwareVersion != "1.0" || system.SerialNumber != "2235123000123" || system.SoftwareVersion != "1.0.3 Build 20240510 Rel.66219" {
		t.Fatalf("unexpected hardware details %+v", system)
	}

	resources := &api.Resources{}

	if err := tpLink.Resources(client, resources); err != nil {
		t.Fatal(err)
	}

	if resources.CPULoadPercent == nil || *resources.CPULoadPercent != 15 || resources.MemoryUsedPercent == nil || *resources.MemoryUsedPercent != 41 || resources.MemoryTotalBytes != nil {
		t.Fatalf("expected the averaged 1-minute CPU load and memory percentage, got %+v", resources)
	}

	readings, err := tpLink.Optics(client, nil)

	if err != nil {
		t.Fatal(err)
	}

	if readings.Count() != 1 || readings.Has(49154) {
		t.Fatalf("expected readings for the port with a transceiver only, got %d", readings.Count())
	}

	expectLevels(t, readings, 49153, []float64{-10}, []float64{-3.0}, &api.OpticalThresholds{LowDBm: -20, HighDBm: 0}, &api.OpticalThresholds{LowDBm: -10, HighDBm: 0})
	expectDiagnostics(t, readings, 49153, floatPointer(35.5), floatPointer(3.29), []float64{6.4})

	for _, request := range agent.Requests() {
		if len(request.OIDs) > tpLinkMaxOIDs || request.MaxRepetitions > tpLinkMaxRepetitions {
			t.Fatalf("expected every request to keep to TP-Link's limits, got %+v", request)
		}
	}
}
