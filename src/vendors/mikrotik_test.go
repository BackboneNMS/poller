package vendors

import (
	"main/src/api"
	"main/src/optics"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"testing"
)

func TestMikroTikReadings(t *testing.T) {
	readings := mikroTikReadings(
		map[string]snmp.Row{
			"3": {
				2:  snmptest.OctetString("sfp-sfpplus1"),
				5:  snmptest.Gauge(131000),
				6:  snmptest.Gauge(38),
				7:  snmptest.Gauge(3287),
				8:  snmptest.Gauge(24),
				9:  snmptest.Integer(-2104),
				10: snmptest.Integer(-6532),
				11: snmptest.OctetString("FINISAR CORP."),
				12: snmptest.OctetString("UW30DBL"),
				13: snmptest.Integer(1),
				14: snmptest.OctetString("FTLX1471D3BCL"),
				15: snmptest.Integer(3),
			},
			"4": {2: snmptest.OctetString("sfp-sfpplus2"), 9: snmptest.Integer(0), 10: snmptest.Integer(0), 13: snmptest.Integer(0)},
		},
		[]api.Interface{{Index: 1, Name: "ether1"}, {Index: 12, Name: "sfp-sfpplus1"}, {Index: 13, Name: "sfp-sfpplus2"}},
	)

	expectLevels(t, readings, 12, []float64{-6.53}, []float64{-2.1}, nil, nil)
	expectDiagnostics(t, readings, 12, floatPointer(38.0), floatPointer(3.287), []float64{24})

	interfaces := []api.Interface{{Index: 12}, {Index: 13}}

	optics.Apply(interfaces, readings)

	expected := api.Transceiver{Type: "SFP", Manufacturer: "FINISAR CORP.", Model: "FTLX1471D3BCL", SerialNumber: "UW30DBL", WavelengthNM: floatPointer(1310.0)}

	if got := interfaces[0].Transceiver; got == nil || got.Type != expected.Type || got.Manufacturer != expected.Manufacturer || got.Model != expected.Model || got.SerialNumber != expected.SerialNumber || got.WavelengthNM == nil || *got.WavelengthNM != *expected.WavelengthNM {
		t.Fatalf("expected transceiver %+v, got %+v", expected, got)
	}

	if interfaces[1].Optical != nil || interfaces[1].Transceiver != nil {
		t.Fatalf("expected the empty cage to be skipped, got %+v %+v", interfaces[1].Optical, interfaces[1].Transceiver)
	}
}
