package main

import (
	"context"
	"main/src/api"
	"time"
)

// discoveries holds the devices waiting for a full discovery. Discoveries run in the background on a few workers
// (snmp.discovery_concurrency), separately from the poll cycles, so a large backlog (every device right after an
// upgrade, for example) is worked through gradually instead of all at once. Requested rediscoveries go first, then
// devices never discovered, then the ones discovered longest ago.
var discoveries = newDeviceQueue(func(device api.Device) bool { return device.Discover }, compareDiscoveryPriority)

func compareDiscoveryPriority(a, b api.Device) int {
	return compareRequestedThenOldest(a.DiscoveryRequestedAt, b.DiscoveryRequestedAt, a.LastDiscoveredAt, b.LastDiscoveredAt)
}

// RunDiscoveryWorkers discovers queued devices until ctx is done, submitting each result as soon as it is ready
func RunDiscoveryWorkers(ctx context.Context) {
	discoveries.runWorkers(ctx, "DiscoverDevice", conf.SNMP.DiscoveryConcurrency, discoverDevice)
}

func discoverDevice(ctx context.Context, device api.Device) {
	result := func() api.PollResult {
		lock := deviceLock(device.ID)

		// Waits for a status poll of the same device to finish, so the two never query it at the same time
		lock.Lock()
		defer lock.Unlock()

		return PollDevice(ctx, device, currentPollInterval(), true)
	}()

	if ctx.Err() != nil {
		return
	}

	if err := client.SubmitResults(ctx, []api.PollResult{result}); err != nil {
		logger.Error("Failed to submit discovery result to polling server", "device", device.Name, "error", err)

		return
	}

	logger.Info("Discovered device", "device", device.Name, "host", device.Host, "reachable", result.Reachable, "duration", (time.Duration(result.PollDurationMS) * time.Millisecond).String())
}
