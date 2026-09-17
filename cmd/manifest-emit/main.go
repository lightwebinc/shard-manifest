// Command manifest-emit sends a single BRC-139 ShardManifest datagram and
// exits. Useful for smoke-testing connectivity, multicast routing, and
// listener-side dispatch without running the full daemon.
package main

import (
	"context"
	"hash/crc32"
	"log/slog"
	"os"
	"time"

	"github.com/lightwebinc/shard-common/logging"

	"github.com/lightwebinc/shard-manifest/config"
	"github.com/lightwebinc/shard-manifest/metrics"
	"github.com/lightwebinc/shard-manifest/sender"
)

// Version is set via -ldflags at build time (see the Makefile's build-cli).
var Version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	// Same logging contract as the daemon: identity attributes on every line
	// and the operator's -log-format/-log-level honoured. A one-shot emitter
	// that logs in its own format is unjoinable with the daemon's lines for
	// the same fabric.
	logLevel := logging.ParseLevel(cfg.LogLevel)
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	logging.Init(logging.Options{
		Service:    metrics.ServiceName,
		InstanceID: cfg.InstanceID,
		Version:    Version,
		Level:      logLevel,
		Format:     logging.ParseFormat(cfg.LogFormat),
	})

	rec, err := metrics.New(cfg.InstanceID, "", 0)
	if err != nil {
		slog.Error("metrics", "err", err)
		os.Exit(1)
	}

	instanceID := crc32.Checksum([]byte(cfg.InstanceID), crc32.MakeTable(crc32.Castagnoli))
	if instanceID == 0 {
		instanceID = 1
	}

	snd, err := sender.New(cfg, rec, instanceID)
	if err != nil {
		slog.Error("sender", "err", err)
		os.Exit(1)
	}

	// Run for a single tick: cancel context just after kickoff send. Sender's
	// Run sends immediately on entry, then waits for the first jittered tick.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := snd.Run(ctx); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
	slog.Info("manifest emitted")
}
