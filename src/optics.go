package main

import (
	"main/src/api"
	"main/src/entity"
	"main/src/optics"
	"main/src/vendors"
	"slices"

	"github.com/gosnmp/gosnmp"
)

// collectOptics adds transceiver diagnostics and details to the interfaces they belong to: from the vendor's own
// tables when it has them, otherwise from its variants of ENTITY-SENSOR-MIB and then the standard one, traced through
// the device's physical entities back to an interface
func collectOptics(client *gosnmp.GoSNMP, vendor *vendors.Vendor, entities *entity.Table, interfaces []api.Interface) error {
	if vendor.Optics != nil {
		readings, err := vendor.Optics(client, interfaces)

		if err != nil {
			return err
		}

		if readings.Count() > 0 {
			optics.Apply(interfaces, readings)

			return nil
		}
	}

	readings, err := optics.ReadSensors(client, slices.Concat(vendor.SensorTables, []optics.SensorTable{optics.StandardSensors}), entities, interfaces)

	optics.Apply(interfaces, readings)

	return err
}
