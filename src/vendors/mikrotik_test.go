package vendors

import (
	"main/src/api"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"testing"
)

func TestMikroTikReadings(t *testing.T) {
	readings := mikroTikReadings(
		map[string]snmp.Row{"3": {2: snmptest.OctetString("sfp-sfpplus1"), 9: snmptest.Integer(-2104), 10: snmptest.Integer(-6532)}},
		[]api.Interface{{Index: 1, Name: "ether1"}, {Index: 12, Name: "sfp-sfpplus1"}},
	)

	expectLevels(t, readings, 12, []float64{-6.53}, []float64{-2.1}, nil, nil)
}
