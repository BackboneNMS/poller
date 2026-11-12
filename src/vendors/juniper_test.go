package vendors

import (
	"main/src/api"
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"testing"
)

func TestJuniperReadings(t *testing.T) {
	readings := juniperReadings(
		map[string]snmp.Row{
			"10": {5: snmptest.Integer(-312), 7: snmptest.Integer(-158), 9: snmptest.Integer(50), 10: snmptest.Integer(-1440), 17: snmptest.Integer(0), 18: snmptest.Integer(0)},
			"11": {5: snmptest.Integer(-999), 7: snmptest.Integer(-999), 9: snmptest.Integer(450), 10: snmptest.Integer(-1060), 17: snmptest.Integer(500), 18: snmptest.Integer(-940)},
			"14": {5: snmptest.Integer(-5000), 7: snmptest.Integer(50)},
		},
		map[string]snmp.Row{
			"11.1": {6: snmptest.Integer(-210), 8: snmptest.Integer(105)},
			"11.0": {6: snmptest.Integer(-205), 8: snmptest.Integer(110)},
		},
	)

	expectLevels(t, readings, 10, []float64{-3.12}, []float64{-1.58}, &api.OpticalThresholds{LowDBm: -14.4, HighDBm: 0.5}, nil)
	expectLevels(t, readings, 11, []float64{-2.05, -2.1}, []float64{1.1, 1.05}, &api.OpticalThresholds{LowDBm: -10.6, HighDBm: 4.5}, &api.OpticalThresholds{LowDBm: -9.4, HighDBm: 5})
	expectLevels(t, readings, 14, []float64{-40}, []float64{0.5}, nil, nil)
}
