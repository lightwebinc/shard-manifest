// Package config loads and validates runtime configuration for the
// bitcoin-shard-manifest daemon. Parameters are accepted from CLI flags
// first; environment variables serve as fallbacks; hard-coded defaults
// apply when neither is present.
package config

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lightwebinc/bitcoin-shard-common/frame"
)

// Scopes maps a human-readable scope name to the two-byte big-endian IPv6
// multicast prefix.
var Scopes = map[string]uint16{
	"link":   0xFF02,
	"site":   0xFF05,
	"org":    0xFF08,
	"global": 0xFF0E,
}

// roleHints maps role-hint names to their on-wire byte value.
var roleHints = map[string]uint8{
	"generic":        frame.RoleHintGeneric,
	"proxy":          frame.RoleHintProxy,
	"listener":       frame.RoleHintListener,
	"retry-endpoint": frame.RoleHintRetryEndpoint,
	"producer":       frame.RoleHintProducer,
	"manifest-only":  frame.RoleHintManifestOnly,
}

// EncodingForm selects between list and bitmap payloads.
type EncodingForm uint8

const (
	// EncodingAuto selects list ≤ thresholdListEntries entries, bitmap otherwise.
	EncodingAuto EncodingForm = iota
	// EncodingList forces list form.
	EncodingList
	// EncodingBitmap forces bitmap form.
	EncodingBitmap
)

// thresholdListEntries is the joined-group count above which the auto encoder
// switches from list to bitmap form. Each list entry is 2 bytes; a bitmap
// covering 2^ShardBits indices is fixed at ceil(2^ShardBits/8) bytes. List
// form is more compact for sparse claims.
const thresholdListEntries = 32

// Config holds all runtime parameters for the manifest daemon.
type Config struct {
	// Identity and content
	ShardBits     uint8
	JoinedGroups  []uint16
	JoinAll       bool
	Encoding      EncodingForm
	RoleHint      uint8
	GenerationID  [16]byte
	Authoritative bool
	InstanceID    string // service.instance.id (defaults to hostname)

	// Network
	Iface         string
	Port          int
	ManifestScope string // comma list: site,org,global
	MCGroupID     uint16

	// Cadence
	AnnounceInterval time.Duration
	TTL              time.Duration

	// Observability
	MetricsAddr  string
	OTLPEndpoint string
	OTLPInterval time.Duration

	// Misc
	Debug bool
}

// ScopePrefixes returns the active scope prefix bytes (e.g. 0xFF05) parsed
// from ManifestScope. Order is preserved.
func (c *Config) ScopePrefixes() ([]uint16, error) {
	parts := strings.Split(c.ManifestScope, ",")
	out := make([]uint16, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, ok := Scopes[p]
		if !ok {
			return nil, fmt.Errorf("invalid manifest-scope %q (allowed: link,site,org,global)", p)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("manifest-scope is empty")
	}
	return out, nil
}

// Load parses CLI flags and environment variables and returns a validated
// Config.
func Load() (*Config, error) {
	c := &Config{}

	fs := flag.NewFlagSet("bitcoin-shard-manifest", flag.ContinueOnError)

	var (
		shardBits        = fs.Uint("shard-bits", envUint("SHARD_BITS", 0), "shard bits (0..12)")
		joinedGroups     = fs.String("joined-groups", os.Getenv("JOINED_GROUPS"), "comma list of hex group indices, or 'all'")
		encodingFlag     = fs.String("bitmap", envOrDefault("BITMAP", "auto"), "joined-groups encoding: auto|list|bitmap")
		roleHint         = fs.String("role-hint", envOrDefault("ROLE_HINT", "generic"), "generic|proxy|listener|retry-endpoint|producer|manifest-only")
		genID            = fs.String("generation-id", os.Getenv("GENERATION_ID"), "16-byte hex (with or without dashes); empty = zero UUID")
		authoritative    = fs.Bool("authoritative", envBool("AUTHORITATIVE", false), "set Flags.Authoritative")
		instanceID       = fs.String("instance-id", os.Getenv("INSTANCE_ID"), "service.instance.id (defaults to hostname)")
		iface            = fs.String("iface", os.Getenv("IFACE"), "outgoing multicast interface (defaults to first non-loopback)")
		port             = fs.Int("port", envInt("PORT", 9001), "destination UDP port")
		manifestScope    = fs.String("manifest-scope", envOrDefault("MANIFEST_SCOPE", "site"), "comma list of scopes: link,site,org,global")
		mcGroupID        = fs.String("mc-group-id", envOrDefault("MC_GROUP_ID", "0x000B"), "IANA multicast group-id (16 bits)")
		announceInterval = fs.Duration("announce-interval", envDuration("ANNOUNCE_INTERVAL", 300*time.Second), "send period")
		ttl              = fs.Duration("ttl", envDuration("TTL", 0), "TTL on the wire (0 = consumer default)")
		metricsAddr      = fs.String("metrics-addr", envOrDefault("METRICS_ADDR", "[::]:9091"), "metrics/health HTTP listener address")
		otlpEndpoint     = fs.String("otlp-endpoint", os.Getenv("OTLP_ENDPOINT"), "OTLP gRPC endpoint (empty = disabled)")
		otlpInterval     = fs.Duration("otlp-interval", envDuration("OTLP_INTERVAL", 15*time.Second), "OTLP push interval")
		debug            = fs.Bool("debug", envBool("DEBUG", false), "verbose logging")
	)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}

	if *shardBits > frame.MaxShardBits {
		return nil, fmt.Errorf("shard-bits %d exceeds maximum %d", *shardBits, frame.MaxShardBits)
	}
	c.ShardBits = uint8(*shardBits)

	groups, joinAll, err := parseJoinedGroups(*joinedGroups, c.ShardBits)
	if err != nil {
		return nil, err
	}
	c.JoinedGroups = groups
	c.JoinAll = joinAll

	switch strings.ToLower(*encodingFlag) {
	case "auto", "":
		c.Encoding = EncodingAuto
	case "list":
		c.Encoding = EncodingList
	case "bitmap":
		c.Encoding = EncodingBitmap
	default:
		return nil, fmt.Errorf("invalid bitmap %q (auto|list|bitmap)", *encodingFlag)
	}

	r, ok := roleHints[strings.ToLower(*roleHint)]
	if !ok {
		return nil, fmt.Errorf("invalid role-hint %q", *roleHint)
	}
	c.RoleHint = r

	gen, err := parseGenerationID(*genID)
	if err != nil {
		return nil, err
	}
	c.GenerationID = gen

	c.Authoritative = *authoritative

	if *instanceID == "" {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		c.InstanceID = host
	} else {
		c.InstanceID = *instanceID
	}

	c.Iface = *iface
	if *port < 1 || *port > 65535 {
		return nil, fmt.Errorf("port %d out of range", *port)
	}
	c.Port = *port
	c.ManifestScope = *manifestScope
	if _, err := c.ScopePrefixes(); err != nil {
		return nil, err
	}

	groupID, err := parseUint16(*mcGroupID)
	if err != nil {
		return nil, fmt.Errorf("mc-group-id: %w", err)
	}
	c.MCGroupID = groupID

	if *announceInterval <= 0 {
		return nil, fmt.Errorf("announce-interval must be > 0")
	}
	c.AnnounceInterval = *announceInterval
	if *ttl < 0 {
		return nil, fmt.Errorf("ttl must be ≥ 0")
	}
	c.TTL = *ttl

	c.MetricsAddr = *metricsAddr
	c.OTLPEndpoint = *otlpEndpoint
	c.OTLPInterval = *otlpInterval
	c.Debug = *debug

	return c, nil
}

// ResolveIface returns the outgoing multicast *net.Interface based on
// c.Iface. When unset, the first non-loopback interface with a global IPv6
// address is selected.
func (c *Config) ResolveIface() (*net.Interface, error) {
	if c.Iface != "" {
		return net.InterfaceByName(c.Iface)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ipn.IP.To4() != nil {
				continue
			}
			if ipn.IP.IsGlobalUnicast() {
				ifcCopy := ifc
				return &ifcCopy, nil
			}
		}
	}
	return nil, fmt.Errorf("no non-loopback interface with a global IPv6 address found; set -iface")
}

// PrimaryIPv6 returns the first global-unicast IPv6 address on the resolved
// interface (used for the SrcIPv6 field of the manifest).
func (c *Config) PrimaryIPv6(ifc *net.Interface) (net.IP, error) {
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ipn.IP.To4() != nil {
			continue
		}
		if ipn.IP.IsGlobalUnicast() {
			return ipn.IP.To16(), nil
		}
	}
	return net.IPv6unspecified, nil
}

// EncodingFormForGroups returns the form to use given the configured policy
// and current join count. groupCount is len(JoinedGroups) when JoinAll is
// false, or 1<<ShardBits when JoinAll is true.
func (c *Config) EncodingFormForGroups(groupCount int) EncodingForm {
	switch c.Encoding {
	case EncodingList:
		return EncodingList
	case EncodingBitmap:
		return EncodingBitmap
	default:
		if groupCount <= thresholdListEntries {
			return EncodingList
		}
		return EncodingBitmap
	}
}

// --- helpers ---

func parseJoinedGroups(s string, shardBits uint8) ([]uint16, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false, nil
	}
	if strings.EqualFold(s, "all") {
		return nil, true, nil
	}
	parts := strings.Split(s, ",")
	limit := uint32(1) << shardBits
	if shardBits == 0 {
		limit = 1
	}
	seen := make(map[uint16]struct{}, len(parts))
	out := make([]uint16, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := parseUint16(p)
		if err != nil {
			return nil, false, fmt.Errorf("joined-groups %q: %w", p, err)
		}
		if uint32(v) >= limit {
			return nil, false, fmt.Errorf("joined-groups: index %d ≥ 2^shard_bits (%d)", v, limit)
		}
		if _, dup := seen[v]; dup {
			return nil, false, fmt.Errorf("joined-groups: duplicate index %d", v)
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	// sort ascending (small slice; insertion sort)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, false, nil
}

func parseGenerationID(s string) ([16]byte, error) {
	var out [16]byte
	s = strings.ReplaceAll(s, "-", "")
	if s == "" {
		return out, nil
	}
	if len(s) != 32 {
		return out, fmt.Errorf("generation-id must be 16 bytes (32 hex chars), got %d chars", len(s))
	}
	dec, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("generation-id hex: %w", err)
	}
	copy(out[:], dec)
	return out, nil
}

func parseUint16(s string) (uint16, error) {
	s = strings.TrimSpace(s)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base = 16
		s = s[2:]
	}
	v, err := strconv.ParseUint(s, base, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envUint(key string, def uint) uint {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			return uint(n)
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
