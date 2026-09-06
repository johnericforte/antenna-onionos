// Package fb draws to the Miyoo Mini's Linux framebuffer.
//
// The panel is 640x480 at 32 bits per pixel. Bytes are stored blue, green,
// red, alpha -- not the RGBA order Go's image package assumes. The panel is
// also mounted rotated 180 degrees, which is why OnionOS video playback passes
// "-vf hflip,vflip" to ffplay; Present applies the same rotation for us.
package fb

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	// FBIOGET_VSCREENINFO returns the variable screen geometry.
	fbioGetVScreenInfo = 0x4600

	defaultWidth  = 640
	defaultHeight = 480
	bytesPerPixel = 4
)

// Color is a packed BGRA8888 pixel.
type Color uint32

// RGB packs r, g, b into the framebuffer's native byte order, fully opaque.
func RGB(r, g, b uint8) Color {
	return Color(uint32(b) | uint32(g)<<8 | uint32(r)<<16 | 0xff<<24)
}

// Framebuffer owns the mmap'd display and an off-screen buffer. All drawing
// lands in the off-screen buffer; Present blits it to the panel in one pass so
// partial frames are never visible.
type Framebuffer struct {
	file   *os.File
	screen []byte // mmap'd panel memory
	back   []byte // off-screen buffer, same layout
	w, h   int
	rotate bool
}

// Open maps /dev/fb0 and queries its geometry, falling back to 640x480 if the
// ioctl is unavailable.
func Open() (*Framebuffer, error) {
	f, err := os.OpenFile("/dev/fb0", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/fb0: %w", err)
	}

	w, h := queryGeometry(f)
	length := w * h * bytesPerPixel

	screen, err := syscall.Mmap(int(f.Fd()), 0, length,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap framebuffer (%dx%d): %w", w, h, err)
	}

	return &Framebuffer{
		file:   f,
		screen: screen,
		back:   make([]byte, length),
		w:      w,
		h:      h,
		rotate: true,
	}, nil
}

// queryGeometry reads xres and yres from fb_var_screeninfo. Those are the
// first two uint32 fields of the struct, so we only need to read that far.
func queryGeometry(f *os.File) (int, int) {
	var info [8]uint32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		fbioGetVScreenInfo, uintptr(unsafe.Pointer(&info[0])))
	if errno != 0 || info[0] == 0 || info[1] == 0 {
		return defaultWidth, defaultHeight
	}
	return int(info[0]), int(info[1])
}

// Width returns the panel width in pixels.
func (f *Framebuffer) Width() int { return f.w }

// Height returns the panel height in pixels.
func (f *Framebuffer) Height() int { return f.h }

// Close unmaps the panel and releases the device.
func (f *Framebuffer) Close() error {
	if f.screen != nil {
		syscall.Munmap(f.screen)
		f.screen = nil
	}
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

// Clear fills the entire off-screen buffer with one color.
func (f *Framebuffer) Clear(c Color) {
	f.Rect(0, 0, f.w, f.h, c)
}

// Rect fills an axis-aligned rectangle, clipped to the panel.
func (f *Framebuffer) Rect(x, y, w, h int, c Color) {
	if w <= 0 || h <= 0 {
		return
	}
	x0, y0 := max(x, 0), max(y, 0)
	x1, y1 := min(x+w, f.w), min(y+h, f.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}

	// Build one row, then copy it down the rectangle.
	rowLen := (x1 - x0) * bytesPerPixel
	row := make([]byte, rowLen)
	for i := 0; i < rowLen; i += bytesPerPixel {
		putPixel(row[i:], c)
	}
	for y := y0; y < y1; y++ {
		off := (y*f.w + x0) * bytesPerPixel
		copy(f.back[off:off+rowLen], row)
	}
}

// Border draws a rectangle outline of the given thickness, inset nowhere.
func (f *Framebuffer) Border(x, y, w, h, thickness int, c Color) {
	f.Rect(x, y, w, thickness, c)
	f.Rect(x, y+h-thickness, w, thickness, c)
	f.Rect(x, y, thickness, h, c)
	f.Rect(x+w-thickness, y, thickness, h, c)
}

// SetPixel writes a single pixel to the off-screen buffer.
func (f *Framebuffer) SetPixel(x, y int, c Color) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	putPixel(f.back[(y*f.w+x)*bytesPerPixel:], c)
}

func putPixel(dst []byte, c Color) {
	dst[0] = byte(c)
	dst[1] = byte(c >> 8)
	dst[2] = byte(c >> 16)
	dst[3] = byte(c >> 24)
}

// Present copies the off-screen buffer to the panel, rotating 180 degrees to
// compensate for how the display is mounted.
func (f *Framebuffer) Present() {
	if !f.rotate {
		copy(f.screen, f.back)
		return
	}
	total := f.w * f.h
	for i := 0; i < total; i++ {
		src := i * bytesPerPixel
		dst := (total - 1 - i) * bytesPerPixel
		copy(f.screen[dst:dst+bytesPerPixel], f.back[src:src+bytesPerPixel])
	}
}
