package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testModTime = time.Unix(0, 0)

const body = "0123456789abcdefghijklmnopqrstuvwxyz"

// origin stands in for archive.org: it serves one file and honours Range,
// which is what seeking in a video depends on.
func origin(t *testing.T, seen *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "v.mp4", testModTime, strings.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func startRelay(t *testing.T, target string) *Relay {
	t.Helper()
	r, err := Start(target)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestRelayServesTheStreamOverPlainHTTP(t *testing.T) {
	src := origin(t, nil)
	r := startRelay(t, src.URL+"/v.mp4")

	// The whole point: the player is handed http, never https.
	if !strings.HasPrefix(r.URL(), "http://127.0.0.1:") {
		t.Fatalf("relay URL is %q, want loopback http", r.URL())
	}

	resp, err := http.Get(r.URL())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Errorf("got %q, want %q", got, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type is %q", ct)
	}
	if r.Err() != nil {
		t.Errorf("relay recorded %v", r.Err())
	}
}

// Without Range forwarding, seeking re-downloads the file from the start every
// time, which on a handheld's Wi-Fi is the difference between usable and not.
func TestRelayForwardsRangeAndReturnsPartialContent(t *testing.T) {
	var seen http.Header
	src := origin(t, &seen)
	r := startRelay(t, src.URL+"/v.mp4")

	req, err := http.NewRequest(http.MethodGet, r.URL(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Range", "bytes=10-19")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status is %d, want 206", resp.StatusCode)
	}
	if got := seen.Get("Range"); got != "bytes=10-19" {
		t.Errorf("origin saw Range %q", got)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body[10:20] {
		t.Errorf("got %q, want %q", got, body[10:20])
	}
	if cr := resp.Header.Get("Content-Range"); cr == "" {
		t.Error("Content-Range was not passed back, so the player cannot seek")
	}
}

func TestRelayReportsAnOriginFailure(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer src.Close()

	r := startRelay(t, src.URL+"/v.mp4")

	resp, err := http.Get(r.URL())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	// The status has to reach the player, or it plays nothing and says nothing.
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status is %d, want the origin's 404", resp.StatusCode)
	}
}

func TestRelayRefusesATargetItCannotFetch(t *testing.T) {
	for _, target := range []string{"", "://nonsense", "ftp://example.invalid/v.mp4"} {
		if _, err := Start(target); err == nil {
			t.Errorf("Start(%q) should fail", target)
		}
	}
}

func TestRelayCloseReleasesThePort(t *testing.T) {
	src := origin(t, nil)

	r, err := Start(src.URL + "/v.mp4")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := r.URL()
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp, err := http.Get(addr)
	if err == nil {
		_ = resp.Body.Close()
		t.Error("the relay still answers after Close")
	}
}

// Two videos in a row must not collide on a port.
func TestRelaysDoNotShareAPort(t *testing.T) {
	src := origin(t, nil)

	first := startRelay(t, src.URL+"/v.mp4")
	second := startRelay(t, src.URL+"/v.mp4")

	if first.URL() == second.URL() {
		t.Fatalf("both relays claimed %s", first.URL())
	}
}

func TestIsClientGone(t *testing.T) {
	if !isClientGone(fmt.Errorf("write tcp: broken pipe")) {
		t.Error("a broken pipe is the player quitting, not a failure")
	}
	if isClientGone(fmt.Errorf("unexpected EOF from origin")) {
		t.Error("an origin failure was mistaken for the player quitting")
	}
}
