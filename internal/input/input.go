// Package input reads the Miyoo Mini's buttons from the Linux evdev interface.
package input

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Device is the evdev node the Miyoo exposes its buttons on.
const Device = "/dev/input/event0"

// eventSize is sizeof(struct input_event) on a 32-bit kernel: two 4-byte
// timeval fields, two uint16, and one int32.
const eventSize = 16

// evKey is EV_KEY, the event type carrying button presses.
const evKey = 1

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
}

// Open starts reading events from path in the background.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	r := &Reader{file: f, events: make(chan Event, 32)}
	go r.loop()
	return r, nil
}

// Events returns the channel button events arrive on. It closes when the
// device is closed or hits an unrecoverable read error.
func (r *Reader) Events() <-chan Event { return r.events }

// Close stops reading and releases the device.
func (r *Reader) Close() error { return r.file.Close() }

func (r *Reader) loop() {
	defer close(r.events)
	buf := make([]byte, eventSize*8)
	for {
		n, err := r.file.Read(buf)
		if err != nil {
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
