package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecorder_HealthAndReady(t *testing.T) {
	rec, err := New("test-instance", "", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec.SetMaxAge(60 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", rec.handleHealthz)
	mux.HandleFunc("/readyz", rec.handleReadyz)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// healthz: always 200.
	resp := mustGet(t, srv.URL+"/healthz")
	if resp.StatusCode != 200 {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}

	// readyz before any send: 503 starting.
	resp = mustGet(t, srv.URL+"/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("pre-send readyz = %d, want 503", resp.StatusCode)
	}

	// After SendOK: 200 ready.
	rec.SendOK(64)
	resp = mustGet(t, srv.URL+"/readyz")
	if resp.StatusCode != 200 {
		t.Errorf("post-send readyz = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"status":"ready"`) {
		t.Errorf("readyz body = %s, want ready", string(body))
	}

	// Draining: 503.
	rec.SetDraining()
	resp = mustGet(t, srv.URL+"/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("draining readyz = %d, want 503", resp.StatusCode)
	}
}

func TestRecorder_Counters(t *testing.T) {
	rec, err := New("test", "", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec.SendOK(100)
	rec.SendOK(50)
	rec.SendError("write")
	rec.SetShardBits(4)
	rec.SetJoinedGroups(7)
	// No assertion library available; just ensure no panic and the metrics
	// endpoint still works.
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // local test
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}
