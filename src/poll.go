package main

import (
	"context"
	"main/src/api"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	submitBatchSize = 50
	submitWorkers   = 4
	maxErrorLength  = 2000
)

type counterSample struct {
	polledAt      time.Time
	uptimeSeconds int64
	octets        map[int][2]int64
}

var (
	previousSamples   = make(map[string]counterSample)
	previousSamplesMu sync.Mutex

	// unreachableDevices holds the devices whose last poll got no SNMP response
	unreachableDevices   = make(map[string]struct{})
	unreachableDevicesMu sync.Mutex

	// deviceLocks keeps a status poll and a discovery of the same device from running at the same time
	deviceLocks sync.Map

	// pollInterval is the organization's polling interval from the last device list, used by discoveries for rates
	pollInterval atomic.Int64
)

func deviceLock(deviceID string) *sync.Mutex {
	lock, _ := deviceLocks.LoadOrStore(deviceID, &sync.Mutex{})

	return lock.(*sync.Mutex)
}

func currentPollInterval() time.Duration {
	return time.Duration(pollInterval.Load())
}

func PollAllDevices(ctx context.Context) time.Duration {
	startedAt := time.Now()

	list, err := client.FetchDevices(ctx)

	if err != nil {
		logger.Error("Failed to fetch device list from polling server", "error", err)

		return retryDelay
	}

	interval := time.Duration(list.PollInterval) * time.Second

	pollInterval.Store(int64(interval))
	pruneDeviceState(list.Devices)

	queued := discoveries.Schedule(list.Devices, startedAt)
	backupsQueued := configBackups.Schedule(list.Devices, startedAt)

	var (
		wg        sync.WaitGroup
		submitted sync.WaitGroup
		failed    atomic.Int64
		skipped   atomic.Int64
		slots     = make(chan struct{}, conf.SNMP.Concurrency)
		results   = make(chan api.PollResult, submitBatchSize)
	)

	for range submitWorkers {
		submitted.Go(func() { submitResults(ctx, results) })
	}

	devices := prioritizeDevices(list.Devices)

	// Slots are taken here rather than inside each goroutine so devices start polling in the order of the list, with
	// priority devices first. The rest fill any free slots straight away instead of waiting for them to finish.
dispatch:
	for _, device := range devices {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}

		wg.Go(func() {
			var (
				result api.PollResult
				polled bool
			)

			// A device being discovered right now is skipped, since its discovery reports everything a status poll would
			if lock := deviceLock(device.ID); lock.TryLock() {
				RunRecovered("PollDevice", func() {
					defer lock.Unlock()

					result = PollDevice(ctx, device, interval, false)
					polled = true
				})
			} else {
				skipped.Add(1)
			}

			// The slot is released before queuing the result, so a slow upload never holds up polling
			<-slots

			if !polled || ctx.Err() != nil {
				return
			}

			if !result.Reachable && !(device.PingOnly && result.Online) {
				failed.Add(1)
			}

			results <- result
		})
	}

	wg.Wait()
	close(results)
	submitted.Wait()

	logger.Info(
		"Completed poll cycle",
		"devices", len(list.Devices),
		"prioritized", countPriorityDevices(devices),
		"unreachable", failed.Load(),
		"skippedWhileDiscovering", skipped.Load(),
		"discoveriesQueued", queued,
		"configBackupsQueued", backupsQueued,
		"duration", time.Since(startedAt).Round(time.Millisecond).String(),
		"interval", interval.String(),
	)

	return interval
}

func submitResults(ctx context.Context, results <-chan api.PollResult) {
	batch := make([]api.PollResult, 0, submitBatchSize)

	flush := func() {
		if len(batch) < 1 {
			return
		}

		if err := client.SubmitResults(ctx, batch); err != nil {
			logger.Error("Failed to submit poll results to polling server", "count", len(batch), "error", err)
		}

		batch = batch[:0]
	}

	for result := range results {
		batch = append(batch, result)

		if len(batch) >= submitBatchSize {
			flush()
		}
	}

	flush()
}

// PollDevice pings a device and polls it over SNMP, either a status poll or a full discovery
func PollDevice(ctx context.Context, device api.Device, interval time.Duration, discover bool) api.PollResult {
	polledAt := time.Now()

	online, latency := PingHost(ctx, device.Host)

	if device.PingOnly {
		return pingOnlyResult(device, polledAt, online, latency)
	}

	data, pollErr := CollectSNMPData(ctx, device, polledAt, wasUnreachable(device.ID), discover)

	setUnreachable(device.ID, pollErr != nil)

	result := api.PollResult{
		DeviceID:       device.ID,
		Discovery:      discover,
		PolledAt:       polledAt,
		PollDurationMS: time.Since(polledAt).Milliseconds(),
		Online:         online,
		Reachable:      pollErr == nil,
		LatencyMS:      latency,
		Interfaces:     make([]api.Interface, 0),
		Neighbors:      make([]api.Neighbor, 0),
	}

	if pollErr != nil {
		result.Error = PointerOf(TruncateString(pollErr.Error(), maxErrorLength))

		return result
	}

	result.Online = true
	result.CredentialID = data.CredentialID
	result.System = data.System
	result.Interfaces = data.Interfaces
	result.Neighbors = data.Neighbors
	result.Resources = data.Resources

	calculateInterfaceRates(&result, interval)

	return result
}

// pingOnlyResult reports a device that is monitored by ping alone. It is never reachable over SNMP, so the polling
// server decides its status from Online, and Error is only set when it did not answer.
func pingOnlyResult(device api.Device, polledAt time.Time, online bool, latency *float64) api.PollResult {
	result := api.PollResult{
		DeviceID:       device.ID,
		PolledAt:       polledAt,
		PollDurationMS: time.Since(polledAt).Milliseconds(),
		Online:         online,
		LatencyMS:      latency,
		Interfaces:     make([]api.Interface, 0),
		Neighbors:      make([]api.Neighbor, 0),
	}

	if !online {
		result.Error = PointerOf("No response to ping")
	}

	return result
}

// prioritizeDevices orders a device list for polling: devices the polling server marked as priority (down or unknown)
// first, then the rest, each group keeping the server's order
func prioritizeDevices(devices []api.Device) []api.Device {
	ordered := slices.Clone(devices)

	slices.SortStableFunc(ordered, func(a, b api.Device) int {
		switch {
		case a.Priority == b.Priority:
			return 0
		case a.Priority:
			return -1
		default:
			return 1
		}
	})

	return ordered
}

func countPriorityDevices(devices []api.Device) int {
	count := 0

	for _, device := range devices {
		if device.Priority {
			count++
		}
	}

	return count
}

func pruneDeviceState(devices []api.Device) {
	active := make(map[string]struct{}, len(devices))

	for _, device := range devices {
		active[device.ID] = struct{}{}
	}

	previousSamplesMu.Lock()

	for id := range previousSamples {
		if _, ok := active[id]; !ok {
			delete(previousSamples, id)
		}
	}

	previousSamplesMu.Unlock()

	unreachableDevicesMu.Lock()

	for id := range unreachableDevices {
		if _, ok := active[id]; !ok {
			delete(unreachableDevices, id)
		}
	}

	unreachableDevicesMu.Unlock()

	deviceLocks.Range(func(id, lock any) bool {
		if _, ok := active[id.(string)]; !ok && lock.(*sync.Mutex).TryLock() {
			deviceLocks.Delete(id)
		}

		return true
	})
}

func wasUnreachable(deviceID string) bool {
	unreachableDevicesMu.Lock()
	defer unreachableDevicesMu.Unlock()

	_, ok := unreachableDevices[deviceID]

	return ok
}

func setUnreachable(deviceID string, unreachable bool) {
	unreachableDevicesMu.Lock()
	defer unreachableDevicesMu.Unlock()

	if unreachable {
		unreachableDevices[deviceID] = struct{}{}
	} else {
		delete(unreachableDevices, deviceID)
	}
}

func calculateInterfaceRates(result *api.PollResult, interval time.Duration) {
	current := counterSample{
		polledAt:      result.PolledAt,
		uptimeSeconds: result.System.UptimeSeconds,
		octets:        make(map[int][2]int64, len(result.Interfaces)),
	}

	for _, item := range result.Interfaces {
		current.octets[item.Index] = [2]int64{item.InOctets, item.OutOctets}
	}

	previousSamplesMu.Lock()
	previous, ok := previousSamples[result.DeviceID]
	previousSamples[result.DeviceID] = current
	previousSamplesMu.Unlock()

	if !ok || current.uptimeSeconds < previous.uptimeSeconds {
		return
	}

	elapsed := current.polledAt.Sub(previous.polledAt)

	if elapsed <= 0 || elapsed > interval*3 {
		return
	}

	for i := range result.Interfaces {
		item := &result.Interfaces[i]

		before, ok := previous.octets[item.Index]

		if !ok {
			continue
		}

		item.InBitsPerSecond = bitRate(before[0], item.InOctets, elapsed)
		item.OutBitsPerSecond = bitRate(before[1], item.OutOctets, elapsed)
	}
}

func bitRate(before, after int64, elapsed time.Duration) *int64 {
	if after < before {
		return nil
	}

	return PointerOf(int64(float64(after-before) * 8 / elapsed.Seconds()))
}
