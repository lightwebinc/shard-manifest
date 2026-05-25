// Command bitcoin-shard-manifest is a small standalone daemon that
// periodically emits BRC-137 ShardManifest datagrams to the IPv6 multicast
// beacon group. It advertises the local participant's shard_bits
// configuration and the set of shard groups it has joined so operators (and
// future automation) can detect divergence and coordinate shifts.
package main

import (
	"context"
	"hash/crc32"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/lightwebinc/bitcoin-shard-manifest/config"
	"github.com/lightwebinc/bitcoin-shard-manifest/metrics"
	"github.com/lightwebinc/bitcoin-shard-manifest/sender"
)

// Version is set via -ldflags at build time.
var Version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	metrics.Version = Version

	logLevel := slog.LevelInfo
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("bitcoin-shard-manifest starting",
		"version", Version,
		"shard_bits", cfg.ShardBits,
		"role_hint", cfg.RoleHint,
		"manifest_scope", cfg.ManifestScope,
		"announce_interval", cfg.AnnounceInterval,
		"ttl", cfg.TTL,
		"port", cfg.Port,
		"metrics_addr", cfg.MetricsAddr,
	)

	rec, err := metrics.New(cfg.InstanceID, cfg.OTLPEndpoint, cfg.OTLPInterval)
	if err != nil {
		return err
	}
	rec.SetMaxAge(2 * cfg.AnnounceInterval)

	instanceID := hashInstanceID(cfg.InstanceID)

	snd, err := sender.New(cfg, rec, instanceID)
	if err != nil {
		return err
	}

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		rec.Serve(cfg.MetricsAddr, done)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := snd.Run(ctx); err != nil {
			slog.Error("sender exited with error", "err", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("shutdown signal received", "signal", sig.String())

	rec.SetDraining()
	cancel()
	close(done)
	wg.Wait()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	rec.Shutdown(shutdownCtx)

	slog.Info("shutdown complete")
	return nil
}

// hashInstanceID derives a stable 32-bit identifier from the instance
// hostname. CRC32c matches the BRC-126 ADVERT scheme so the same mapping
// works in any future cross-protocol consumer.
func hashInstanceID(s string) uint32 {
	h := crc32.Checksum([]byte(s), crc32.MakeTable(crc32.Castagnoli))
	if h == 0 {
		h = 1
	}
	return h
}
