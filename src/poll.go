package main

import (
	"context"
	"main/src/dashboard"
	"sync"
	"sync/atomic"
	"time"
)

const (
	submitBatchSize = 50
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
)

func PollAllDevices(ctx context.Context) time.Duration {
	startedAt := time.Now()

	list, err := client.FetchDevices(ctx)

	if err != nil {
		logger.Error("Failed to fetch device list from dashboard", "error", err)

		return retryDelay
	}

	interval := time.Duration(list.PollInterval) * time.Second

	pruneSamples(list.Devices)

	var (
		wg        sync.WaitGroup
		submitted sync.WaitGroup
		failed    atomic.Int64
		slots     = make(chan struct{}, conf.SNMP.Concurrency)
		results   = make(chan dashboard.PollResult, submitBatchSize)
	)

	submitted.Go(func() { submitResults(ctx, results) })

	for _, device := range list.Devices {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}

			defer func() { <-slots }()

			RunRecovered("PollDevice", func() {
				result := PollDevice(ctx, device, interval)

				if !result.Reachable {
					failed.Add(1)
				}

				if ctx.Err() == nil {
					results <- result
				}
			})
		})
	}

	wg.Wait()
	close(results)
	submitted.Wait()

	logger.Info(
		"Completed poll cycle",
		"devices", len(list.Devices),
		"unreachable", failed.Load(),
		"duration", time.Since(startedAt).Round(time.Millisecond).String(),
		"interval", interval.String(),
	)

	return interval
}

func submitResults(ctx context.Context, results <-chan dashboard.PollResult) {
	batch := make([]dashboard.PollResult, 0, submitBatchSize)

	flush := func() {
		if len(batch) < 1 {
			return
		}

		if err := client.SubmitResults(ctx, batch); err != nil {
			logger.Error("Failed to submit poll results to dashboard", "count", len(batch), "error", err)
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

func PollDevice(ctx context.Context, device dashboard.Device, interval time.Duration) dashboard.PollResult {
	polledAt := time.Now()

	online, latency := PingHost(ctx, device.Host)
	data, pollErr := CollectSNMPData(ctx, device, polledAt)

	result := dashboard.PollResult{
		DeviceID:       device.ID,
		PolledAt:       polledAt,
		PollDurationMS: time.Since(polledAt).Milliseconds(),
		Online:         online,
		Reachable:      pollErr == nil,
		LatencyMS:      latency,
		Interfaces:     make([]dashboard.Interface, 0),
		Neighbors:      make([]dashboard.Neighbor, 0),
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

func pruneSamples(devices []dashboard.Device) {
	active := make(map[string]struct{}, len(devices))

	for _, device := range devices {
		active[device.ID] = struct{}{}
	}

	previousSamplesMu.Lock()
	defer previousSamplesMu.Unlock()

	for id := range previousSamples {
		if _, ok := active[id]; !ok {
			delete(previousSamples, id)
		}
	}
}

func calculateInterfaceRates(result *dashboard.PollResult, interval time.Duration) {
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
