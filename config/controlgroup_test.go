package config

import (
	"net"
	"testing"

	"github.com/lightwebinc/shard-common/shard"
)

func groupAddr(prefix uint16) net.IP {
	return shard.GroupAddr(prefix, shard.DefaultGroupID, shard.GroupBeacon)
}

// THE DEFECT. BRC-126 §Beacon Scopes and BRC-129 §Source Mode and Address
// Range both require the 0xFFFD control group to take the source-specific
// FF3x prefix under SSM. Every release before this one derived the manifest
// destination from -manifest-scope alone and produced FF05::B:FFFD whatever
// -source-mode said.
func TestControlGroupPrefixes_SSMUsesSourceSpecificPrefix(t *testing.T) {
	for _, tc := range []struct {
		scope string
		want  uint16
		addr  string
	}{
		{"site", 0xFF35, "ff35::b:fffd"},
		{"global", 0xFF3E, "ff3e::b:fffd"},
	} {
		got, err := ControlGroupPrefixes(ControlGroupDerived, "ssm", tc.scope)
		if err != nil {
			t.Fatalf("scope %s: %v", tc.scope, err)
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("scope %s under ssm: prefixes = %#04x, want [%#04x]", tc.scope, got, tc.want)
		}
		if ip := groupAddr(got[0]); !ip.Equal(net.ParseIP(tc.addr)) {
			t.Errorf("scope %s under ssm: group = %s, want %s", tc.scope, ip, tc.addr)
		}
	}
}

// The same defect through the Config the sender actually reads.
func TestControlGroupDestPrefixes_SSMSiteIsFF35(t *testing.T) {
	c := &Config{ManifestScope: "site", SourceMode: "ssm", ControlGroupCompat: ControlGroupDerived}
	got, err := c.ControlGroupDestPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 0xFF35 {
		t.Fatalf("derived dest prefixes = %#04x, want [0xff35]", got)
	}
	// ScopePrefixes stays the raw any-source lookup: it is what "legacy"
	// means, and the two must not be confused.
	raw, err := c.ScopePrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw[0] != 0xFF05 {
		t.Fatalf("ScopePrefixes = %#04x, want [0xff05]", raw)
	}
}

// ASM is unchanged in every compat mode.
func TestControlGroupPrefixes_ASMUnchanged(t *testing.T) {
	for scope, want := range Scopes {
		for _, compat := range ControlGroupCompatValues {
			got, err := ControlGroupPrefixes(compat, "asm", scope)
			if err != nil {
				t.Fatalf("compat %s scope %s: %v", compat, scope, err)
			}
			if len(got) != 1 || got[0] != want {
				t.Errorf("compat %s scope %s under asm: prefixes = %#04x, want [%#04x]",
					compat, scope, got, want)
			}
		}
	}
}

// The sender-safe default. "asm-only" must never consult -source-mode: it
// is what keeps an un-upgraded consumer reaching pilot quorum on upgrade.
func TestControlGroupDestPrefixes_DefaultIsSenderSafe(t *testing.T) {
	c := &Config{ManifestScope: "site", SourceMode: "ssm", ControlGroupCompat: ControlGroupASMOnly}
	got, err := c.ControlGroupDestPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 0xFF05 {
		t.Fatalf("asm-only under ssm: prefixes = %#04x, want [0xff05]", got)
	}
}

func TestControlGroupDestPrefixes_BothSpansTheFlagDay(t *testing.T) {
	c := &Config{ManifestScope: "site", SourceMode: "ssm", ControlGroupCompat: ControlGroupBoth}
	got, err := c.ControlGroupDestPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 0xFF05 || got[1] != 0xFF35 {
		t.Fatalf("both under ssm: prefixes = %#04x, want [0xff05 0xff35]", got)
	}
}

// A multi-scope -manifest-scope keeps operator order, legacy prefix first
// within each scope, and never repeats a prefix.
func TestControlGroupDestPrefixes_MultiScopeOrderAndDedup(t *testing.T) {
	c := &Config{ManifestScope: "site,global", SourceMode: "ssm", ControlGroupCompat: ControlGroupBoth}
	got, err := c.ControlGroupDestPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{0xFF05, 0xFF35, 0xFF0E, 0xFF3E}
	if len(got) != len(want) {
		t.Fatalf("prefixes = %#04x, want %#04x", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("prefixes = %#04x, want %#04x", got, want)
		}
	}

	// Duplicated scope names collapse rather than double-sending.
	c = &Config{ManifestScope: "site,site", SourceMode: "ssm", ControlGroupCompat: ControlGroupBoth}
	got, err = c.ControlGroupDestPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("prefixes = %#04x, want 2 after dedup", got)
	}
}

// BRC-129 tables an SSM control group at site and global only, so an org or
// link manifest scope cannot be made conformant. "derived" fails at load
// with a message naming the fix rather than quietly falling back to FF08.
func TestControlGroupDestPrefixes_DerivedRejectsScopesWithNoSSMGroup(t *testing.T) {
	for _, scope := range []string{"link", "org", "site,org"} {
		c := &Config{ManifestScope: scope, SourceMode: "ssm", ControlGroupCompat: ControlGroupDerived}
		if _, err := c.ControlGroupDestPrefixes(); err == nil {
			t.Errorf("derived scope %s under ssm: want error, got none", scope)
		}
		// asm-only and both still work, so an existing deployment upgrades
		// without tripping over it.
		for _, compat := range []string{ControlGroupASMOnly, ControlGroupBoth} {
			c := &Config{ManifestScope: scope, SourceMode: "ssm", ControlGroupCompat: compat}
			if _, err := c.ControlGroupDestPrefixes(); err != nil {
				t.Errorf("compat %s scope %s under ssm: %v", compat, scope, err)
			}
		}
	}
}

// "both" adds the SSM form only where BRC-129 defines one: site gains FF35,
// org keeps FF08 alone.
func TestControlGroupDestPrefixes_BothDegradesPerScope(t *testing.T) {
	c := &Config{ManifestScope: "site,org", SourceMode: "ssm", ControlGroupCompat: ControlGroupBoth}
	got, err := c.ControlGroupDestPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{0xFF05, 0xFF35, 0xFF08}
	if len(got) != len(want) {
		t.Fatalf("prefixes = %#04x, want %#04x", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("prefixes = %#04x, want %#04x", got, want)
		}
	}
}

func TestControlGroupPrefixes_Invalid(t *testing.T) {
	if _, err := ControlGroupPrefixes("sometimes", "ssm", "site"); err == nil {
		t.Error("unknown compat mode: want error, got none")
	}
	if _, err := ControlGroupPrefixes(ControlGroupDerived, "ssm", "nonsense"); err == nil {
		t.Error("unknown scope: want error, got none")
	}
	c := &Config{ManifestScope: "", SourceMode: "asm", ControlGroupCompat: ControlGroupASMOnly}
	if _, err := c.ControlGroupDestPrefixes(); err == nil {
		t.Error("empty manifest-scope: want error, got none")
	}
}

// The derived prefix must come from the shared helper the data plane uses,
// not a second table.
func TestControlGroupPrefixes_MatchesSharedHelper(t *testing.T) {
	for scopeName, scope := range map[string]shard.Scope{
		"site":   shard.ScopeSite,
		"global": shard.ScopeGlobal,
	} {
		want, err := shard.Prefix(shard.SourceModeSSM, scope)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ControlGroupPrefixes(ControlGroupDerived, "ssm", scopeName)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != want {
			t.Errorf("scope %s: derived %#04x, shard.Prefix says %#04x", scopeName, got[0], want)
		}
	}
}
