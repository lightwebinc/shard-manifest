package config

import (
	"net"
	"os"
	"testing"
	"time"
)

func loopbackIface(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		t.Skip("no interfaces")
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 {
			return ifc.Name
		}
	}
	return ifaces[0].Name
}

// loadWithArgs swaps os.Args for the duration of one Load call. Load uses its
// own FlagSet that parses os.Args[1:], so resetting flag.CommandLine is not
// needed. Not safe for t.Parallel.
func loadWithArgs(t *testing.T, args ...string) (*Config, error) {
	t.Helper()
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = append([]string{"shard-manifest"}, args...)
	return Load()
}

func TestLoad_Defaults(t *testing.T) {
	c, err := loadWithArgs(t)
	if err != nil {
		t.Fatalf("default load: %v", err)
	}
	if c.SourceMode != "asm" {
		t.Errorf("source-mode = %q, want asm", c.SourceMode)
	}
	if c.Port != 9001 {
		t.Errorf("port = %d, want 9001", c.Port)
	}
	if c.AnnounceInterval != 300*time.Second {
		t.Errorf("announce-interval = %v", c.AnnounceInterval)
	}
	if c.InstanceID == "" {
		t.Error("instance-id should default to hostname")
	}
	if c.Encoding != EncodingAuto {
		t.Errorf("encoding = %v, want auto", c.Encoding)
	}
}

func TestLoad_ShardBitsExceedsMax(t *testing.T) {
	if _, err := loadWithArgs(t, "-shard-bits=13"); err == nil {
		t.Error("shard-bits > MaxShardBits should error")
	}
}

func TestLoad_EncodingForms(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want EncodingForm
	}{
		{"auto", EncodingAuto},
		{"list", EncodingList},
		{"bitmap", EncodingBitmap},
	} {
		c, err := loadWithArgs(t, "-bitmap="+tc.flag)
		if err != nil {
			t.Fatalf("bitmap=%s: %v", tc.flag, err)
		}
		if c.Encoding != tc.want {
			t.Errorf("bitmap=%s → %v, want %v", tc.flag, c.Encoding, tc.want)
		}
	}
	if _, err := loadWithArgs(t, "-bitmap=bogus"); err == nil {
		t.Error("invalid bitmap form should error")
	}
}

func TestLoad_InvalidScalars(t *testing.T) {
	cases := map[string][]string{
		"role-hint":        {"-role-hint=wizard"},
		"generation-id":    {"-generation-id=xyz"},
		"port-low":         {"-port=0"},
		"port-high":        {"-port=70000"},
		"manifest-scope":   {"-manifest-scope=bogus"},
		"mc-group-id":      {"-mc-group-id=zzzz"},
		"announce-zero":    {"-announce-interval=0"},
		"ttl-negative":     {"-ttl=-1s"},
		"source-mode":      {"-source-mode=bogus"},
		"publishers-refr":  {"-publishers-refresh=0"},
		"joined-bad-index": {"-shard-bits=2", "-joined-groups=9"},
	}
	for name, args := range cases {
		if _, err := loadWithArgs(t, args...); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoad_SSMRequiresPublishers(t *testing.T) {
	if _, err := loadWithArgs(t, "-source-mode=ssm"); err == nil {
		t.Error("ssm without publishers should error")
	}
	c, err := loadWithArgs(t, "-source-mode=ssm", "-publishers=2001:db8::1,2001:db8::2")
	if err != nil {
		t.Fatalf("ssm with publishers: %v", err)
	}
	if c.SourceMode != "ssm" || len(c.Publishers) != 2 {
		t.Errorf("ssm config: mode=%q publishers=%v", c.SourceMode, c.Publishers)
	}
}

func TestLoad_PilotOnlyImpliesAuthoritative(t *testing.T) {
	c, err := loadWithArgs(t, "-pilot-only")
	if err != nil {
		t.Fatalf("pilot-only: %v", err)
	}
	if !c.PilotOnly || !c.Authoritative {
		t.Errorf("pilot-only must promote authoritative: pilot=%v auth=%v",
			c.PilotOnly, c.Authoritative)
	}
}

func TestLoad_JoinedGroupsAndAll(t *testing.T) {
	c, err := loadWithArgs(t, "-shard-bits=3", "-joined-groups=2,0,5")
	if err != nil {
		t.Fatalf("joined-groups: %v", err)
	}
	// parseJoinedGroups sorts ascending.
	if len(c.JoinedGroups) != 3 || c.JoinedGroups[0] != 0 || c.JoinedGroups[2] != 5 {
		t.Errorf("joined-groups = %v", c.JoinedGroups)
	}
	if c.JoinAll {
		t.Error("JoinAll should be false")
	}
	c, err = loadWithArgs(t, "-shard-bits=3", "-joined-groups=all")
	if err != nil {
		t.Fatalf("joined-groups=all: %v", err)
	}
	if !c.JoinAll {
		t.Error("JoinAll should be true for 'all'")
	}
}

func TestLoad_SuccessorFlagsWithoutGenID(t *testing.T) {
	if _, err := loadWithArgs(t, "-successor-shard-bits=3"); err == nil {
		t.Error("successor flags without -successor-generation-id should error")
	}
}

func TestLoad_SuccessorValid(t *testing.T) {
	gen := "0123456789abcdef0123456789abcdef"
	c, err := loadWithArgs(t,
		"-shard-bits=2",
		"-successor-generation-id="+gen,
		"-successor-shard-bits=3",
		"-successor-transition-epoch=9999999999")
	if err != nil {
		t.Fatalf("valid successor load: %v", err)
	}
	if c.Successor == nil {
		t.Fatal("Successor block should be populated")
	}
	if c.Successor.ShardBits != 3 {
		t.Errorf("successor shard-bits = %d, want 3", c.Successor.ShardBits)
	}
	if c.Successor.SourceModeSSM {
		t.Error("successor should inherit asm (non-SSM) source mode")
	}
}

// The bound is |successor − active| ≤ 1, which INCLUDES equality: a
// generation can turn over (new GenerationID) without a width change. The
// flag help says so; this pins the code the help describes.
func TestLoad_SuccessorEqualShardBitsAllowed(t *testing.T) {
	gen := "0123456789abcdef0123456789abcdef"
	c, err := loadWithArgs(t,
		"-shard-bits=2",
		"-successor-generation-id="+gen,
		"-successor-shard-bits=2",
		"-successor-transition-epoch=9999999999")
	if err != nil {
		t.Fatalf("equal successor shard-bits must be accepted: %v", err)
	}
	if c.Successor == nil || c.Successor.ShardBits != 2 {
		t.Fatalf("Successor = %+v, want ShardBits 2", c.Successor)
	}
	// Two bits away is still rejected.
	if _, err := loadWithArgs(t,
		"-shard-bits=2",
		"-successor-generation-id="+gen,
		"-successor-shard-bits=4",
		"-successor-transition-epoch=9999999999"); err == nil {
		t.Error("|successor - active| = 2 should error")
	}
}

func TestResolveIface_Explicit(t *testing.T) {
	c := &Config{Iface: loopbackIface(t)}
	ifc, err := c.ResolveIface()
	if err != nil {
		t.Fatalf("resolve explicit iface: %v", err)
	}
	if ifc == nil || ifc.Name != c.Iface {
		t.Errorf("resolved iface = %v, want %q", ifc, c.Iface)
	}
	// Nonexistent name must error.
	bad := &Config{Iface: "definitely-not-real0"}
	if _, err := bad.ResolveIface(); err == nil {
		t.Error("nonexistent iface should error")
	}
}

func TestPrimaryIPv6_Loopback(t *testing.T) {
	c := &Config{Iface: loopbackIface(t)}
	ifc, err := c.ResolveIface()
	if err != nil {
		t.Skip("loopback not resolvable")
	}
	ip, err := c.PrimaryIPv6(ifc)
	if err != nil {
		t.Fatalf("primary ipv6: %v", err)
	}
	// Loopback carries no global-unicast IPv6, so the function falls back to
	// the unspecified address rather than erroring.
	if ip == nil {
		t.Error("expected a non-nil fallback address")
	}
}
