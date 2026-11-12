package vendors

import (
	"main/src/api"
	"main/src/optics"
	"slices"
	"testing"
)

// expectLevels checks the light levels readings give an interface, with its thresholds when they are not nil
func expectLevels(t *testing.T, readings optics.Readings, index int, receive, transmit []float64, receiveThresholds, transmitThresholds *api.OpticalThresholds) {
	t.Helper()

	interfaces := []api.Interface{{Index: index}}

	optics.Apply(interfaces, readings)

	optical := interfaces[0].Optical

	if optical == nil {
		t.Fatalf("interface %d: expected optical levels, got none", index)
	}

	if !slices.Equal(optical.RxPowerDBm, receive) || !slices.Equal(optical.TxPowerDBm, transmit) {
		t.Fatalf("interface %d: expected rx %v tx %v, got rx %v tx %v", index, receive, transmit, optical.RxPowerDBm, optical.TxPowerDBm)
	}

	equal := func(a, b *api.OpticalThresholds) bool {
		return a == nil && b == nil || a != nil && b != nil && *a == *b
	}

	if !equal(optical.RxThresholds, receiveThresholds) || !equal(optical.TxThresholds, transmitThresholds) {
		t.Fatalf("interface %d: expected thresholds rx %+v tx %+v, got rx %+v tx %+v", index, receiveThresholds, transmitThresholds, optical.RxThresholds, optical.TxThresholds)
	}
}

// expectDiagnostics checks the temperature, supply voltage and bias current readings give an interface
func expectDiagnostics(t *testing.T, readings optics.Readings, index int, temperature, voltage *float64, biasCurrent []float64) {
	t.Helper()

	interfaces := []api.Interface{{Index: index}}

	optics.Apply(interfaces, readings)

	optical := interfaces[0].Optical

	if optical == nil {
		t.Fatalf("interface %d: expected diagnostics, got none", index)
	}

	equal := func(a, b *float64) bool {
		return a == nil && b == nil || a != nil && b != nil && *a == *b
	}

	if !equal(optical.TemperatureCelsius, temperature) || !equal(optical.VoltageVolts, voltage) || !slices.Equal(optical.BiasCurrentMA, biasCurrent) {
		t.Fatalf("interface %d: expected temperature %v voltage %v bias %v, got %v %v %v", index, temperature, voltage, biasCurrent, optical.TemperatureCelsius, optical.VoltageVolts, optical.BiasCurrentMA)
	}
}

func floatPointer(value float64) *float64 {
	return &value
}
