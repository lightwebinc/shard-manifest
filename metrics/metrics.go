// Package metrics initialises an OpenTelemetry MeterProvider for the
// bitcoin-shard-manifest daemon, backed by both a Prometheus exporter
// (for scraping) and an optional OTLP gRPC exporter (for push-based
// delivery to any OTel-compatible backend). It also serves /metrics,
// /healthz, and /readyz over HTTP.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServiceName is the OTel resource service.name for this daemon.
const ServiceName = "bitcoin-shard-manifest"

// Version is set via -ldflags at build time.
var Version = "dev"

// Recorder is the metrics facade used by the rest of the daemon.
type Recorder struct {
	provider   *sdkmetric.MeterProvider
	promReg    promclient.Gatherer
	startTime  time.Time
	shutdownFn func(context.Context) error

	// readiness
	lastSendUnix atomic.Int64
	maxAge       atomic.Int64 // seconds
	draining     atomic.Bool

	// Counters / gauges
	announcementsSent metric.Int64Counter
	announcementBytes metric.Int64Counter
	sendErrors        metric.Int64Counter
	shardBitsGauge    metric.Int64Gauge
	joinedGroupsGauge metric.Int64Gauge
	lastSendGauge     metric.Int64Gauge
	buildInfo         metric.Int64Gauge
}

// New constructs a Recorder. instanceID is the OTel service.instance.id;
// otlpEndpoint is empty to disable OTLP push.
func New(instanceID string, otlpEndpoint string, otlpInterval time.Duration) (*Recorder, error) {
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", ServiceName),
			attribute.String("service.instance.id", instanceID),
			attribute.String("service.version", Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: build resource: %w", err)
	}

	reg := promclient.NewRegistry()
	promExp, err := prometheusexporter.New(prometheusexporter.WithRegisterer(reg))
	if err != nil {
		return nil, fmt.Errorf("metrics: prometheus exporter: %w", err)
	}

	runtimeReg := promclient.NewRegistry()
	runtimeReg.MustRegister(collectors.NewGoCollector())
	runtimeReg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	mpOpts := []sdkmetric.Option{
		sdkmetric.WithReader(promExp),
		sdkmetric.WithResource(res),
	}

	var shutdownFuncs []func(context.Context) error

	if otlpEndpoint != "" {
		otlpExp, oerr := otlpmetricgrpc.New(
			context.Background(),
			otlpmetricgrpc.WithEndpoint(otlpEndpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if oerr != nil {
			return nil, fmt.Errorf("metrics: OTLP exporter: %w", oerr)
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(otlpExp, sdkmetric.WithInterval(otlpInterval)),
		))
		shutdownFuncs = append(shutdownFuncs, otlpExp.Shutdown)
		slog.Info("OTLP exporter enabled", "endpoint", otlpEndpoint, "interval", otlpInterval)
	}

	mp := sdkmetric.NewMeterProvider(mpOpts...)
	shutdownFuncs = append(shutdownFuncs, mp.Shutdown)

	r := &Recorder{
		provider:  mp,
		promReg:   promclient.Gatherers{reg, runtimeReg},
		startTime: time.Now(),
		shutdownFn: func(ctx context.Context) error {
			var last error
			for _, fn := range shutdownFuncs {
				if err := fn(ctx); err != nil {
					last = err
				}
			}
			return last
		},
	}

	meter := mp.Meter(ServiceName)

	if r.announcementsSent, err = meter.Int64Counter("bsm_announcements_sent_total",
		metric.WithDescription("ShardManifest datagrams successfully sent")); err != nil {
		return nil, err
	}
	if r.announcementBytes, err = meter.Int64Counter("bsm_announcement_bytes_total",
		metric.WithDescription("Total bytes of ShardManifest datagrams successfully sent")); err != nil {
		return nil, err
	}
	if r.sendErrors, err = meter.Int64Counter("bsm_send_errors_total",
		metric.WithDescription("Send errors, labelled by kind")); err != nil {
		return nil, err
	}
	if r.shardBitsGauge, err = meter.Int64Gauge("bsm_shard_bits",
		metric.WithDescription("Currently advertised ShardBits value")); err != nil {
		return nil, err
	}
	if r.joinedGroupsGauge, err = meter.Int64Gauge("bsm_joined_groups",
		metric.WithDescription("Number of joined groups currently advertised")); err != nil {
		return nil, err
	}
	if r.lastSendGauge, err = meter.Int64Gauge("bsm_last_send_unixtime",
		metric.WithDescription("Unix time of the last successful manifest send")); err != nil {
		return nil, err
	}
	if r.buildInfo, err = meter.Int64Gauge("bsm_build_info",
		metric.WithDescription("Build info gauge (always 1; version/instance via labels)")); err != nil {
		return nil, err
	}
	r.buildInfo.Record(context.Background(), 1, metric.WithAttributes(
		attribute.String("version", Version),
		attribute.String("instance", instanceID),
	))

	return r, nil
}

// SetMaxAge configures /readyz: the daemon is considered ready if the last
// successful send is no older than maxAge.
func (r *Recorder) SetMaxAge(maxAge time.Duration) {
	r.maxAge.Store(int64(maxAge.Seconds()))
}

// SendOK records a successful send of size bytes.
func (r *Recorder) SendOK(size int) {
	now := time.Now().Unix()
	r.lastSendUnix.Store(now)
	r.announcementsSent.Add(context.Background(), 1)
	r.announcementBytes.Add(context.Background(), int64(size))
	r.lastSendGauge.Record(context.Background(), now)
}

// SendError records a send error of the given kind (e.g. "encode", "write",
// "dial").
func (r *Recorder) SendError(kind string) {
	r.sendErrors.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("kind", kind),
	))
}

// SetShardBits records the currently advertised ShardBits value.
func (r *Recorder) SetShardBits(v uint8) {
	r.shardBitsGauge.Record(context.Background(), int64(v))
}

// SetJoinedGroups records the number of currently joined groups.
func (r *Recorder) SetJoinedGroups(n int) {
	r.joinedGroupsGauge.Record(context.Background(), int64(n))
}

// SetDraining marks the daemon as draining so /readyz returns 503.
func (r *Recorder) SetDraining() {
	r.draining.Store(true)
}

// Shutdown flushes and stops the meter provider and OTLP exporter.
func (r *Recorder) Shutdown(ctx context.Context) {
	if err := r.shutdownFn(ctx); err != nil {
		slog.Warn("metrics shutdown error", "err", err)
	}
}

// Serve starts the HTTP server exposing /metrics, /healthz, /readyz on addr.
// Blocks until done is closed, then performs a graceful shutdown.
func (r *Recorder) Serve(addr string, done <-chan struct{}) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", r.handleHealthz)
	mux.HandleFunc("/readyz", r.handleReadyz)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("metrics server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "err", err)
		}
	}()
	<-done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("metrics server shutdown error", "err", err)
	}
}

func (r *Recorder) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"status":"ok","uptime_seconds":%.1f}`, time.Since(r.startTime).Seconds())
}

func (r *Recorder) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	last := r.lastSendUnix.Load()
	maxAge := r.maxAge.Load()
	now := time.Now().Unix()
	w.Header().Set("Content-Type", "application/json")

	if r.draining.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"status":"draining"}`)
		return
	}
	if last == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"status":"starting","last_send_unixtime":0}`)
		return
	}
	age := now - last
	if maxAge > 0 && age > maxAge {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"status":"stale","last_send_unixtime":%d,"age_seconds":%d,"max_age_seconds":%d}`,
			last, age, maxAge)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"status":"ready","last_send_unixtime":%d,"age_seconds":%d}`, last, age)
}
