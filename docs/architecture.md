# Architecture

`bitcoin-shard-manifest` is a single-purpose periodic announcer. It does
**not** ingress, cache, retransmit, or interpret data-plane frames; it only
emits BRC-137 ShardManifest datagrams.

## Data flow

```
+-------------------+                 +----------------------+
|  configuration    |                 |  IPv6 multicast      |
|  (flags / env)    |                 |  beacon group(s):    |
+--------+----------+                 |  FF05::B:FFFD        |
         |                            |  FF08::B:FFFD        |
         v                            |  FF0E::B:FFFD        |
+--------+----------+                 +----------+-----------+
|   config.Load()   |                            ^
+--------+----------+                            |
         |                                       |
         v                                       |
+--------+----------+      build + encode        |
|  sender.Sender    +-------- WriteTo ---------->+
+--------+----------+                            |
         |                                       |
         | metric updates                        |
         v                                       |
+--------+----------+      Prometheus scrape     |
|  metrics.Recorder +<--- HTTP :9091 ---+        |
+-------------------+                   |        |
                                        |        |
                                +-------+--------+----------+
                                |  Prometheus / Grafana     |
                                |  + downstream consumers   |
                                +---------------------------+
```

## Components

### `config` — flag/env loader
Parses CLI flags with environment-variable fallbacks. Validates `ShardBits`
(0..12), parses `JoinedGroups` (or `all`), the `GenerationID` UUID, and the
manifest scope list. Resolves the egress interface and primary IPv6 address
on demand.

### `sender` — encode + emit loop
Builds an in-memory `frame.ShardManifest`, calls `frame.EncodeShardManifest`,
and writes the resulting datagram to one UDP socket per configured scope
(each socket dialed to the corresponding beacon address). Sockets force the
egress interface via `IPV6_MULTICAST_IF`. The send loop ticks at
`AnnounceInterval` ± 10 % jitter; on `ctx.Done()` it emits one final
manifest with `Flags.Shutdown=1`.

The sender chooses encoding form (list vs bitmap) per `cfg.Encoding`:

- `auto` (default): list when ≤ 32 joined groups, bitmap otherwise.
- `list`: forced list form.
- `bitmap`: forced bitmap form sized `ceil(2^ShardBits / 8)` bytes.

### `metrics` — observability
Wraps an OpenTelemetry `MeterProvider` with a Prometheus exporter (always
on) and an optional OTLP gRPC exporter. The HTTP server exposes:

- `/metrics` — Prometheus exposition (also includes `go_*`/`process_*`).
- `/healthz` — always 200.
- `/readyz` — 200 once a manifest has been sent within
  `2 × AnnounceInterval`; 503 otherwise (starting, draining, or stale).

### `cmd/manifest-emit`
One-shot CLI that reuses `config` + `sender` to emit a single manifest and
exit. Useful for ops/debugging multicast routing without running the
long-running daemon.

## On-the-wire format

See [BRC-137](https://github.com/lightwebinc/bitcoin-multicast/blob/main/docs/brc-137-shard-manifest.md)
for the complete specification. Key points:

- 64-byte header + variable-length payload (list of 16-bit indices or bitmap).
- `MsgType = 0x40` at offset 6; consumers MUST dispatch on this byte before
  parsing because `MsgType 0x20` (BRC-126 ADVERT) shares the beacon group.
- 4-byte `ManifestCRC` at offset 44 (CRC32c, Castagnoli) covers the entire
  datagram with the CRC field zeroed.

## Identity

`InstanceID` is `CRC32c(hostname)` (matching BRC-126 ADVERT) so the same
identifier is stable across restarts. The source IPv6 in the datagram
header (`SrcIPv6`) is informational; consumers SHOULD authoritative-key on
the IPv6 packet header source.

## Failure modes

- **No global IPv6 address on the egress interface** — startup fails. Set
  `-iface` explicitly or fix node networking.
- **Multicast send error** — incremented as `bsm_send_errors_total{kind="write"}`;
  the loop continues and retries on the next tick.
- **Encode error** — extremely unlikely (config is validated upfront);
  incremented as `bsm_send_errors_total{kind="encode"}`.

## Resource footprint

The daemon does no I/O between ticks beyond Prometheus scrapes. With
`AnnounceInterval=300s` it sends roughly one ≤ 1 KB datagram per scope per
5 minutes. Memory is dominated by the OTel MeterProvider (~ a few MB).
