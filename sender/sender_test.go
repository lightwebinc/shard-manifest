package sender

import (
	"math/rand"
	"testing"
	"time"

	"github.com/lightwebinc/shard-common/frame"

	"github.com/lightwebinc/shard-manifest/config"
)

func newSender(c *config.Config) *Sender {
	return &Sender{
		cfg:        c,
		log:        nil, //nolint:staticcheck // tests don't exercise the logger
		instanceID: 0xDEADBEEF,
	}
}

func TestBuildManifest_List(t *testing.T) {
	c := &config.Config{
		ShardBits:        4,
		JoinedGroups:     []uint16{0, 3, 5},
		Encoding:         config.EncodingAuto,
		RoleHint:         frame.RoleHintProxy,
		AnnounceInterval: 300 * time.Second,
		TTL:              900 * time.Second,
	}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagGroupsValid == 0 {
		t.Errorf("GroupsValid not set")
	}
	if len(m.Groups) != 3 {
		t.Errorf("Groups len = %d, want 3", len(m.Groups))
	}
	if len(m.Bitmap) != 0 {
		t.Errorf("Bitmap should be empty in list form")
	}
	if m.AnnounceInterval != 300 {
		t.Errorf("AnnounceInterval = %d, want 300", m.AnnounceInterval)
	}
	if m.TTL != 900 {
		t.Errorf("TTL = %d, want 900", m.TTL)
	}
	if m.RoleHint != frame.RoleHintProxy {
		t.Errorf("RoleHint = %d", m.RoleHint)
	}
}

func TestBuildManifest_Bitmap(t *testing.T) {
	c := &config.Config{
		ShardBits: 8,
		JoinAll:   true,
		Encoding:  config.EncodingAuto,
		RoleHint:  frame.RoleHintListener,
	}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagGroupsValid == 0 {
		t.Errorf("GroupsValid not set")
	}
	if len(m.Bitmap) != 256/8 {
		t.Errorf("Bitmap len = %d, want %d", len(m.Bitmap), 256/8)
	}
	for i, b := range m.Bitmap {
		if b != 0xFF {
			t.Errorf("Bitmap[%d] = 0x%02X, want 0xFF", i, b)
		}
	}
	if len(m.Groups) != 0 {
		t.Errorf("Groups should be empty in bitmap form")
	}
}

func TestBuildManifest_IdentityOnly(t *testing.T) {
	c := &config.Config{ShardBits: 2}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags != 0 {
		t.Errorf("Flags = 0x%02X, want 0", m.Flags)
	}
}

func TestBuildManifest_ShutdownFlag(t *testing.T) {
	c := &config.Config{ShardBits: 2, Authoritative: true}
	s := newSender(c)
	m, err := s.buildManifest(true)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagShutdown == 0 {
		t.Errorf("Shutdown flag not set")
	}
	if m.Flags&frame.ShardManifestFlagAuthoritative == 0 {
		t.Errorf("Authoritative flag not set")
	}
}

func TestBuildManifest_RoundTripsThroughEncoder(t *testing.T) {
	c := &config.Config{
		ShardBits:        4,
		JoinedGroups:     []uint16{0, 1, 2, 3, 4, 5, 6, 7},
		Encoding:         config.EncodingAuto,
		AnnounceInterval: 300 * time.Second,
	}
	s := newSender(c)
	m, _ := s.buildManifest(false)
	buf := make([]byte, frame.ShardManifestSize(m))
	if _, err := frame.EncodeShardManifest(m, buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := frame.DecodeShardManifest(buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Groups) != len(c.JoinedGroups) {
		t.Errorf("decoded groups = %d, want %d", len(got.Groups), len(c.JoinedGroups))
	}
}

func TestBitmapBytes(t *testing.T) {
	cases := []struct {
		bits uint8
		want int
	}{
		{0, 1},
		{3, 1},
		{8, 32},
		{12, 512},
	}
	for _, c := range cases {
		if got := bitmapBytes(c.bits); got != c.want {
			t.Errorf("bitmapBytes(%d) = %d, want %d", c.bits, got, c.want)
		}
	}
}

func TestBuildManifest_SSM_SourceModeFlag(t *testing.T) {
	c := &config.Config{
		SourceMode:       "ssm",
		ShardBits:        4,
		Encoding:         config.EncodingAuto,
		RoleHint:         frame.RoleHintManifestOnly,
		AnnounceInterval: 60 * time.Second,
	}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagSourceModeSSM == 0 {
		t.Error("SourceModeSSM flag not set when cfg.SourceMode=ssm")
	}
	if m.Flags&frame.ShardManifestFlagSourcesValid != 0 {
		t.Error("SourcesValid set with no resolved publishers")
	}
	if len(m.Sources) != 0 {
		t.Errorf("Sources = %v, want empty", m.Sources)
	}
}

func TestBuildManifest_SSM_WithSources(t *testing.T) {
	c := &config.Config{
		SourceMode:       "ssm",
		ShardBits:        4,
		Encoding:         config.EncodingAuto,
		RoleHint:         frame.RoleHintManifestOnly,
		AnnounceInterval: 60 * time.Second,
	}
	s := newSender(c)
	// Simulate the publisher resolver having populated the cache.
	s.sources = [][16]byte{
		{0xFD, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01},
		{0xFD, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02},
	}
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagSourceModeSSM == 0 {
		t.Error("SourceModeSSM flag not set")
	}
	if m.Flags&frame.ShardManifestFlagSourcesValid == 0 {
		t.Error("SourcesValid flag not set when sources are populated")
	}
	if len(m.Sources) != 2 {
		t.Fatalf("Sources len = %d, want 2", len(m.Sources))
	}
	// Round-trip through the encoder/decoder.
	buf := make([]byte, frame.ShardManifestSize(m))
	if _, err := frame.EncodeShardManifest(m, buf); err != nil {
		t.Fatalf("EncodeShardManifest: %v", err)
	}
	got, err := frame.DecodeShardManifest(buf)
	if err != nil {
		t.Fatalf("DecodeShardManifest: %v", err)
	}
	if len(got.Sources) != 2 {
		t.Errorf("decoded Sources len = %d, want 2", len(got.Sources))
	}
}

func TestBuildManifest_ASM_NoSSMFlags(t *testing.T) {
	c := &config.Config{
		SourceMode:       "asm",
		ShardBits:        4,
		Encoding:         config.EncodingAuto,
		RoleHint:         frame.RoleHintManifestOnly,
		AnnounceInterval: 60 * time.Second,
	}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagSourceModeSSM != 0 {
		t.Error("SourceModeSSM flag should not be set when cfg.SourceMode=asm")
	}
}

func TestJitterBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	d := 100 * time.Millisecond
	for i := 0; i < 1000; i++ {
		j := jitter(d, 0.10, rng)
		if j < 90*time.Millisecond || j > 110*time.Millisecond {
			t.Fatalf("jitter out of range: %v", j)
		}
	}
}

func TestBuildManifest_PilotOnlyFlag(t *testing.T) {
	c := &config.Config{
		ShardBits:        8,
		JoinedGroups:     []uint16{0, 1, 2, 3},
		Encoding:         config.EncodingAuto,
		AnnounceInterval: 300 * time.Second,
		Authoritative:    true,
		PilotOnly:        true,
	}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagPilotOnly == 0 {
		t.Errorf("PilotOnly flag not set on emitted manifest")
	}
	if m.Flags&frame.ShardManifestFlagAuthoritative == 0 {
		t.Errorf("Authoritative not set (PilotOnly requires it)")
	}
}

func TestBuildManifest_SuccessorBlockEmitted(t *testing.T) {
	successorGenID := [16]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	c := &config.Config{
		ShardBits:        8,
		JoinedGroups:     []uint16{0, 1, 2},
		Encoding:         config.EncodingAuto,
		AnnounceInterval: 300 * time.Second,
		Authoritative:    true,
		PilotOnly:        true,
		Successor: &config.SuccessorConfig{
			GenerationID:    successorGenID,
			ShardBits:       9,
			SourceModeSSM:   true,
			TransitionEpoch: 1746900000,
		},
	}
	s := newSender(c)
	m, err := s.buildManifest(false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.Flags&frame.ShardManifestFlagSuccessorValid == 0 {
		t.Fatalf("SuccessorValid flag not set")
	}
	if m.Successor == nil {
		t.Fatalf("Successor block nil")
	}
	if m.Successor.ShardBits != 9 {
		t.Errorf("Successor.ShardBits = %d, want 9", m.Successor.ShardBits)
	}
	if m.Successor.GenerationID != successorGenID {
		t.Errorf("Successor.GenerationID mismatch")
	}
	if m.Successor.Flags&frame.SuccessorFlagSourceModeSSM == 0 {
		t.Errorf("Successor.Flags missing SourceModeSSM")
	}
	if m.Successor.TransitionEpoch != 1746900000 {
		t.Errorf("Successor.TransitionEpoch = %d", m.Successor.TransitionEpoch)
	}

	// Round-trip through wire to confirm consumer-side compatibility.
	buf := make([]byte, frame.ShardManifestSize(m))
	if _, err := frame.EncodeShardManifest(m, buf); err != nil {
		t.Fatalf("EncodeShardManifest: %v", err)
	}
	got, err := frame.DecodeShardManifest(buf)
	if err != nil {
		t.Fatalf("DecodeShardManifest: %v", err)
	}
	if got.Successor == nil || got.Successor.ShardBits != 9 {
		t.Errorf("round-trip lost Successor: %+v", got.Successor)
	}
}

// silence unused import for rand in older test versions.
var _ = rand.New
