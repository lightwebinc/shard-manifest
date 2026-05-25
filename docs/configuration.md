# Configuration

All parameters accept a CLI flag and an environment variable. CLI flags
take precedence; environment variables provide fallbacks; defaults apply
when neither is set.

## Identity and content

| Flag                | Env             | Default     | Description                                                                 |
| ------------------- | --------------- | ----------- | --------------------------------------------------------------------------- |
| `-shard-bits`       | `SHARD_BITS`    | `0`         | Number of TxID prefix bits used as the shard group key (0..12). Required.   |
| `-joined-groups`    | `JOINED_GROUPS` | `""`        | Comma list of group indices (decimal or `0x` hex), or `all`, or empty.      |
| `-bitmap`           | `BITMAP`        | `auto`      | Encoding form: `auto` (list ≤32 entries, else bitmap), `list`, or `bitmap`. |
| `-role-hint`        | `ROLE_HINT`     | `generic`   | One of `generic`, `proxy`, `listener`, `retry-endpoint`, `producer`, `manifest-only`. Informational. |
| `-generation-id`    | `GENERATION_ID` | zero UUID   | 16-byte hex (with or without dashes). Bump whenever `ShardBits` changes.    |
| `-authoritative`    | `AUTHORITATIVE` | `false`     | Sets `Flags.Authoritative` on the wire.                                     |
| `-instance-id`      | `INSTANCE_ID`   | hostname    | OTel `service.instance.id`. The 32-bit `InstanceID` field is `CRC32c(this)`. |

## Network

| Flag                | Env             | Default     | Description                                                          |
| ------------------- | --------------- | ----------- | -------------------------------------------------------------------- |
| `-iface`            | `IFACE`         | (auto)      | Egress interface for multicast send. When unset, the first non-loopback interface with a global IPv6 address is used. |
| `-port`             | `PORT`          | `9001`      | UDP destination port.                                                |
| `-manifest-scope`   | `MANIFEST_SCOPE`| `site`      | Comma list of scopes: `link`, `site`, `org`, `global`. One datagram is sent per scope per tick. |
| `-mc-group-id`      | `MC_GROUP_ID`   | `0x000B`    | 16-bit IANA multicast group-id occupying bytes [12:14] of the IPv6 group address. |

## Cadence

| Flag                  | Env                  | Default   | Description                                                       |
| --------------------- | -------------------- | --------- | ----------------------------------------------------------------- |
| `-announce-interval`  | `ANNOUNCE_INTERVAL`  | `300s`    | Time between sends. Each send is jittered by ±10 %.               |
| `-ttl`                | `TTL`                | `0`       | Wire-format TTL in seconds. `0` = consumer applies its default (3× interval). |

## Observability

| Flag                | Env             | Default       | Description                                            |
| ------------------- | --------------- | ------------- | ------------------------------------------------------ |
| `-metrics-addr`     | `METRICS_ADDR`  | `[::]:9091`   | HTTP listener for `/metrics`, `/healthz`, `/readyz`.   |
| `-otlp-endpoint`    | `OTLP_ENDPOINT` | `""`          | OTLP gRPC endpoint (e.g. `otel-collector:4317`). Empty disables OTLP push. |
| `-otlp-interval`    | `OTLP_INTERVAL` | `15s`         | OTLP push interval.                                    |
| `-debug`            | `DEBUG`         | `false`       | Enable `slog.LevelDebug`.                              |

## Behaviour notes

- **`shard-bits` = 0** — single-group configuration. `joined-groups` may be
  empty, `0`, or `all`; in the last two cases the manifest carries
  `Flags.GroupsValid=1` and a single-entry list (or 1-byte bitmap).
- **`joined-groups` = `all`** — the daemon enumerates all `2^shard-bits`
  indices.
- **`joined-groups` empty** — identity-only manifest: `Flags.GroupsValid=0`,
  no payload. Useful for participants that don't subscribe to any data-plane
  groups (e.g. a producer signalling its `shard-bits` agreement).
- **Bitmap form size** — `ceil(2^shard-bits / 8)` bytes regardless of how
  many bits are set. With `shard-bits=12` this is exactly 512 B; with
  `shard-bits=8` it is 32 B.

## Example: proxy on shard_bits=4 joined to all groups

```bash
bitcoin-shard-manifest \
  -shard-bits=4 \
  -joined-groups=all \
  -role-hint=proxy \
  -manifest-scope=site,global \
  -iface=enp6s0 \
  -generation-id=00112233445566778899aabbccddeeff
```

## Example: listener on shard_bits=4 joined to two specific groups

```bash
bitcoin-shard-manifest \
  -shard-bits=4 \
  -joined-groups=0x3,0x7 \
  -role-hint=listener \
  -manifest-scope=site \
  -bitmap=list
```
