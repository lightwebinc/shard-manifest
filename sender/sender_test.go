package sender

import (
	"math/rand"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-shard-common/frame"

	"github.com/lightwebinc/bitcoin-shard-manifest/config"
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
