# Configuration

All parameters accept a CLI flag and an environment variable. CLI flags
take precedence; environment variables provide fallbacks; defaults apply
when neither is set.

## Identity and content

| Flag                | Env             | Default     | Description                                                                 |
| ------------------- | --------------- | ----------- | --------------------------------------------------------------------------- |
| `-shard-bits`       | `SHARD_BITS`    | `2`         | Number of TxID prefix bits used as the shard group key (0..12). `0` is a valid single-group configuration (see Behaviour notes). |
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
| `-manifest-scope`   | `MANIFEST_SCOPE`| `site`      | Comma list of scopes: `link`, `site`, `org`, `global`. One datagram is sent per destination per tick. |
| `-control-group-compat` | `CONTROL_GROUP_COMPAT` | `asm-only` | Which prefix the `0xFFFD` control group takes: `asm-only` \| `both` \| `derived`. See [Control-plane group address](#control-plane-group-address). |
| `-mc-group-id`      | `MC_GROUP_ID`   | `0x000B`    | 16-bit IANA multicast group-id occupying bytes [12:14] of the IPv6 group address. |

## Control-plane group address

Manifests are announced to the BRC-129 control group at index `0xFFFD` — the
same group address the BRC-126 ADVERT beacon uses, on a different UDP port
(`-port`, default 9001, vs the beacon's 9300).

Per BRC-126 §Beacon Scopes and BRC-129 §Source Mode and Address Range that
address is a function of the **source mode** as well as the scope: under SSM
the control-plane groups take the source-specific `FF3x` prefix, exactly as
the data-plane shard groups do.

| `-manifest-scope` | ASM group      | SSM group      |
| ----------------- | -------------- | -------------- |
| `link`            | `FF02::B:FFFD` | —              |
| `site`            | `FF05::B:FFFD` | `FF35::B:FFFD` |
| `org`             | `FF08::B:FFFD` | —              |
| `global`          | `FF0E::B:FFFD` | `FF3E::B:FFFD` |

BRC-129 tables an SSM control group at site and global scope only, so `link`
and `org` are ASM-only. With `-control-group-compat=derived` — an explicit
request for the conformant address — combining either with
`-source-mode=ssm` is a startup error rather than a silent fall back to
`FF0x`. With `asm-only` or `both` they keep working exactly as today (`both`
simply has no second form to add), so a binary upgrade at default settings
can never fail to start.

### `-control-group-compat` / `CONTROL_GROUP_COMPAT` (default: `asm-only`)

| Value | Announces to |
|-------|--------------|
| `asm-only` (default) | Always the any-source `FF0x` form, ignoring `-source-mode`. Pre-fix behaviour. |
| `both` | Both the `FF0x` form and the `-source-mode`-derived form where one exists (one datagram to each per tick, per scope). |
| `derived` | The `-source-mode`-derived form only: `FF3x` under `-source-mode=ssm`. BRC-126/129 conformant. |

Under `-source-mode=asm` all three collapse to the same `FF0x` prefixes, so
the flag does nothing in an ASM deployment.

**This is a flag day.** Releases before this one derived the destination
from `-manifest-scope` alone and always produced the any-source prefix, even
under `-source-mode=ssm`. A consumer joined to `FF35::B:FFFD` sees nothing
from an announcer still sending to `FF05::B:FFFD`, and a manifest that lands
on the wrong group raises no error anywhere: the symptom is a consumer that
never reaches pilot quorum however many announcers are running.

The default is `asm-only` precisely so that upgrading announcers is safe on
its own: the binary changes, the wire does not.

**Rollout order** — the other sender is `retry-endpoint`; the receivers are
`shard-listener` and `shard-proxy`:

1. Roll every **receiver** (shard-listener, shard-proxy). Their default is
   `both`, so they join the legacy and the conformant group together.
2. Roll every **sender** (retry-endpoint, shard-manifest). Default
   `asm-only`; the wire does not move.
3. One converge sets the **senders** to `derived`. Manifests move to
   `FF3x`, which every receiver from step 1 already joined.
4. After a soak, one converge sets the **receivers** to `derived` to drop
   the legacy join.

Setting this announcer to `derived` before step 1 has covered every consumer
is the one ordering that silently strands a peer. `both` is the escape hatch
for a fleet that is knowingly mixed and does not want a second converge.

Under `-source-mode=ssm` a fabric's multicast routes and PIM/smcroute group
ranges are usually derived from the source mode too, and so cover
`ff35::/16` and `ff3e::/16` but not `ff05::/16`. The legacy leg of `both`
therefore reaches only same-segment consumers on such a fabric — which is
all it reached before this fix as well. Check that anything keyed on the
group prefix rather than on `ff00::/8` — multicast routes, PIM/smcroute
group ranges, MLD snooping filters, narrowed firewall rules — covers the SSM
block before moving off `asm-only`.

Note that this changes the destination address only. The manifest wire
format, `Flags.SourceModeSSM` and the `Flags.SourcesValid` payload are
untouched.

## SSM (RFC 4607)

See the [SSM Support Plan](https://github.com/lightwebinc/bsv-multicast/blob/main/DESIGN.md#source-specific-multicast-ssm).
The shard-manifest is the **authoritative publisher source set** for
downstream SSM consumers: when `-source-mode=ssm`, every emitted
manifest carries `Flags.SourceModeSSM` (BRC-139 bit 3) and, when
`-publishers` is non-empty, the trailing `SourceCount × 16`-byte
sources payload under `Flags.SourcesValid` (bit 4). Listeners and
retry-endpoints union the source set across currently-valid manifests
to compute their `(S,G)` data-plane joins.

| Flag                  | Env                  | Default | Description                                                                                                                                          |
| --------------------- | -------------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-source-mode`        | `SOURCE_MODE`        | `asm`   | When `ssm`, sets `Flags.SourceModeSSM` on every manifest and REQUIRES `-publishers` to be non-empty. Also selects the control-group prefix — see [Control-plane group address](#control-plane-group-address). |
| `-publishers`         | `PUBLISHERS`         | `""`    | CSV of data-plane publisher addresses (IPv6 literals or DNS names; a headless-Service name is the expected production form). Resolved via `shard-common/bootstrap.Resolver` and emitted as the `Flags.SourcesValid` payload union. |
| `-publishers-refresh` | `PUBLISHERS_REFRESH` | `30s`   | DNS re-resolve interval. Last-good AAAA set is retained on transient refresh failures so brief DNS outages don't empty the manifest source payload. |

The shard-manifest pod's own source IPv6 is what receivers pass to
`-ssm-bootstrap-manifest` (helm: `config.ssmBootstrap.manifest`) to
`(S,G)`-join the manifest group under Posture C. Distinct IPv6 per
replica is required; use Multus + deterministic IPAM (Whereabouts) for
stable per-pod addressing.

## Pilot mode (BRC-139 auto-shard-config)

A shard-manifest configured with `-pilot-only` becomes a pilot
announcer: the manifest's groups payload describes desired fleet state
(what consumers SHOULD join), not the announcer's own joins. Pilots
must be `-authoritative=true`; `-pilot-only` forces it. See the
[Automatic Shard Configuration Plan](https://github.com/lightwebinc/bsv-multicast/blob/main/DESIGN.md#automatic-shard-configuration).

Operators MUST stand up at least `-pilot-quorum` (proxy/listener
default `2`) pilot replicas with the same `-shard-bits`,
`-generation-id`, and `-joined-groups` for consumers to adopt.

> **Note:** `-pilot-quorum` is a **consumer-side** knob (proxy/listener),
> not a shard-manifest flag. It sets how many agreeing pilot manifests a
> consumer requires before adopting a configuration. shard-manifest only
> *emits* pilot manifests; it does not observe or count quorum.

| Flag           | Env           | Default | Description                                                              |
| -------------- | ------------- | ------- | ------------------------------------------------------------------------ |
| `-pilot-only`  | `PILOT_ONLY`  | `false` | Sets `Flags.PilotOnly` (BRC-139 bit 5) and forces `-authoritative=true`. |

## Live re-sharding (BRC-139 Successor block)

When the operator publishes a Successor block on a pilot's manifest,
auto-config consumers see a `(GenerationID, ShardBits, SourceModeSSM,
TransitionEpoch)` candidate; with `-live-resharding=true` on the
consumer side the proxy enters dual-emit mode and the listener
union-joins the active + successor group sets. The pilot side floor is
`TransitionEpoch ≥ now + 2 × AnnounceInterval` (enforced at
`config.Load`).

> **Note:** `-live-resharding` is a **consumer-side** knob (proxy/listener),
> not a shard-manifest flag. shard-manifest only emits the Successor block
> via the `-successor-*` flags below; consumers gate whether they act on it.

| Flag                            | Env                          | Default | Description                                                                                                                                  |
| ------------------------------- | ---------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `-successor-generation-id`      | `SUCCESSOR_GENERATION_ID`    | `""`    | 16-byte hex; non-empty triggers Successor-block emission. All other `-successor-*` flags below are required when set.                         |
| `-successor-shard-bits`         | `SUCCESSOR_SHARD_BITS`       | `0`     | Incoming generation `ShardBits`; MUST be within ±1 of `-shard-bits` per BRC-139 — equal is allowed (a generation may turn over without a width change). |
| `-successor-source-mode`        | `SUCCESSOR_SOURCE_MODE`      | `""`    | `asm` / `ssm`; empty inherits `-source-mode`.                                                                                                  |
| `-successor-transition-epoch`   | `SUCCESSOR_TRANSITION_EPOCH` | `0`     | Unix seconds at which the successor becomes the sole active generation. MUST be `≥ now + 2 × AnnounceInterval`; the daemon rejects otherwise. |

After `TransitionEpoch`, the operator rolls `-generation-id` forward
to what was `-successor-generation-id` and clears the `-successor-*`
flags so the manifest reverts to single-generation steady state.

## Cadence

| Flag                  | Env                  | Default   | Description                                                       |
| --------------------- | -------------------- | --------- | ----------------------------------------------------------------- |
| `-announce-interval`  | `ANNOUNCE_INTERVAL`  | `300s`    | Time between sends. Each send is jittered by ±10 %.               |
| `-ttl`                | `TTL`                | `0s`      | Go duration (e.g. `-ttl=900s`); encoded on the wire as whole seconds. `0` = consumer applies its default (3× interval). |

## Observability

| Flag                | Env             | Default       | Description                                            |
| ------------------- | --------------- | ------------- | ------------------------------------------------------ |
| `-metrics-addr`     | `METRICS_ADDR`  | `[::]:9091`   | HTTP listener for `/metrics`, `/healthz`, `/readyz`.   |
| `-otlp-endpoint`    | `OTLP_ENDPOINT` | `""`          | OTLP gRPC endpoint (e.g. `otel-collector:4317`). Empty disables OTLP push. |
| `-otlp-interval`    | `OTLP_INTERVAL` | `15s`         | OTLP push interval.                                    |
| `-log-format`       | `LOG_FORMAT`    | `json`        | Log output: `text` (stderr) or `json` (stdout, default for this daemon). Lines carry `service.{name,instance.id,version}` shared with OTLP metrics. See [Unified Logging](https://github.com/lightwebinc/shard-common/blob/main/docs/logging.md). |
| `-log-level`        | `LOG_LEVEL`     | `info`        | `debug` \| `info` \| `warn` \| `error`. Runtime-togglable via `POST /loglevel` and SIGHUP. |
| `-trace-sampling`   | `TRACE_SAMPLING`| `0`           | Trace head sampling ratio `0`–`1` (`0` = no-op tracer; exports via `-otlp-endpoint`). Startup emits a one-shot `host.inventory` event + `bsm_host_info` gauge. |
| `-debug`            | `DEBUG`         | `false`       | Deprecated alias for `-log-level=debug`.               |

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
shard-manifest \
  -shard-bits=4 \
  -joined-groups=all \
  -role-hint=proxy \
  -manifest-scope=site,global \
  -iface=enp6s0 \
  -generation-id=00112233445566778899aabbccddeeff
```

## Example: listener on shard_bits=4 joined to two specific groups

```bash
shard-manifest \
  -shard-bits=4 \
  -joined-groups=0x3,0x7 \
  -role-hint=listener \
  -manifest-scope=site \
  -bitmap=list
```

## BRC-148 Domains descriptors

| Flag / Env | Default | Description |
|------------|---------|-------------|
| `-domain` (repeatable) / `DOMAINS` (comma-separated) | — | One BRC-148 plane descriptor per entry: `id:bits=N[:ssm][:active][:slotspan=S][:generation=HEX32][:succbits=N:succepoch=T[:succgen=HEX32][:succssm]]`. The trailing `succ*` tokens announce a per-domain in-flight generation transition (Successor block) — the announcer must be `-authoritative` and `succbits` must be within ±1 of `bits` (enforced at encode). Sets `DomainsValid` (BRC-139 flags bit 7) and appends the Domains section after the successor block. SlotSpan defaults to the implied span (`ceil(2^bits/4096)`). Wire-level constraints (unique IDs ≤ 0x0E, slot overlap, control-plane bound, domain-0 top-level agreement) are enforced at encode time |

Per-domain successor announcements are supported (the `succ*` tokens above); the consumer evaluator adopts them per domain.
