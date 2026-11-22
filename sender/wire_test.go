package sender

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lightwebinc/shard-common/frame"

	"github.com/lightwebinc/shard-manifest/config"
	"github.com/lightwebinc/shard-manifest/metrics"
)

// lockedBuffer is a log sink the sender goroutine writes while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// loopbackName returns the loopback interface the sender egresses on.
func loopbackName(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("interfaces: %v", err)
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 && ifc.Flags&net.FlagUp != 0 {
			return ifc.Name
		}
	}
	t.Skip("no loopback interface")
	return ""
}

// receiver is a UDP socket on the IPv6 loopback standing in for a manifest
// consumer.
func receiver(t *testing.T) *net.UDPConn {
	t.Helper()
	rx, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	t.Cleanup(func() { _ = rx.Close() })
	return rx
}

// receive reads one datagram from rx and decodes it as a ShardManifest.
func receive(t *testing.T, rx *net.UDPConn, what string) *frame.ShardManifest {
	t.Helper()
	_ = rx.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64*1024)
	n, _, err := rx.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("%s: no manifest arrived: %v", what, err)
	}
	m, err := frame.DecodeShardManifest(buf[:n])
	if err != nil {
		t.Fatalf("%s: %d bytes arrived but do not decode as a manifest: %v", what, n, err)
	}
	return m
}

// wireSender builds the real Sender (New: interface, source address, the
// beacon-group destinations) egressing on loopback, then points it at dests
// in place of the beacon groups and captures its log. The sockets Run opens
// and the writes sendOnce makes are the production ones.
func wireSender(t *testing.T, dests ...*net.UDPAddr) (*Sender, *lockedBuffer) {
	t.Helper()
	cfg := &config.Config{
		ShardBits:          4,
		JoinedGroups:       []uint16{1, 2},
		Encoding:           config.EncodingAuto,
		RoleHint:           frame.RoleHintProxy,
		Iface:              loopbackName(t),
		Port:               9001,
		ManifestScope:      "site",
		MCGroupID:          0x000B,
		AnnounceInterval:   time.Hour,
		TTL:                900 * time.Second,
		SourceMode:         "asm",
		ControlGroupCompat: config.ControlGroupASMOnly,
	}
	rec, err := metrics.New("wire-test", "", 0)
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}
	s, err := New(cfg, rec, 0xC0FFEE)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.dests = dests
	logs := &lockedBuffer{}
	s.log = slog.New(slog.NewTextHandler(logs, nil))
	return s, logs
}

// runSender starts Run and returns a stop function that cancels it and
// waits for it to return.
func runSender(t *testing.T, s *Sender) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
	}
}

// The sender must put a decodable manifest on the wire: one at start, and a
// Shutdown manifest when Run is canceled.
func TestRun_DeliversManifestOverTheWire(t *testing.T) {
	rx := receiver(t)
	s, logs := wireSender(t, rx.LocalAddr().(*net.UDPAddr))
	stop := runSender(t, s)

	m := receive(t, rx, "first manifest")
	if m.InstanceID != 0xC0FFEE || m.ShardBits != 4 || m.TTL != 900 || m.AnnounceInterval != 3600 {
		t.Errorf("manifest = instance %#x shard-bits %d ttl %d interval %d, want 0xc0ffee, 4, 900, 3600",
			m.InstanceID, m.ShardBits, m.TTL, m.AnnounceInterval)
	}
	if m.Flags&frame.ShardManifestFlagGroupsValid == 0 || len(m.Groups) != 2 || m.Groups[0] != 1 || m.Groups[1] != 2 {
		t.Errorf("joined groups = %v (flags %#x), want [1 2]", m.Groups, m.Flags)
	}
	if m.Flags&frame.ShardManifestFlagShutdown != 0 {
		t.Error("first manifest carries the Shutdown flag")
	}

	stop()
	if last := receive(t, rx, "shutdown manifest"); last.Flags&frame.ShardManifestFlagShutdown == 0 {
		t.Errorf("manifest sent on cancel lacks the Shutdown flag (flags %#x)", last.Flags)
	}
	if strings.Contains(logs.String(), "send error") {
		t.Errorf("sender logged send errors:\n%s", logs)
	}
}

// A destination that fails to dial is skipped. The sockets after it must
// keep their own destinations: manifests reach the live receiver, and a
// write error is reported against the address that failed, not against the
// skipped one.
func TestRun_FailedDialDoesNotShiftDestinations(t *testing.T) {
	rx := receiver(t)

	// A udp6 socket cannot connect to an IPv4 address: this dial fails.
	undialable := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	// A loopback port nobody listens on: the first write succeeds, the
	// ICMPv6 port-unreachable makes the next one fail.
	closed, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	refused := closed.LocalAddr().(*net.UDPAddr)
	_ = closed.Close()

	s, logs := wireSender(t, undialable, refused, rx.LocalAddr().(*net.UDPAddr))
	stop := runSender(t, s)
	receive(t, rx, "first manifest")
	time.Sleep(100 * time.Millisecond) // let the port-unreachable reach the refused socket
	stop()
	receive(t, rx, "shutdown manifest")

	var sendErrs []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "send error") {
			sendErrs = append(sendErrs, line)
		}
	}
	if len(sendErrs) == 0 {
		t.Fatalf("no send error logged for the refused port %s:\n%s", refused, logs)
	}
	for _, line := range sendErrs {
		if !strings.Contains(line, "dest="+refused.String()) {
			t.Errorf("send error not attributed to the refused port %s: %s", refused, line)
		}
	}
	if !strings.Contains(logs.String(), "msg=dial") || !strings.Contains(logs.String(), undialable.String()) {
		t.Errorf("the failed dial of %s was not logged:\n%s", undialable, logs)
	}
}
