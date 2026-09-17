package config

import (
	"fmt"
	"strings"

	"github.com/lightwebinc/shard-common/shard"
)

// Control-plane group compatibility (BRC-126 §Beacon Scopes, BRC-129
// §Source Mode and Address Range).
//
// Both BRCs require the control-plane groups at index 0xFFFD — the BRC-126
// ADVERT beacon and the BRC-139 shard manifest, which share that group
// address and differ only by UDP port — to take the source-specific FF3x
// prefix when the fabric runs SSM: FF35::B:FFFD at site scope, FF3E::B:FFFD
// at global scope. Releases before this one derived the destination from
// -manifest-scope alone and always produced the any-source FF0x prefix,
// even with -source-mode=ssm.
//
// Correcting that moves a live group address, so it is a flag day: a
// listener joined to FF35::B:FFFD hears nothing from an announcer still
// sending to FF05::B:FFFD, and a manifest that lands on the wrong group
// raises no error anywhere — the symptom is a consumer that never reaches
// pilot quorum. This switch exists only to make that transition ordered and
// observable. It is temporary: once the fleet is on "derived" the flag and
// this file go away.
//
// ROLLOUT ORDER — the same note is in shard-listener and retry-endpoint:
//
//  1. Roll RECEIVERS (shard-listener, shard-proxy). Their default is
//     "both", so an upgraded receiver joins FF0x AND FF3x and hears
//     un-upgraded and upgraded announcers alike. No config change is needed
//     and there is no window in which discovery stops.
//  2. Roll SENDERS (retry-endpoint, shard-manifest) — this binary. The
//     default here is "asm-only", so upgrading does not move the wire.
//  3. One converge sets the SENDERS to "derived". Manifests move to FF3x,
//     which every receiver from step 1 already joined.
//  4. After a soak, one converge sets the RECEIVERS to "derived", dropping
//     the legacy join.
//
// Setting this announcer to "derived" before step 1 has covered every
// consumer is the one ordering that silently strands a peer. The default is
// picked so that doing nothing but upgrading binaries can never produce it.
//
// Under -source-mode=asm every mode collapses to the same FF0x prefixes, so
// this flag is a no-op for an ASM deployment.
const (
	// ControlGroupASMOnly always derives the any-source FF0x prefix from
	// the scope table, ignoring -source-mode. Pre-fix behaviour, and the
	// default for this sender.
	ControlGroupASMOnly = "asm-only"

	// ControlGroupBoth derives both prefixes: the announcer sends each
	// manifest to FF0x and FF3x. Transition value; use it when consumers
	// are known to be mixed and a second converge is not wanted.
	ControlGroupBoth = "both"

	// ControlGroupDerived derives the prefix from (-source-mode, scope)
	// per BRC-126/BRC-129. Conformant; the end state.
	ControlGroupDerived = "derived"
)

// ControlGroupCompatValues lists the accepted -control-group-compat values
// for flag help and validation.
var ControlGroupCompatValues = []string{ControlGroupASMOnly, ControlGroupBoth, ControlGroupDerived}

// ControlGroupPrefixes returns the upper-16-bit multicast prefixes the
// control-plane groups (index 0xFFFD) are derived from for one scope name,
// in the order they should be sent to: the legacy FF0x prefix first, the
// derived prefix second, deduplicated when they coincide.
//
// The SSM prefix comes from [shard.Prefix], the same helper the data plane
// uses — the scope table below is only consulted for the any-source form,
// which it already held.
//
// BRC-129 defines SSM control groups at site and global scope only (FF35
// and FF3E); "link" and "org" have no SSM control group. Under "derived" —
// an explicit request for the conformant address — asking for one at such a
// scope is a config error rather than a silent fall back to FF0x. Under
// "both" it degrades to the any-source prefix alone, so the receiver-safe
// default can never turn a binary upgrade into a startup failure. Under ASM
// all four scope names keep working exactly as before.
func ControlGroupPrefixes(compat, sourceMode, scopeName string) ([]uint16, error) {
	asm, ok := Scopes[scopeName]
	if !ok {
		return nil, fmt.Errorf("invalid manifest-scope %q (allowed: link,site,org,global)", scopeName)
	}

	switch compat {
	case ControlGroupASMOnly:
		// Never consults -source-mode: this is the pre-fix wire, kept
		// byte-for-byte so an un-upgraded consumer still hears us.
		return []uint16{asm}, nil
	case ControlGroupBoth, ControlGroupDerived:
	default:
		return nil, fmt.Errorf("invalid -control-group-compat %q (%s)",
			compat, strings.Join(ControlGroupCompatValues, "|"))
	}

	derived := asm
	if strings.EqualFold(sourceMode, "ssm") {
		sc, err := shard.ParseScope(scopeName)
		switch {
		case err != nil && compat == ControlGroupDerived:
			// Strict: the operator asked for the conformant address and
			// there isn't one at this scope. Say so rather than emitting
			// FF0x under a flag named "derived".
			return nil, fmt.Errorf("-control-group-compat=derived with -source-mode=ssm needs a manifest scope BRC-129 "+
				"defines an SSM control group for (site|global), got %q", scopeName)
		case err != nil:
			// Permissive: "both" means every form that exists, and at this
			// scope only the any-source one does. Falling back here is what
			// keeps the receiver-safe default from turning a binary upgrade
			// into a startup failure; the resolved group list is logged at
			// startup, so the outcome is visible.
		default:
			p, perr := shard.Prefix(shard.SourceModeSSM, sc)
			if perr != nil {
				return nil, perr
			}
			derived = p
		}
	}

	if compat == ControlGroupDerived || derived == asm {
		return []uint16{derived}, nil
	}
	return []uint16{asm, derived}, nil
}

// ControlGroupDestPrefixes resolves the prefixes this announcer sends to,
// from -control-group-compat, -source-mode and the -manifest-scope list.
// Order is scope-major (the order the operator listed them) and, within a
// scope, legacy prefix first.
//
// This supersedes [Config.ScopePrefixes] as the destination derivation:
// ScopePrefixes is the raw any-source scope-table lookup and stays that way
// (it is what "legacy" means here), while this is what the sender uses.
func (c *Config) ControlGroupDestPrefixes() ([]uint16, error) {
	names, err := c.scopeNames()
	if err != nil {
		return nil, err
	}
	out := make([]uint16, 0, 2*len(names))
	seen := make(map[uint16]struct{}, 2*len(names))
	for _, name := range names {
		ps, err := ControlGroupPrefixes(c.ControlGroupCompat, c.SourceMode, name)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out, nil
}

// scopeNames splits -manifest-scope into its individual scope names,
// preserving order.
func (c *Config) scopeNames() ([]string, error) {
	parts := strings.Split(c.ManifestScope, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("manifest-scope is empty")
	}
	return out, nil
}
