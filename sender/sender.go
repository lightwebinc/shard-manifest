// Package sender implements the BRC-137 ShardManifest sender loop. It
// builds and emits ShardManifest datagrams to one or more beacon multicast
// groups on a configurable cadence.
package sender

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/lightwebinc/shard-common/bootstrap"
	"github.com/lightwebinc/shard-common/frame"
	"github.com/lightwebinc/shard-common/shard"

	"github.com/lightwebinc/shard-manifest/config"
	"github.com/lightwebinc/shard-manifest/metrics"
)

// Sender drives the periodic emission of ShardManifest datagrams.
type Sender struct {
	cfg        *config.Config
	rec        *metrics.Recorder
	log        *slog.Logger
	iface      *net.Interface
	srcIPv6    net.IP
	instanceID uint32
	dests      []*net.UDPAddr

	// publishers resolves the SSM data-plane publisher source set
	// (Flags.SourcesValid payload). nil when cfg.SourceMode is "asm"
	// or cfg.Publishers is empty.
	publishers *bootstrap.Resolver

	mu      sync.RWMutex
	sources [][16]byte // cached, sorted, deduped; refreshed by publishers.OnChange
}

// New builds a Sender. ResolveIface and PrimaryIPv6 are called eagerly.
func New(cfg *config.Config, rec *metrics.Recorder, instanceID uint32) (*Sender, error) {
	ifc, err := cfg.ResolveIface()
	if err != nil {
		return nil, fmt.Errorf("resolve iface: %w", err)
	}
	src, err := cfg.PrimaryIPv6(ifc)
	if err != nil {
		return nil, fmt.Errorf("primary ipv6: %w", err)
	}
	scopes, err := cfg.ScopePrefixes()
	if err != nil {
		return nil, err
	}
	dests := make([]*net.UDPAddr, 0, len(scopes))
	for _, scope := range scopes {
		ip := shard.GroupAddr(scope, cfg.MCGroupID, shard.GroupBeacon)
		dests = append(dests, &net.UDPAddr{IP: ip, Port: cfg.Port})
	}
	s := &Sender{
		cfg:        cfg,
		rec:        rec,
		log:        slog.Default().With("component", "sender"),
		iface:      ifc,
		srcIPv6:    src,
		instanceID: instanceID,
		dests:      dests,
	}
	if len(cfg.Publishers) > 0 {
		s.publishers = &bootstrap.Resolver{
			Entries:  cfg.Publishers,
			Refresh:  cfg.PublishersRefresh,
			OnChange: s.onPublishersChange,
		}
	}
	return s, nil
}

// onPublishersChange caches the current resolved set as [][16]byte so
// buildManifest doesn't re-resolve on every emission.
func (s *Sender) onPublishersChange(_, _ []netip.Addr) {
	if s.publishers == nil {
		return
	}
	cur := s.publishers.Current()
	cached := make([][16]byte, len(cur))
	for i, a := range cur {
		cached[i] = a.As16()
	}
	s.mu.Lock()
	s.sources = cached
	s.mu.Unlock()
	if s.rec != nil {
		s.rec.SetPublisherCount(len(cached))
	}
}

// currentSources returns the cached source set; safe for concurrent reads.
func (s *Sender) currentSources() [][16]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.sources) == 0 {
		return nil
	}
	out := make([][16]byte, len(s.sources))
	copy(out, s.sources)
	return out
}

// Iface returns the resolved egress interface (informational).
func (s *Sender) Iface() *net.Interface { return s.iface }

// SrcIPv6 returns the resolved primary IPv6 address (informational).
func (s *Sender) SrcIPv6() net.IP { return s.srcIPv6 }

// Run executes the sender loop. It sends one manifest immediately, then
// re-sends at AnnounceInterval (with ±10% jitter) until ctx is cancelled.
// On context cancellation a final manifest with the Shutdown flag is emitted.
func (s *Sender) Run(ctx context.Context) error {
	if s.publishers != nil {
		if err := s.publishers.Start(ctx); err != nil {
			return fmt.Errorf("publishers resolver: %w", err)
		}
	}
	conns, err := s.openSockets()
	if err != nil {
		return err
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	s.log.Info("sender started",
		"interval", s.cfg.AnnounceInterval,
		"ttl", s.cfg.TTL,
		"shard_bits", s.cfg.ShardBits,
		"groups", len(s.dests),
		"iface", s.iface.Name,
		"src", s.srcIPv6.String(),
	)

	// Send immediately.
	s.sendOnce(conns, false)

	// Use a timer (instead of a fixed ticker) so we can introduce jitter.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		next := jitter(s.cfg.AnnounceInterval, 0.10, rng)
		t := time.NewTimer(next)
		select {
		case <-ctx.Done():
			t.Stop()
			s.sendOnce(conns, true) // shutdown manifest
			return nil
		case <-t.C:
			s.sendOnce(conns, false)
		}
	}
}

// sendOnce builds one manifest and writes it to every destination conn.
// shutdown sets Flags.Shutdown.
func (s *Sender) sendOnce(conns []*net.UDPConn, shutdown bool) {
	m, err := s.buildManifest(shutdown)
	if err != nil {
		s.log.Error("build manifest", "err", err)
		s.rec.SendError("build")
		return
	}
	buf := make([]byte, frame.ShardManifestSize(m))
	n, err := frame.EncodeShardManifest(m, buf)
	if err != nil {
		s.log.Error("encode manifest", "err", err)
		s.rec.SendError("encode")
		return
	}

	if shutdown {
		s.log.Info("emitting shutdown manifest")
	}

	for i, conn := range conns {
		if _, werr := conn.WriteTo(buf[:n], s.dests[i]); werr != nil {
			s.log.Warn("send error", "dest", s.dests[i].String(), "err", werr)
			s.rec.SendError("write")
			continue
		}
		s.rec.SendOK(n)
	}

	// Update gauges.
	s.rec.SetShardBits(s.cfg.ShardBits)
	s.rec.SetJoinedGroups(joinCount(s.cfg))
}

// buildManifest constructs an in-memory ShardManifest from the daemon config.
func (s *Sender) buildManifest(shutdown bool) (*frame.ShardManifest, error) {
	m := &frame.ShardManifest{
		InstanceID:       s.instanceID,
		Epoch:            uint32(time.Now().Unix()), //nolint:gosec // wraps in 2106
		TTL:              uint16(s.cfg.TTL.Seconds()),
		AnnounceInterval: uint16(s.cfg.AnnounceInterval.Seconds()),
		ShardBits:        s.cfg.ShardBits,
		RoleHint:         s.cfg.RoleHint,
		GenerationID:     s.cfg.GenerationID,
	}
	src := s.srcIPv6.To16()
	if src != nil {
		copy(m.SrcIPv6[:], src)
	}

	if s.cfg.Authoritative {
		m.Flags |= frame.ShardManifestFlagAuthoritative
	}
	if shutdown {
		m.Flags |= frame.ShardManifestFlagShutdown
	}
	if s.cfg.SourceMode == "ssm" {
		m.Flags |= frame.ShardManifestFlagSourceModeSSM
	}
	if s.cfg.PilotOnly {
		m.Flags |= frame.ShardManifestFlagPilotOnly
	}
	if srcs := s.currentSources(); len(srcs) > 0 {
		m.Flags |= frame.ShardManifestFlagSourcesValid
		m.Sources = srcs
	}
	if s.cfg.Successor != nil {
		m.Flags |= frame.ShardManifestFlagSuccessorValid
		var sf byte
		if s.cfg.Successor.SourceModeSSM {
			sf |= frame.SuccessorFlagSourceModeSSM
		}
		m.Successor = &frame.SuccessorBlock{
			GenerationID:    s.cfg.Successor.GenerationID,
			ShardBits:       s.cfg.Successor.ShardBits,
			Flags:           sf,
			TransitionEpoch: s.cfg.Successor.TransitionEpoch,
		}
	}

	groups, hasClaim := resolveGroups(s.cfg)
	if hasClaim {
		m.Flags |= frame.ShardManifestFlagGroupsValid
		count := len(groups)
		switch s.cfg.EncodingFormForGroups(count) {
		case config.EncodingBitmap:
			bmBytes := bitmapBytes(s.cfg.ShardBits)
			bm := make([]byte, bmBytes)
			for _, g := range groups {
				idx := int(g)
				if idx/8 < len(bm) {
					bm[idx/8] |= 1 << (idx % 8)
				}
			}
			m.Bitmap = bm
		default: // list (or auto-list)
			m.Groups = groups
		}
	}
	return m, nil
}

// openSockets dials one UDP socket per destination, forcing the multicast
// egress interface via IPV6_MULTICAST_IF.
func (s *Sender) openSockets() ([]*net.UDPConn, error) {
	conns := make([]*net.UDPConn, 0, len(s.dests))
	for _, dest := range s.dests {
		conn, err := net.DialUDP("udp6", nil, dest)
		if err != nil {
			s.log.Warn("dial", "dest", dest.String(), "err", err)
			continue
		}
		if rc, err := conn.SyscallConn(); err == nil {
			_ = rc.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6,
					syscall.IPV6_MULTICAST_IF, s.iface.Index)
			})
		}
		conns = append(conns, conn)
	}
	if len(conns) == 0 {
		return nil, errors.New("no destinations could be dialed")
	}
	return conns, nil
}

// resolveGroups returns the joined group set and whether a claim is being
// made (i.e. ShardManifestFlagGroupsValid should be set).
func resolveGroups(c *config.Config) ([]uint16, bool) {
	if c.JoinAll {
		n := 1 << c.ShardBits
		out := make([]uint16, n)
		for i := 0; i < n; i++ {
			out[i] = uint16(i)
		}
		return out, true
	}
	if len(c.JoinedGroups) > 0 {
		out := make([]uint16, len(c.JoinedGroups))
		copy(out, c.JoinedGroups)
		return out, true
	}
	return nil, false
}

func joinCount(c *config.Config) int {
	if c.JoinAll {
		return 1 << c.ShardBits
	}
	return len(c.JoinedGroups)
}

func bitmapBytes(shardBits uint8) int {
	n := 1 << shardBits
	return (n + 7) / 8
}

func jitter(d time.Duration, frac float64, rng *rand.Rand) time.Duration {
	if frac <= 0 {
		return d
	}
	delta := float64(d) * frac
	return d + time.Duration((rng.Float64()*2-1)*delta)
}
