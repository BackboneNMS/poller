// Package optics collects transceiver diagnostics (light levels, temperature, supply voltage and laser bias current)
// and details: the readings vendor definitions and sensor tables add to, and the generic ENTITY-SENSOR-MIB reader used
// for every vendor without its own optics table.
package optics

import (
	"main/src/api"
	"math"
	"slices"
)

// NoLightDBm is the floor for reported light levels; transceivers report anything from -40 dBm to -inf without light
const NoLightDBm = -40.0

// Readings holds transceiver readings by interface index. Values that differ per lane are kept by lane (0 when the
// optic has a single lane).
type Readings map[int]*transceiverReadings

type transceiverReadings struct {
	receive            map[int]float64
	transmit           map[int]float64
	biasCurrent        map[int]float64
	receiveThresholds  *api.OpticalThresholds
	transmitThresholds *api.OpticalThresholds
	temperature        *float64
	voltage            *float64
	details            *api.Transceiver
}

func (r Readings) entry(interfaceIndex int) *transceiverReadings {
	if r[interfaceIndex] == nil {
		r[interfaceIndex] = &transceiverReadings{receive: make(map[int]float64), transmit: make(map[int]float64), biasCurrent: make(map[int]float64)}
	}

	return r[interfaceIndex]
}

// Add records the receive or transmit level of one lane of an interface, in dBm
func (r Readings) Add(interfaceIndex, lane int, receive bool, dBm float64) {
	value := roundTo(math.Max(dBm, NoLightDBm), 2)

	if receive {
		r.entry(interfaceIndex).receive[lane] = value
	} else {
		r.entry(interfaceIndex).transmit[lane] = value
	}
}

// SetThresholds records the alarm thresholds of an interface that has light levels. Empty ranges are ignored, since
// that is how most optics without thresholds report them (as zeros).
func (r Readings) SetThresholds(interfaceIndex int, receive bool, lowDBm, highDBm float64) {
	readings := r[interfaceIndex]

	if readings == nil || lowDBm >= highDBm {
		return
	}

	thresholds := &api.OpticalThresholds{LowDBm: roundTo(lowDBm, 2), HighDBm: roundTo(highDBm, 2)}

	if receive {
		readings.receiveThresholds = thresholds
	} else {
		readings.transmitThresholds = thresholds
	}
}

// AddBiasCurrent records the laser bias current of one lane of an interface, in milliamps
func (r Readings) AddBiasCurrent(interfaceIndex, lane int, milliamps float64) {
	r.entry(interfaceIndex).biasCurrent[lane] = roundTo(milliamps, 2)
}

// SetTemperature records the temperature of an interface's transceiver, in degrees Celsius
func (r Readings) SetTemperature(interfaceIndex int, celsius float64) {
	value := roundTo(celsius, 1)
	r.entry(interfaceIndex).temperature = &value
}

// SetVoltage records the supply voltage of an interface's transceiver, in volts. Zero is ignored, since optics that
// do not measure their supply report it that way.
func (r Readings) SetVoltage(interfaceIndex int, volts float64) {
	if volts <= 0 {
		return
	}

	value := roundTo(volts, 3)
	r.entry(interfaceIndex).voltage = &value
}

// SetDetails records what transceiver is plugged into an interface. Details without anything identifying the
// transceiver are ignored.
func (r Readings) SetDetails(interfaceIndex int, details api.Transceiver) {
	if len(details.Type) > 0 || len(details.Manufacturer) > 0 || len(details.Model) > 0 || len(details.SerialNumber) > 0 {
		r.entry(interfaceIndex).details = &details
	}
}

// Has reports whether an interface has light levels
func (r Readings) Has(interfaceIndex int) bool {
	readings := r[interfaceIndex]

	return readings != nil && len(readings.receive)+len(readings.transmit) > 0
}

// Count is how many interfaces have light levels or transceiver details
func (r Readings) Count() int {
	count := 0

	for interfaceIndex, readings := range r {
		if r.Has(interfaceIndex) || readings.details != nil {
			count++
		}
	}

	return count
}

// Apply sets the transceiver details and diagnostics of the interfaces that have readings. Diagnostics are only set on
// interfaces with light levels, which every optic with diagnostics reports.
func Apply(interfaces []api.Interface, readings Readings) {
	for i := range interfaces {
		item := readings[interfaces[i].Index]

		if item == nil {
			continue
		}

		if item.details != nil {
			interfaces[i].Transceiver = item.details
		}

		if !readings.Has(interfaces[i].Index) {
			continue
		}

		interfaces[i].Optical = &api.Optical{
			RxPowerDBm:         laneValues(item.receive),
			TxPowerDBm:         laneValues(item.transmit),
			RxThresholds:       item.receiveThresholds,
			TxThresholds:       item.transmitThresholds,
			TemperatureCelsius: item.temperature,
			VoltageVolts:       item.voltage,
			BiasCurrentMA:      laneValues(item.biasCurrent),
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

func roundTo(value float64, decimals int) float64 {
	scale := math.Pow(10, float64(decimals))

	return math.Round(value*scale) / scale
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
