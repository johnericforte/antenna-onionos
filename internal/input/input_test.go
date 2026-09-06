package input

import (
	"testing"
	"time"
)

// Drain never touches the device file, so a Reader with just a channel is
// enough and no /dev/input node is needed.
func newTestReader() *Reader {
	return &Reader{events: make(chan Event, 32)}
}

// The whole reason Drain exists: presses made during a video must not replay
// into the list when it ends.
func TestDrainDiscardsQueuedEvents(t *testing.T) {
	r := newTestReader()
	for i := 0; i < 5; i++ {
		r.events <- Event{Button: A, Pressed: true}
	}

	r.Drain(20 * time.Millisecond)

	if n := len(r.events); n != 0 {
		t.Fatalf("%d events survived the drain", n)
	}
}

// An empty channel must not block. Without the timer arm the app would freeze
// every time a video ended with nothing queued.
func TestDrainReturnsOnAnIdleChannel(t *testing.T) {
	r := newTestReader()

	done := make(chan struct{})
	go func() {
		r.Drain(20 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Drain blocked on an idle channel")
	}
}

// A closed channel is always ready to receive. Without the ok check this
// spins a core at 100% and never returns, which on a handheld means a battery
// pull.
func TestDrainReturnsOnAClosedChannel(t *testing.T) {
	r := newTestReader()
	close(r.events)

	done := make(chan struct{})
	go func() {
		r.Drain(20 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Drain did not return on a closed channel")
	}
}

// The press that quit the video is still in flight when playback returns, so
// draining until the channel is empty is not enough. MENU quits ffplay and
// also exits Antenna, so a leaked press ends the app instead of showing the
// list.
func TestDrainWaitsForTheStreamToGoQuiet(t *testing.T) {
	r := newTestReader()

	go func() {
		time.Sleep(30 * time.Millisecond)
		r.events <- Event{Button: Menu, Pressed: true}
	}()

	r.Drain(120 * time.Millisecond)

	if n := len(r.events); n != 0 {
		t.Fatalf("%d late events survived, one of them would exit the app", n)
	}
}

func TestErrIsNilUntilAReadFails(t *testing.T) {
	r := newTestReader()
	if err := r.Err(); err != nil {
		t.Fatalf("Err() = %v on a healthy reader", err)
	}
}

// keySupported reads the kernel's key bitmap, one bit per key code. Getting
// the bit order wrong picks the wrong device and leaves the app with no
// working controls, which on a handheld looks exactly like a crash.
func TestKeySupported(t *testing.T) {
	bits := make([]byte, keyBitmapBytes)
	set := func(key Button) { bits[int(key)/8] |= 1 << (uint(key) % 8) }

	set(Up)   // 103
	set(Down) // 108

	if !keySupported(bits, Up) {
		t.Error("Up should be reported")
	}
	if !keySupported(bits, Down) {
		t.Error("Down should be reported")
	}
	if keySupported(bits, Left) {
		t.Error("Left is not set but was reported")
	}
	if keySupported(bits, A) {
		t.Error("A is not set but was reported")
	}
}

// A device that reports nothing, or a short read, must not be chosen.
func TestKeySupportedOnAnEmptyOrShortBitmap(t *testing.T) {
	if keySupported(make([]byte, keyBitmapBytes), Up) {
		t.Error("an empty bitmap reported a key")
	}
	if keySupported([]byte{}, Up) {
		t.Error("an empty slice should not index out of range or report a key")
	}
	if keySupported(make([]byte, 4), Up) {
		t.Error("a short bitmap should not report a key past its end")
	}
}

// The ioctl request number decides whether the kernel answers at all. It is
// _IOC(_IOC_READ, 'E', 0x20+EV_KEY, 96) from linux/input.h.
func TestEviocgbitKeyEncoding(t *testing.T) {
	const want = uint32(0x80604521)
	if got := uint32(eviocgbitKey); got != want {
		t.Errorf("eviocgbitKey = %#x, want %#x", got, want)
	}
}
