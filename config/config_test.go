package config

import (
	"testing"
	"time"
)

func TestParseJoinedGroups(t *testing.T) {
	g, all, err := parseJoinedGroups("0x0,0x1,3,5", 4)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if all {
		t.Errorf("all should be false")
	}
	want := []uint16{0, 1, 3, 5}
	if len(g) != len(want) {
		t.Fatalf("len = %d, want %d", len(g), len(want))
	}
	for i, v := range want {
		if g[i] != v {
			t.Errorf("g[%d] = %d, want %d", i, g[i], v)
		}
	}
}

func TestParseJoinedGroups_All(t *testing.T) {
	_, all, err := parseJoinedGroups("all", 4)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !all {
		t.Errorf("all should be true")
	}
}

func TestParseJoinedGroups_OutOfRange(t *testing.T) {
	_, _, err := parseJoinedGroups("16", 4)
	if err == nil {
		t.Errorf("expected out-of-range error")
	}
}

func TestParseJoinedGroups_Duplicate(t *testing.T) {
	_, _, err := parseJoinedGroups("1,1", 4)
	if err == nil {
		t.Errorf("expected duplicate error")
	}
}

func TestParseGenerationID(t *testing.T) {
	g, err := parseGenerationID("00112233-4455-6677-8899-AABBCCDDEEFF")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	for i, b := range want {
		if g[i] != b {
			t.Errorf("g[%d] = 0x%02X, want 0x%02X", i, g[i], b)
		}
	}
}

func TestParseGenerationID_Empty(t *testing.T) {
	g, err := parseGenerationID("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i, b := range g {
		if b != 0 {
			t.Errorf("g[%d] = 0x%02X, want 0", i, b)
		}
	}
}

func TestParseGenerationID_BadLength(t *testing.T) {
	if _, err := parseGenerationID("deadbeef"); err == nil {
		t.Errorf("expected length error")
	}
}

func TestParseUint16(t *testing.T) {
	cases := []struct {
		in   string
		want uint16
	}{
		{"0", 0},
		{"123", 123},
		{"0x10", 16},
		{"0XFF", 255},
	}
	for _, c := range cases {
		v, err := parseUint16(c.in)
		if err != nil {
			t.Errorf("%q: err %v", c.in, err)
			continue
		}
		if v != c.want {
			t.Errorf("%q: got %d, want %d", c.in, v, c.want)
		}
	}
}

func TestEncodingFormForGroups(t *testing.T) {
	c := &Config{Encoding: EncodingAuto}
	if got := c.EncodingFormForGroups(10); got != EncodingList {
		t.Errorf("auto/10: got %v, want list", got)
	}
	if got := c.EncodingFormForGroups(thresholdListEntries + 1); got != EncodingBitmap {
		t.Errorf("auto/many: got %v, want bitmap", got)
	}
	c.Encoding = EncodingList
	if got := c.EncodingFormForGroups(10000); got != EncodingList {
		t.Errorf("forced list: got %v", got)
	}
	c.Encoding = EncodingBitmap
	if got := c.EncodingFormForGroups(1); got != EncodingBitmap {
		t.Errorf("forced bitmap: got %v", got)
	}
}

func TestScopePrefixes(t *testing.T) {
	c := &Config{ManifestScope: "site,global"}
	got, err := c.ScopePrefixes()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 || got[0] != 0xFF05 || got[1] != 0xFF0E {
		t.Errorf("got %v, want [FF05 FF0E]", got)
	}
}

func TestScopePrefixes_Bad(t *testing.T) {
	c := &Config{ManifestScope: "bogus"}
	if _, err := c.ScopePrefixes(); err == nil {
		t.Errorf("expected error")
	}
}

func TestEnvHelpers(t *testing.T) {
	t.Setenv("X_INT", "42")
	t.Setenv("X_BOOL", "true")
	t.Setenv("X_DUR", "150ms")
	if envInt("X_INT", 0) != 42 {
		t.Error("envInt")
	}
	if !envBool("X_BOOL", false) {
		t.Error("envBool")
	}
	if envDuration("X_DUR", time.Second) != 150*time.Millisecond {
		t.Error("envDuration")
	}
	if envOrDefault("X_MISSING", "def") != "def" {
		t.Error("envOrDefault")
	}
}

func TestParseSuccessor_OK(t *testing.T) {
	// active=8, successor=9 (+1), epoch sufficiently in the future.
	now := time.Now().Unix()
	epoch := int(now) + 3600 // 1 hour ahead
	sc, err := parseSuccessor(
		"00112233445566778899AABBCCDDEEFF",
		9, "ssm", epoch,
		8, "asm", 300*time.Second,
	)
	if err != nil {
		t.Fatalf("parseSuccessor: %v", err)
	}
	if sc.ShardBits != 9 {
		t.Errorf("ShardBits = %d, want 9", sc.ShardBits)
	}
	if !sc.SourceModeSSM {
		t.Errorf("SourceModeSSM = false, want true")
	}
	if sc.TransitionEpoch != uint32(epoch) {
		t.Errorf("TransitionEpoch = %d, want %d", sc.TransitionEpoch, epoch)
	}
}

func TestParseSuccessor_RejectsShiftAboveOne(t *testing.T) {
	// active=8, successor=10 → |10-8|=2 must reject.
	epoch := int(time.Now().Unix()) + 3600
	_, err := parseSuccessor(
		"00112233445566778899AABBCCDDEEFF",
		10, "asm", epoch,
		8, "asm", 300*time.Second,
	)
	if err == nil {
		t.Fatalf("expected error for shift-of-2, got nil")
	}
}

func TestParseSuccessor_RejectsBelowFloor(t *testing.T) {
	// epoch < now + 2 × AnnounceInterval must reject.
	now := time.Now().Unix()
	epoch := int(now) + 60 // only 60s ahead vs 2×300s floor
	_, err := parseSuccessor(
		"00112233445566778899AABBCCDDEEFF",
		9, "asm", epoch,
		8, "asm", 300*time.Second,
	)
	if err == nil {
		t.Fatalf("expected floor-rejection error, got nil")
	}
}

func TestParseSuccessor_InheritsActiveSourceMode(t *testing.T) {
	epoch := int(time.Now().Unix()) + 3600
	sc, err := parseSuccessor(
		"00112233445566778899AABBCCDDEEFF",
		9, "", // empty mode ⇒ inherit
		epoch,
		8, "ssm", 300*time.Second,
	)
	if err != nil {
		t.Fatalf("parseSuccessor: %v", err)
	}
	if !sc.SourceModeSSM {
		t.Errorf("expected to inherit SSM from active, got ASM")
	}
}

func TestParseSuccessor_MissingEpoch(t *testing.T) {
	_, err := parseSuccessor(
		"00112233445566778899AABBCCDDEEFF",
		9, "asm", 0,
		8, "asm", 300*time.Second,
	)
	if err == nil {
		t.Errorf("expected error for missing epoch")
	}
}
