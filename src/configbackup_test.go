package main

import (
	"context"
	"main/src/api"
	"strings"
	"testing"
	"time"
)

func TestBackUpDeviceConfigFailsWithoutAPlatformOrCredentials(t *testing.T) {
	for _, test := range []struct {
		name   string
		backup api.ConfigBackup
		error  string
	}{
		{"unknown platform", api.ConfigBackup{Platform: "FUTURE_OS"}, "does not support the FUTURE_OS platform"},
		{"undetectable platform", api.ConfigBackup{ObjectID: "1.3.6.1.4.1.8072.3.2.10", Description: "Linux"}, "could not be detected"},
		{"no credentials", api.ConfigBackup{Platform: "CISCO_IOS"}, "no login credentials"},
	} {
		result := BackUpDeviceConfig(context.Background(), api.Device{ID: "d", Host: "192.0.2.1", ConfigBackup: &test.backup})

		if result.Error == nil || !strings.Contains(*result.Error, test.error) || result.Config != nil {
			t.Errorf("%s: expected an error containing %q, got %+v", test.name, test.error, result)
		}
	}
}

func TestConfigBackupQueueOrdersRequestedThenNeverThenOldest(t *testing.T) {
	q := newDeviceQueue(configBackups.wants, configBackups.compare)
	now := PointerOf(time.Now())

	q.Schedule([]api.Device{
		{ID: "not-due"},
		{ID: "recent", ConfigBackup: &api.ConfigBackup{LastAttemptAt: now}},
		{ID: "never", ConfigBackup: &api.ConfigBackup{}},
		{ID: "requested", ConfigBackup: &api.ConfigBackup{RequestedAt: now, LastAttemptAt: now}},
	}, *now)

	if ids := queuedIDs(q); strings.Join(ids, ",") != "requested,never,recent" {
		t.Fatalf("unexpected queue %v", ids)
	}
}
