package sender

import (
	"net"
	"testing"

	"github.com/lightwebinc/shard-common/shard"

	"github.com/lightwebinc/shard-manifest/config"
)

// THE DEFECT, at the socket the announcer actually writes to. BRC-126
// §Beacon Scopes and BRC-129 §Source Mode and Address Range require the
// 0xFFFD control group to take the source-specific FF3x prefix under SSM;
// this used to be derived from -manifest-scope alone and always produced
// FF05::B:FFFD.
func TestBuildDests_SSMSendsToSourceSpecificGroup(t *testing.T) {
	dests, err := buildDests(&config.Config{
		ManifestScope:      "site",
		SourceMode:         "ssm",
		ControlGroupCompat: config.ControlGroupDerived,
		MCGroupID:          shard.DefaultGroupID,
		Port:               9001,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dests) != 1 {
		t.Fatalf("dests = %d, want 1", len(dests))
	}
	want := net.ParseIP("ff35::b:fffd")
	if !dests[0].IP.Equal(want) {
		t.Errorf("manifest destination = %s, want %s", dests[0].IP, want)
	}
	if dests[0].Port != 9001 {
		t.Errorf("manifest port = %d, want 9001", dests[0].Port)
	}
}

// The sender-safe default keeps the pre-fix destination, so upgrading this
// binary alone never moves the wire out from under an un-upgraded consumer.
func TestBuildDests_DefaultKeepsLegacyGroup(t *testing.T) {
	dests, err := buildDests(&config.Config{
		ManifestScope:      "site",
		SourceMode:         "ssm",
		ControlGroupCompat: config.ControlGroupASMOnly,
		MCGroupID:          shard.DefaultGroupID,
		Port:               9001,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dests) != 1 || !dests[0].IP.Equal(net.ParseIP("ff05::b:fffd")) {
		t.Fatalf("dests = %v, want only ff05::b:fffd", dests)
	}
}

// "both" writes each manifest into the group an un-upgraded consumer still
// joins AND the conformant one.
func TestBuildDests_BothSpansTheFlagDay(t *testing.T) {
	dests, err := buildDests(&config.Config{
		ManifestScope:      "site",
		SourceMode:         "ssm",
		ControlGroupCompat: config.ControlGroupBoth,
		MCGroupID:          shard.DefaultGroupID,
		Port:               9001,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dests) != 2 {
		t.Fatalf("dests = %d, want 2", len(dests))
	}
	for i, want := range []string{"ff05::b:fffd", "ff35::b:fffd"} {
		if !dests[i].IP.Equal(net.ParseIP(want)) {
			t.Errorf("dest %d = %s, want %s", i, dests[i].IP, want)
		}
		if dests[i].Port != 9001 {
			t.Errorf("dest %d port = %d, want 9001", i, dests[i].Port)
		}
	}
}

// A non-default -mc-group-id still lands in bytes 12-13; only the prefix
// moves under SSM.
func TestBuildDests_CustomGroupIDPreserved(t *testing.T) {
	dests, err := buildDests(&config.Config{
		ManifestScope:      "site",
		SourceMode:         "ssm",
		ControlGroupCompat: config.ControlGroupDerived,
		MCGroupID:          0xCAFE,
		Port:               9001,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dests[0].IP.Equal(net.ParseIP("ff35::cafe:fffd")) {
		t.Errorf("dest = %s, want ff35::cafe:fffd", dests[0].IP)
	}
}

func TestBuildDests_RejectsUnconfigurableScope(t *testing.T) {
	if _, err := buildDests(&config.Config{
		ManifestScope:      "org",
		SourceMode:         "ssm",
		ControlGroupCompat: config.ControlGroupDerived,
		MCGroupID:          shard.DefaultGroupID,
		Port:               9001,
	}); err == nil {
		t.Error("org scope under ssm+derived: want error, got none")
	}
}
