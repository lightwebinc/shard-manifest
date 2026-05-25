// Command manifest-emit sends a single BRC-137 ShardManifest datagram and
// exits. Useful for smoke-testing connectivity, multicast routing, and
// listener-side dispatch without running the full daemon.
package main

import (
	"context"
	"hash/crc32"
	"log/slog"
	"os"
	"time"

	"github.com/lightwebinc/bitcoin-shard-manifest/config"
	"github.com/lightwebinc/bitcoin-shard-manifest/metrics"
	"github.com/lightwebinc/bitcoin-shard-manifest/sender"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

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
