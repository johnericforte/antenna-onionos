// Package relay serves a remote HTTPS stream to a local player over plain
// HTTP on the loopback interface.
//
// It exists because the ffplay that OnionOS ships has no TLS support. Handed
// an https URL it prints "Protocol not found" and exits with status zero, so
// the failure is both fatal and silent. archive.org forces TLS on every path,
// which leaves exactly one option: terminate TLS here, where Go's own stack
// does it, and give the player an address it can actually open.
//
// Nothing leaves the device. The listener binds to 127.0.0.1 on a port the
// kernel picks, and it lives only as long as the video.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"antenna/internal/dbg"
)

// dialTimeout bounds reaching the origin. Playback has already started as far
// as the user is concerned, so this is short.
const dialTimeout = 20 * time.Second

// forwardedRequestHeaders are the headers a player sends that the origin needs
// to see. Range is the one that matters: without it, seeking downloads the
// file from the beginning every time.
var forwardedRequestHeaders = []string{"Range", "If-Range", "Accept", "User-Agent"}

// forwardedResponseHeaders are what the player needs back to seek and to know
// how much is coming.
var forwardedResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Content-Range",
	"Accept-Ranges",
	"Last-Modified",
	"ETag",
}

// Relay is a running loopback server in front of one remote URL.
type Relay struct {
	target   string
	listener net.Listener
	server   *http.Server
	client   *http.Client

	mu  sync.Mutex
	err error
}

// Start begins serving target on 127.0.0.1. The caller must Close it.
func Start(target string) (*Relay, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("relay target %q: %w", target, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("relay cannot serve %q", parsed.Scheme)
	}

	// Port zero: the kernel picks a free one, so two videos in a row cannot
	// collide on a fixed number.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("relay listen: %w", err)
	}

	r := &Relay{
		target:   target,
		listener: listener,
		client:   &http.Client{Timeout: 0},
	}
	r.server = &http.Server{
		Handler:           http.HandlerFunc(r.serve),
		ReadHeaderTimeout: dialTimeout,
	}

	go func() {
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.setErr(err)
		}
	}()

	dbg.Printf("relay: serving %s at %s", target, r.URL())
	return r, nil
}

// URL is the address to hand the player. The path is fixed and ignored: this
// relay fronts exactly one file.
func (r *Relay) URL() string {
	return "http://" + r.listener.Addr().String() + "/stream"
}

// Err reports a failure the server hit while running, which is the only way
// one can surface from a background goroutine.
func (r *Relay) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *Relay) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

// Close stops the server and releases the port.
func (r *Relay) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return r.server.Shutdown(ctx)
}

func (r *Relay) serve(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		http.Error(w, "only GET and HEAD", http.StatusMethodNotAllowed)
		return
	}

	outbound, err := http.NewRequestWithContext(req.Context(), req.Method, r.target, nil)
	if err != nil {
		r.fail(w, "build request", err)
		return
	}
	for _, name := range forwardedRequestHeaders {
		if v := req.Header.Get(name); v != "" {
			outbound.Header.Set(name, v)
		}
	}

	resp, err := r.client.Do(outbound)
	if err != nil {
		r.fail(w, "fetch", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for _, name := range forwardedResponseHeaders {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if req.Method == http.MethodHead {
		return
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		// The player closing the connection mid-video is how quitting looks
		// from here, so it is not worth recording as a failure.
		if isClientGone(err) {
			dbg.Printf("relay: player closed after %d bytes", written)
			return
		}
		r.setErr(fmt.Errorf("relay copy: %w", err))
		dbg.Fail("relay: copy failed after %d bytes: %v", written, err)
	}
}

func (r *Relay) fail(w http.ResponseWriter, what string, err error) {
	r.setErr(fmt.Errorf("relay %s: %w", what, err))
	dbg.Fail("relay: %s: %v", what, err)
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// isClientGone reports whether err is the player hanging up rather than a
// problem worth showing.
func isClientGone(err error) bool {
	switch {
	case errors.Is(err, context.Canceled),
		errors.Is(err, net.ErrClosed),
		errors.Is(err, io.ErrClosedPipe):
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer")
}
