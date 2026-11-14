package main

import (
	"context"
	"log/slog"
	"main/src/api"
	"main/src/config"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	Version    = "1.2.0"
	retryDelay = 30 * time.Second
)

var (
	conf   *config.Config = config.New()
	client *api.Client    = nil
	logger *slog.Logger   = newLogger()
)

func main() {
	if err := conf.ReadFile("config.yml"); err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	client = api.New(conf.PollingServer.URLs, conf.PollingServer.Token, Version, conf.PollingServer.Timeout)

	logger.Info("Starting poller", "version", Version, "pollingServers", conf.PollingServer.URLs, "concurrency", conf.SNMP.Concurrency, "discoveryConcurrency", conf.SNMP.DiscoveryConcurrency, "configBackupConcurrency", conf.ConfigBackup.Concurrency)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	defer stop()

	go RunDiscoveryWorkers(ctx)
	go RunConfigBackupWorkers(ctx)
	go RunVersionChecks(ctx)
	go RunTrapReceiver(ctx)

	for {
		startedAt := time.Now()
		interval := retryDelay

		RunRecovered("PollAllDevices", func() { interval = PollAllDevices(ctx) })

		select {
		case <-ctx.Done():
			logger.Info("Shutting down poller")

			return
		case <-time.After(time.Until(startedAt.Add(interval))):
		}
	}
}
