package main

import (
	"context"
	"main/src/api"
	"slices"
	"sync"
	"time"
)

// discoveryQueue holds the devices waiting for a full discovery. Discoveries run in the background on a few workers
// (snmp.discovery_concurrency), separately from the poll cycles, so a large backlog (every device right after an
// upgrade, for example) is worked through gradually instead of all at once.
type discoveryQueue struct {
	mu       sync.Mutex
	pending  []api.Device
	inFlight map[string]struct{}
	finished map[string]time.Time
	wake     chan struct{}
}

var discoveries = &discoveryQueue{
	inFlight: make(map[string]struct{}),
	finished: make(map[string]time.Time),
	wake:     make(chan struct{}, 1),
}

// Schedule replaces the queue with the devices the polling server asked to discover in a device list fetched at
// fetchedAt. Devices being discovered right now are left out, and so are devices discovered since the list was fetched,
// since the server could not have known about those discoveries yet. Requested rediscoveries go first, then devices
// never discovered, then the ones discovered longest ago.
func (q *discoveryQueue) Schedule(devices []api.Device, fetchedAt time.Time) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	for id, finishedAt := range q.finished {
		if finishedAt.Before(fetchedAt) {
			delete(q.finished, id)
		}
	}

	pending := make([]api.Device, 0)

	for _, device := range devices {
		_, inFlight := q.inFlight[device.ID]
		_, finished := q.finished[device.ID]

		if device.Discover && !inFlight && !finished {
			pending = append(pending, device)
		}
	}

	slices.SortStableFunc(pending, compareDiscoveryPriority)

	q.pending = pending

	if len(pending) > 0 {
		q.signal()
	}

	return len(pending)
}

func compareDiscoveryPriority(a, b api.Device) int {
	if (a.DiscoveryRequestedAt != nil) != (b.DiscoveryRequestedAt != nil) {
		if a.DiscoveryRequestedAt != nil {
			return -1
		}

		return 1
	}

	if a.DiscoveryRequestedAt != nil {
		return a.DiscoveryRequestedAt.Compare(*b.DiscoveryRequestedAt)
	}

	if (a.LastDiscoveredAt != nil) != (b.LastDiscoveredAt != nil) {
		if a.LastDiscoveredAt == nil {
			return -1
		}

		return 1
	}

	if a.LastDiscoveredAt != nil {
		return a.LastDiscoveredAt.Compare(*b.LastDiscoveredAt)
	}

	return 0
}

// next waits for the next device to discover and marks it in flight. It returns false once ctx is done.
func (q *discoveryQueue) next(ctx context.Context) (api.Device, bool) {
	for {
		q.mu.Lock()

		if len(q.pending) > 0 {
			device := q.pending[0]
			q.pending = q.pending[1:]
			q.inFlight[device.ID] = struct{}{}

			// Wake another worker for the rest of the queue
			if len(q.pending) > 0 {
				q.signal()
			}

			q.mu.Unlock()

			return device, true
		}

		q.mu.Unlock()

		select {
		case <-q.wake:
		case <-ctx.Done():
			return api.Device{}, false
		}
	}
}

func (q *discoveryQueue) done(deviceID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.inFlight, deviceID)
	q.finished[deviceID] = time.Now()
}

// signal wakes one waiting worker without blocking. The caller must hold q.mu.
func (q *discoveryQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// RunDiscoveryWorkers discovers queued devices until ctx is done, submitting each result as soon as it is ready
func RunDiscoveryWorkers(ctx context.Context) {
	var wg sync.WaitGroup

	for range conf.SNMP.DiscoveryConcurrency {
		wg.Go(func() {
			for {
				device, ok := discoveries.next(ctx)

				if !ok {
					return
				}

				RunRecovered("DiscoverDevice", func() { discoverDevice(ctx, device) })

				discoveries.done(device.ID)
			}
		})
	}

	wg.Wait()
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
