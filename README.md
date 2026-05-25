# bitcoin-shard-manifest

`bitcoin-shard-manifest` is a tiny standalone daemon that periodically
multicasts [BRC-137](https://github.com/lightwebinc/bitcoin-multicast/blob/main/docs/brc-137-shard-manifest.md)
ShardManifest datagrams. Each manifest advertises the local participant's
`shard_bits` configuration, the set of shard group indices it claims to have
joined, identity, timestamp, TTL, and a `GenerationID`. Manifests are sent
directly to the IPv6 beacon multicast group (`FF0X::B:FFFD`) at a
configurable scope; **no proxy, no retransmission, no listener-side ACK**.

The service is purely informational: it does not subscribe to or interpret
data-plane shard groups. Its purpose is to make the network's sharding
configuration observable, detect divergence, and (in future revisions)
support automated, rate-limited shard-bit shifts.

## Quick start

```bash
make build
./bitcoin-shard-manifest \
  -shard-bits=4 \
  -joined-groups=0,1,2,3 \
  -role-hint=proxy \
  -manifest-scope=site \
  -iface=eth0
```

Smoke-test with the one-shot CLI:

```bash
make build-cli
./manifest-emit -shard-bits=4 -joined-groups=all -manifest-scope=site
```

## Configuration

All flags can be supplied via environment variables (UPPER_SNAKE_CASE).
See [docs/configuration.md](docs/configuration.md) for the full reference.

| Flag                  | Env                  | Default        | Notes                                            |
| --------------------- | -------------------- | -------------- | ------------------------------------------------ |
| `-shard-bits`         | `SHARD_BITS`         | required       | 0..12                                            |
| `-joined-groups`      | `JOINED_GROUPS`      | `""`           | comma list of indices (hex/dec), or `all`        |
| `-bitmap`             | `BITMAP`             | `auto`         | `auto`/`list`/`bitmap`                           |
| `-role-hint`          | `ROLE_HINT`          | `generic`      | proxy/listener/retry-endpoint/producer/...       |
| `-generation-id`      | `GENERATION_ID`      | zero UUID      | 16-byte hex; bump when ShardBits changes         |
| `-authoritative`      | `AUTHORITATIVE`      | `false`        | sets Flags.Authoritative                         |
| `-manifest-scope`     | `MANIFEST_SCOPE`     | `site`         | comma list of `link,site,org,global`             |
| `-announce-interval`  | `ANNOUNCE_INTERVAL`  | `300s`         |                                                  |
| `-ttl`                | `TTL`                | `0`            | seconds; 0 = consumer default (3× interval)      |
| `-port`               | `PORT`               | `9001`         | UDP destination port                             |
| `-iface`              | `IFACE`              | (auto-pick)    | egress interface                                 |
| `-mc-group-id`        | `MC_GROUP_ID`        | `0x000B`       | IANA group-id                                    |
| `-metrics-addr`       | `METRICS_ADDR`       | `[::]:9091`    | metrics + health HTTP listener                   |
| `-otlp-endpoint`      | `OTLP_ENDPOINT`      | `""`           | optional OTLP gRPC endpoint                      |
| `-otlp-interval`      | `OTLP_INTERVAL`      | `15s`          |                                                  |
| `-debug`              | `DEBUG`              | `false`        | verbose logging                                  |

## Observability

- `GET /metrics` — Prometheus exposition (default `:9091`).
- `GET /healthz` — process-alive probe (always 200).
- `GET /readyz` — 200 once a manifest has been sent in the last
  `2 × AnnounceInterval`; 503 when starting, draining, or stale.

Metric series:

| Name                              | Type      | Labels      | Notes                                            |
| --------------------------------- | --------- | ----------- | ------------------------------------------------ |
| `bsm_announcements_sent_total`    | counter   | —           | successful sends                                 |
| `bsm_announcement_bytes_total`    | counter   | —           | total bytes successfully sent                    |
| `bsm_send_errors_total`           | counter   | `kind`      | `build`/`encode`/`write`                         |
| `bsm_shard_bits`                  | gauge     | —           | currently advertised value                       |
| `bsm_joined_groups`               | gauge     | —           | currently advertised join count                  |
| `bsm_last_send_unixtime`          | gauge     | —           | last successful send                             |
| `bsm_build_info`                  | gauge     | `version`,`instance` | always 1                                |

The daemon also serves runtime collectors (`go_*`, `process_*`) on the same
endpoint.

## Graceful shutdown

On `SIGTERM` the daemon emits one final manifest with `Flags.Shutdown=1` so
consumers MAY evict the corresponding registry entry immediately.

## Layout

```
.
├── main.go                 # daemon entrypoint
├── cmd/manifest-emit/      # one-shot CLI
├── config/                 # flag/env loader + tests
├── sender/                 # encode + emit loop + tests
├── metrics/                # OTel + Prometheus + healthz/readyz
├── ci/                     # Dagger CI driver
├── docs/                   # architecture + configuration docs
├── Dockerfile
├── Makefile
└── .github/workflows/{ci,release}.yml
```

## License

Apache 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
