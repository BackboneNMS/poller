package vendors

import "main/src/optics"

// cisco reads light levels from CISCO-ENTITY-SENSOR-MIB, Cisco's variant of ENTITY-SENSOR-MIB, which reports dBm
// directly, says which entity (the port) a sensor measures, and has alarm thresholds
var cisco = &Vendor{
	Name:              "Cisco",
	EnterpriseNumbers: []string{"9"},
	SensorTables: []optics.SensorTable{
		{
			EntryOID:             "1.3.6.1.4.1.9.9.91.1.1.1.1",
			DBmType:              14,
			MeasuredEntityColumn: 8,
			ThresholdEntryOID:    "1.3.6.1.4.1.9.9.91.1.2.1.1",
		},
	},
}
