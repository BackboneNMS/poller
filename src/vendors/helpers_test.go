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
