// Package input reads the Miyoo Mini's buttons from the Linux evdev interface.
package input

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Device is the evdev node the Miyoo is expected to expose its buttons on. It
// is a preference rather than a fact: nothing about this app has been observed
// on the hardware, and a wrong node here means the app opens a device that
// never reports a press.
const Device = "/dev/input/event0"

// deviceGlob is where the kernel puts every evdev node, searched when the
// preferred one turns out not to carry the d-pad.
const deviceGlob = "/dev/input/event*"

// eventSize is sizeof(struct input_event) on a 32-bit kernel: two 4-byte
// timeval fields, two uint16, and one int32.
const eventSize = 16

// evKey is EV_KEY, the event type carrying button presses.
const evKey = 1

// keyBitmapBytes covers KEY_MAX, one bit per key code.
const keyBitmapBytes = 96

// eviocgbitKey is EVIOCGBIT(EV_KEY, keyBitmapBytes), the ioctl that reports
// which key codes a device can send. Encoded as Linux encodes every ioctl:
// direction, size, type letter, then the request number.
const eviocgbitKey = 2<<30 | keyBitmapBytes<<16 | 'E'<<8 | (0x20 + evKey)

// Button identifies a physical control.
type Button int

// Linux key codes as wired on the Miyoo Mini. These are the values the kernel
// reports, not USB HID usages. Verify on device before relying on the face
// buttons; the d-pad codes are confirmed against a known-good client.
const (
	Up     Button = 103
	Down   Button = 108
	Left   Button = 105
	Right  Button = 106
	A      Button = 57
	B      Button = 29
	X      Button = 42
	Y      Button = 56
	Start  Button = 28
	Select Button = 97
	Menu   Button = 1
	L1     Button = 18
	R1     Button = 20
)

// Event is a button transition. Repeat is true for auto-repeat while held.
type Event struct {
	Button  Button
	Pressed bool
	Repeat  bool
}

// Reader streams button events from an evdev device.
type Reader struct {
	file   *os.File
	events chan Event

	mu  sync.Mutex
	err error
}

// Open starts reading button events in the background.
//
// It prefers path, but only if that node actually reports the d-pad. Otherwise
// it asks every evdev node which keys it carries and takes the first that
// answers correctly. Guessing a node number is how this app ends up running
// with no working controls, which on a handheld is indistinguishable from a
// crash.
func Open(path string) (*Reader, error) {
	f, err := openButtons(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{file: f, events: make(chan Event, 32)}
	go r.loop()
	return r, nil
}

func openButtons(preferred string) (*os.File, error) {
	if f, err := os.Open(preferred); err == nil {
		if hasDPad(f) {
			return f, nil
		}
		log.Printf("input: %s does not report the d-pad, searching", preferred)
		_ = f.Close()
	} else {
		log.Printf("input: open %s: %v, searching", preferred, err)
	}

	nodes, err := filepath.Glob(deviceGlob)
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", deviceGlob, err)
	}
	log.Printf("input: %d nodes in %s: %v", len(nodes), deviceGlob, nodes)

	for _, node := range nodes {
		if node == preferred {
			continue
		}
		f, err := os.Open(node)
		if err != nil {
			// Worth recording: a permission problem here looks identical to a
			// device that simply has no buttons.
			log.Printf("input: %s: %v", node, err)
			continue
		}
		if hasDPad(f) {
			log.Printf("input: using %s", node)
			return f, nil
		}
		log.Printf("input: %s has no d-pad", node)
		_ = f.Close()
	}
	return nil, fmt.Errorf("no input device in %s reports the d-pad, checked %d", deviceGlob, len(nodes))
}

// hasDPad asks the kernel which key codes a node can report and looks for the
// two the app cannot work without. Checking for EV_KEY alone is not enough,
// since a power button is also a key device.
func hasDPad(f *os.File) bool {
	var bits [keyBitmapBytes]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		eviocgbitKey, uintptr(unsafe.Pointer(&bits[0])))
	if errno != 0 {
		return false
	}
	return keySupported(bits[:], Up) && keySupported(bits[:], Down)
}

func keySupported(bits []byte, key Button) bool {
	i := int(key) / 8
	if i < 0 || i >= len(bits) {
		return false
	}
	return bits[i]&(1<<(uint(key)%8)) != 0
}

// Events returns the channel button events arrive on. It closes when the
// device is closed or hits an unrecoverable read error.
func (r *Reader) Events() <-chan Event { return r.events }

// Close stops reading and releases the device.
func (r *Reader) Close() error { return r.file.Close() }

// Err reports why the event stream ended. It is nil when the device was
// closed deliberately, and set when the read failed, which is the difference
// between the user quitting and the input device dying.
func (r *Reader) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Drain discards events that queued up while something else owned the screen.
// Without it, every button pressed during playback replays into the list the
// moment the video ends.
//
// It waits for the stream to go quiet rather than for the channel to be empty.
// The press that quit the video is still travelling from the kernel through
// the reader goroutine when playback returns, so an empty channel does not
// mean an idle device. Missing that press matters here because MENU is the
// key ffplay quits on and the same key exits Antenna.
func (r *Reader) Drain(quiet time.Duration) {
	timer := time.NewTimer(quiet)
	defer timer.Stop()

	for {
		select {
		case _, ok := <-r.events:
			if !ok {
				return
			}
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(quiet)
		case <-timer.C:
			return
		}
	}
}

func (r *Reader) loop() {
	defer close(r.events)
	buf := make([]byte, eventSize*8)
	for {
		n, err := r.file.Read(buf)
		if err != nil {
			// A closed device is the normal shutdown path. Anything else
			// killed the only way the user can talk to the app, and it must
			// not look like a clean exit.
			if !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
				r.mu.Lock()
				r.err = fmt.Errorf("read %s: %w", Device, err)
				r.mu.Unlock()
			}
			return
		}
		for off := 0; off+eventSize <= n; off += eventSize {
			rec := buf[off : off+eventSize]
			if binary.LittleEndian.Uint16(rec[8:10]) != evKey {
				continue
			}
			code := binary.LittleEndian.Uint16(rec[10:12])
			value := int32(binary.LittleEndian.Uint32(rec[12:16]))
			r.send(Event{
				Button:  Button(code),
				Pressed: value != 0,
				Repeat:  value == 2,
			})
		}
	}
}

// send delivers ev unless the consumer has fallen behind, in which case the
// event is dropped rather than stalling the reader.
func (r *Reader) send(ev Event) {
	select {
	case r.events <- ev:
	default:
	}
}
