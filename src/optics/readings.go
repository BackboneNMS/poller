// Package optics collects transceiver light levels: the readings vendor definitions and sensor tables add to, and the
// generic ENTITY-SENSOR-MIB reader used for every vendor without its own optics table.
package optics

import (
	"main/src/api"
	"math"
	"slices"
	"strings"
)

// NoLightDBm is the floor for reported light levels; transceivers report anything from -40 dBm to -inf without light
const NoLightDBm = -40.0

// Readings holds light levels by interface index, then by lane (0 when the optic has a single lane)
type Readings map[int]*lanes

type lanes struct {
	receive            map[int]float64
	transmit           map[int]float64
	receiveThresholds  *api.OpticalThresholds
	transmitThresholds *api.OpticalThresholds
}

// Add records the receive or transmit level of one lane of an interface, in dBm
func (r Readings) Add(interfaceIndex, lane int, receive bool, dBm float64) {
	if r[interfaceIndex] == nil {
		r[interfaceIndex] = &lanes{receive: make(map[int]float64), transmit: make(map[int]float64)}
	}

	value := roundDBm(math.Max(dBm, NoLightDBm))

	if receive {
		r[interfaceIndex].receive[lane] = value
	} else {
		r[interfaceIndex].transmit[lane] = value
	}
}

// SetThresholds records the alarm thresholds of an interface that has light levels. Empty ranges are ignored, since
// that is how most optics without thresholds report them (as zeros).
func (r Readings) SetThresholds(interfaceIndex int, receive bool, lowDBm, highDBm float64) {
	lanes := r[interfaceIndex]

	if lanes == nil || lowDBm >= highDBm {
		return
	}

	thresholds := &api.OpticalThresholds{LowDBm: roundDBm(lowDBm), HighDBm: roundDBm(highDBm)}

	if receive {
		lanes.receiveThresholds = thresholds
	} else {
		lanes.transmitThresholds = thresholds
	}
}

// Apply sets the optical details of the interfaces that have readings
func Apply(interfaces []api.Interface, readings Readings) {
	for i := range interfaces {
		lanes := readings[interfaces[i].Index]

		if lanes == nil {
			continue
		}

		interfaces[i].Optical = &api.Optical{
			RxPowerDBm:   laneValues(lanes.receive),
			TxPowerDBm:   laneValues(lanes.transmit),
			RxThresholds: lanes.receiveThresholds,
			TxThresholds: lanes.transmitThresholds,
		}
	}
}

// MilliwattsToDBm converts optical power to dBm. No light at all reads as NoLightDBm.
func MilliwattsToDBm(milliwatts float64) float64 {
	if milliwatts <= 0 {
		return NoLightDBm
	}

	return 10 * math.Log10(milliwatts)
}

// FindInterfaceByName returns the interface whose name or description is name, ignoring case
func FindInterfaceByName(interfaces []api.Interface, name string) *api.Interface {
	if len(name) < 1 {
		return nil
	}

	for i := range interfaces {
		if strings.EqualFold(interfaces[i].Name, name) || strings.EqualFold(interfaces[i].Description, name) {
			return &interfaces[i]
		}
	}

	return nil
}

func roundDBm(value float64) float64 {
	return math.Round(value*100) / 100
}

func laneValues(values map[int]float64) []float64 {
	lanes := make([]int, 0, len(values))

	for lane := range values {
		lanes = append(lanes, lane)
	}

	slices.Sort(lanes)

	result := make([]float64, len(lanes))

	for i, lane := range lanes {
		result[i] = values[lane]
	}

	return result
}
