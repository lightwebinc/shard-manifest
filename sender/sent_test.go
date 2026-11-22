package sender

import (
	"net"
	"testing"
	"time"
)

// Sent counts the datagrams written: manifest-emit uses it to fail a smoke
// test that put nothing on the wire.
func TestSent_CountsWrittenManifests(t *testing.T) {
	rx := receiver(t)
	s, _ := wireSender(t, rx.LocalAddr().(*net.UDPAddr))
	stop := runSender(t, s)
	receive(t, rx, "first manifest")
	stop()
	receive(t, rx, "shutdown manifest")
	if got := s.Sent(); got != 2 {
		t.Errorf("Sent() = %d, want 2 (start + shutdown)", got)
	}

	// Nothing listening: the first write succeeds, the ICMPv6
	// port-unreachable it draws fails the shutdown write, and Sent counts
	// only the first.
	closed, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	refused := closed.LocalAddr().(*net.UDPAddr)
	_ = closed.Close()
	s, _ = wireSender(t, refused)
	stop = runSender(t, s)
	deadline := time.Now().Add(5 * time.Second)
	for s.Sent() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // let the port-unreachable land
	stop()
	if got := s.Sent(); got != 1 {
		t.Errorf("Sent() = %d to a refused port, want 1 (the shutdown write must fail)", got)
	}
}

// Destinations lists every configured destination with its own count, so a
// destination that was never dialed shows 0 even when another one sent.
func TestDestinations_PerDestinationCounts(t *testing.T) {
	rx := receiver(t)
	live := rx.LocalAddr().(*net.UDPAddr)
	undialable := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	s, _ := wireSender(t, undialable, live)
	stop := runSender(t, s)
	receive(t, rx, "first manifest")
	stop()
	got := s.Destinations()
	if len(got) != 2 || got[0].Dest != undialable.String() || got[0].Sent != 0 ||
		got[1].Dest != live.String() || got[1].Sent == 0 {
		t.Errorf("Destinations() = %+v, want [%s 0] then [%s >0]", got, undialable, live)
	}
}
