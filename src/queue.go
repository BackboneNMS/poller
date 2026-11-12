package main

import (
	"context"
	"main/src/api"
	"slices"
	"sync"
	"time"
)

// deviceQueue holds the devices waiting for background work the polling server asked for in the device list (a full
// discovery, or a config backup), worked through by a few workers separately from the poll cycles. wants picks the
// devices that need the work, and compare orders them.
type deviceQueue struct {
	mu       sync.Mutex
	wants    func(api.Device) bool
	compare  func(a, b api.Device) int
	pending  []api.Device
	inFlight map[string]struct{}
	finished map[string]time.Time
	wake     chan struct{}
}

func newDeviceQueue(wants func(api.Device) bool, compare func(a, b api.Device) int) *deviceQueue {
	return &deviceQueue{
		wants:    wants,
		compare:  compare,
		inFlight: make(map[string]struct{}),
		finished: make(map[string]time.Time),
		wake:     make(chan struct{}, 1),
	}
}

// Schedule replaces the queue with the devices that need the work in a device list fetched at fetchedAt. Devices being
// worked on right now are left out, and so are devices finished since the list was fetched, since the server could not
// have known about those results yet.
func (q *deviceQueue) Schedule(devices []api.Device, fetchedAt time.Time) int {
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

		if q.wants(device) && !inFlight && !finished {
			pending = append(pending, device)
		}
	}

	slices.SortStableFunc(pending, q.compare)

	q.pending = pending

	if len(pending) > 0 {
		q.signal()
	}

	return len(pending)
}

// next waits for the next device and marks it in flight. It returns false once ctx is done.
func (q *deviceQueue) next(ctx context.Context) (api.Device, bool) {
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

func (q *deviceQueue) done(deviceID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.inFlight, deviceID)
	q.finished[deviceID] = time.Now()
}

// signal wakes one waiting worker without blocking. The caller must hold q.mu.
func (q *deviceQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// runWorkers takes devices off the queue on `workers` goroutines until ctx is done, calling work for each one
func (q *deviceQueue) runWorkers(ctx context.Context, name string, workers int, work func(context.Context, api.Device)) {
	var wg sync.WaitGroup

	for range workers {
		wg.Go(func() {
			for {
				device, ok := q.next(ctx)

				if !ok {
					return
				}

				RunRecovered(name, func() { work(ctx, device) })

				q.done(device.ID)
			}
		})
	}

	wg.Wait()
}

// compareRequestedThenOldest orders devices with a pending request first (the earliest request first), then devices
// never worked on, then the ones worked on longest ago
func compareRequestedThenOldest(aRequestedAt, bRequestedAt, aLastAt, bLastAt *time.Time) int {
	if (aRequestedAt != nil) != (bRequestedAt != nil) {
		if aRequestedAt != nil {
			return -1
		}

		return 1
	}

	if aRequestedAt != nil {
		return aRequestedAt.Compare(*bRequestedAt)
	}

	if (aLastAt != nil) != (bLastAt != nil) {
		if aLastAt == nil {
			return -1
		}

		return 1
	}

	if aLastAt != nil {
		return aLastAt.Compare(*bLastAt)
	}

	return 0
}
