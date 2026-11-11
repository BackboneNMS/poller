package main

import (
	"main/src/api"
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
