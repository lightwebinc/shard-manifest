# shard-manifest

> Part of the [**BSV Layered Multicast**](https://github.com/lightwebinc/bsv-multicast) open-source project — see the main repository for the full architecture, design docs, and BRC specifications.

`shard-manifest` is a tiny standalone daemon that periodically
multicasts [BRC-139](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/brc-139-shard-manifest.md)
ShardManifest datagrams. Each manifest advertises the local participant's
`shard_bits` configuration, the set of shard group indices it claims to have
joined, identity, timestamp, TTL, and a `GenerationID`. Manifests are sent
directly to the IPv6 beacon multicast group (`FF0X::B:FFFD`) at a
configurable scope; **no proxy, no retransmission, no listener-side ACK**.

The service is purely informational: it does not subscribe to or interpret
data-plane shard groups. Its purpose is to make the network's sharding
configuration observable, detect divergence, and announce live re-sharding
(BRC-139 Successor block; see [docs/configuration.md](docs/configuration.md#live-re-sharding-brc-139-successor-block)).

## Quick start

```bash
make build
./shard-manifest \
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
See [docs/configuration.md](docs/configuration.md) for the full reference, and the
[Unified Component Logging](https://github.com/lightwebinc/shard-common/blob/main/docs/logging.md)
for `-log-format`/`-log-level`/`-trace-sampling`, the `host.inventory` event, and runtime `/loglevel`.

## Observability

`/metrics`, `/healthz`, `/readyz` on `-metrics-addr` (default `:9091`). Endpoints and `bsm_` metric series: [docs/configuration.md](docs/configuration.md#metrics).

