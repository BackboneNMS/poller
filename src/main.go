package main

import (
	"context"
	"log/slog"
	"main/src/config"
	"main/src/dashboard"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	Version    = "1.0.0"
	retryDelay = 30 * time.Second
)

var (
	conf   *config.Config    = config.New()
	client *dashboard.Client = nil
	logger *slog.Logger      = slog.New(slog.NewJSONHandler(os.Stdout, nil))
)

func main() {
	if err := conf.ReadFile("config.yml"); err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	client = dashboard.New(conf.Dashboard.URL, conf.Dashboard.Token, Version, conf.Dashboard.Timeout)

	logger.Info("Starting poller", "version", Version, "dashboard", conf.Dashboard.URL, "concurrency", conf.SNMP.Concurrency)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	defer stop()

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
