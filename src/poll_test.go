package main

import (
	"main/src/api"
	"strings"
	"testing"
	"time"
)

func TestPingOnlyResult(t *testing.T) {
	device := api.Device{ID: "device", PingOnly: true}

	up := pingOnlyResult(device, time.Now(), true, PointerOf(1.5))

	if !up.Online || up.Reachable || up.Error != nil || up.LatencyMS == nil || up.Interfaces == nil || up.Neighbors == nil {
		t.Fatalf("expected an answering ping-only device to be online without an error or SNMP data, got %+v", up)
	}

	down := pingOnlyResult(device, time.Now(), false, nil)

	if down.Online || down.Reachable || down.Error == nil {
		t.Fatalf("expected a silent ping-only device to be offline with an error, got %+v", down)
	}
}

func TestPrioritizeDevices(t *testing.T) {
	devices := []api.Device{{ID: "a"}, {ID: "b", Priority: true}, {ID: "c"}, {ID: "d", Priority: true}}

	ordered := prioritizeDevices(devices)
	ids := make([]string, len(ordered))

	for i, device := range ordered {
		ids[i] = device.ID
	}

	if strings.Join(ids, ",") != "b,d,a,c" {
		t.Fatalf("expected priority devices first in server order, got %v", ids)
	}

	if devices[0].ID != "a" {
		t.Fatal("expected the server's list to be left as it was")
	}

	if count := countPriorityDevices(ordered); count != 2 {
		t.Fatalf("expected 2 priority devices, got %d", count)
	}
}
