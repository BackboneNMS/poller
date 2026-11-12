package main

import (
	"context"
	"main/src/semver"
	"time"
)

const versionCheckInterval = 6 * time.Hour

// RunVersionChecks asks the polling server for the latest poller release at startup and then every
// versionCheckInterval until ctx is done, logging a warning each time this poller is older
func RunVersionChecks(ctx context.Context) {
	for {
		RunRecovered("CheckVersion", func() { checkVersion(ctx) })

		select {
		case <-ctx.Done():
			return
		case <-time.After(versionCheckInterval):
		}
	}
}

func checkVersion(ctx context.Context) {
	current, err := semver.Parse(Version)

	if err != nil {
		logger.Error("Failed to check for a newer poller version", "error", err)

		return
	}

	value, err := client.FetchLatestVersion(ctx)

	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("Failed to check for a newer poller version", "error", err)
		}

		return
	}

	latest, err := semver.Parse(value)

	if err != nil {
		logger.Warn("Failed to check for a newer poller version", "error", err)

		return
	}

	if current.Compare(latest) < 0 {
		logger.Warn("A newer version of the poller is available", "version", Version, "latestVersion", latest.String(), "change", current.Change(latest))
	}
}
