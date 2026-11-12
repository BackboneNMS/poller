package main

import (
	"context"
	"main/src/api"
	"slices"
	"testing"
	"time"
)

func newTestDiscoveryQueue() *deviceQueue {
	return newDeviceQueue(discoveries.wants, discoveries.compare)
}

func queuedIDs(q *deviceQueue) []string {
	ids := make([]string, len(q.pending))

	for i, device := range q.pending {
		ids[i] = device.ID
	}

	return ids
}

func TestDiscoveryQueueOrdersRequestedThenNeverThenOldest(t *testing.T) {
	now := time.Now()
	q := newTestDiscoveryQueue()

	q.Schedule([]api.Device{
		{ID: "recent", Discover: true, LastDiscoveredAt: PointerOf(now.Add(-time.Hour))},
		{ID: "not-due", LastDiscoveredAt: PointerOf(now.Add(-time.Minute))},
		{ID: "never", Discover: true},
		{ID: "oldest", Discover: true, LastDiscoveredAt: PointerOf(now.Add(-48 * time.Hour))},
		{ID: "requested-later", Discover: true, DiscoveryRequestedAt: PointerOf(now.Add(-time.Minute)), LastDiscoveredAt: PointerOf(now)},
		{ID: "requested-first", Discover: true, DiscoveryRequestedAt: PointerOf(now.Add(-time.Hour))},
	}, now)

	expected := []string{"requested-first", "requested-later", "never", "oldest", "recent"}

	if ids := queuedIDs(q); !slices.Equal(ids, expected) {
		t.Fatalf("expected the queue %v, got %v", expected, ids)
	}
}

func TestDiscoveryQueueSkipsInFlightAndJustFinishedDevices(t *testing.T) {
	q := newTestDiscoveryQueue()
	devices := []api.Device{{ID: "a", Discover: true}, {ID: "b", Discover: true}, {ID: "c", Discover: true}}

	fetchedAt := time.Now()

	q.Schedule(devices, fetchedAt)

	first, _ := q.next(context.Background())
	second, _ := q.next(context.Background())

	q.done(second.ID)

	// The list fetched before the second discovery finished still asks for it, which the queue must ignore
	if q.Schedule(devices, fetchedAt); !slices.Equal(queuedIDs(q), []string{"c"}) {
		t.Fatalf("expected only the device neither in flight nor just finished to be queued, got %v (in flight %s, finished %s)", queuedIDs(q), first.ID, second.ID)
	}

	// A list fetched after it finished reflects the result, so asking again means another discovery is wanted
	if q.Schedule(devices, time.Now().Add(time.Second)); !slices.Equal(queuedIDs(q), []string{second.ID, "c"}) {
		t.Fatalf("expected a later list to queue the finished device again, got %v", queuedIDs(q))
	}
}

func TestDiscoveryQueueNextStopsWithContext(t *testing.T) {
	q := newTestDiscoveryQueue()
	ctx, cancel := context.WithCancel(context.Background())

	cancel()

	if _, ok := q.next(ctx); ok {
		t.Fatal("expected next to return false once the context is done")
	}
}
