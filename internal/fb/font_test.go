package fb

import "testing"

// Every glyph must pack to exactly glyphHeight rows of at most glyphWidth
// bits, or text renders with stray pixels that are painful to spot by eye.
func TestGlyphsWellFormed(t *testing.T) {
	if len(glyphs) != len(glyphArt) {
		t.Fatalf("packed %d glyphs from %d definitions", len(glyphs), len(glyphArt))
	}
	maxBits := uint8(1<<glyphWidth - 1)
	for r, mask := range glyphs {
		for row, bits := range mask {
			if bits > maxBits {
				t.Errorf("glyph %q row %d has bits outside %d columns: %05b",
					r, row, glyphWidth, bits)
			}
		}
	}
}

func TestPrintableASCIICovered(t *testing.T) {
	for r := rune(' '); r <= '~'; r++ {
		if _, ok := glyphs[r]; !ok {
			t.Errorf("no glyph for %q (%d)", r, r)
		}
	}
}

// Spot-check one glyph end to end so a broken parser cannot pass silently.
func TestGlyphPacking(t *testing.T) {
	// 'T' is "#####/..#../..#../..#../..#../..#../..#.."
	want := [glyphHeight]uint8{0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100}
	if got := glyphs['T']; got != want {
		t.Fatalf("glyph 'T' = %05b, want %05b", got, want)
	}
}

func TestSpaceIsBlank(t *testing.T) {
	for row, bits := range glyphs[' '] {
		if bits != 0 {
			t.Fatalf("space glyph row %d is not blank: %05b", row, bits)
		}
	}
}

func TestTextWidth(t *testing.T) {
	if got, want := TextWidth("abc", 1), 3*CellWidth; got != want {
		t.Errorf("TextWidth(scale 1) = %d, want %d", got, want)
	}
	if got, want := TextWidth("abc", 2), 3*CellWidth*2; got != want {
		t.Errorf("TextWidth(scale 2) = %d, want %d", got, want)
	}
	if got := TextWidth("", 1); got != 0 {
		t.Errorf("TextWidth(empty) = %d, want 0", got)
	}
}

func TestTruncate(t *testing.T) {
	const scale = 1

	t.Run("short strings pass through", func(t *testing.T) {
		if got := Truncate("hi", 640, scale); got != "hi" {
			t.Errorf("got %q, want %q", got, "hi")
		}
	})

	t.Run("long strings gain an ellipsis and fit", func(t *testing.T) {
		const limit = 60
		got := Truncate("Mighty Mouse: The Wreck of the Hesperus", limit, scale)
		if got == "" {
			t.Fatal("truncated to nothing")
		}
		if TextWidth(got, scale) > limit {
			t.Errorf("result %q is %d px, over the %d px limit",
				got, TextWidth(got, scale), limit)
		}
		if len(got) < 4 || got[len(got)-3:] != "..." {
			t.Errorf("expected trailing ellipsis, got %q", got)
		}
	})

	t.Run("impossible limits yield empty", func(t *testing.T) {
		if got := Truncate("anything", 1, scale); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// The framebuffer geometry ioctl writes the whole of fb_var_screeninfo, and
// the request number carries no size to bound it. A buffer smaller than the
// struct lets the kernel write past the end of a stack array, which smashes
// the goroutine stack and crashes in a way the Go runtime cannot even unwind.
// That shipped once. This pins the size.
func TestVarScreenInfoIsTheWholeStruct(t *testing.T) {
	const sizeofFbVarScreenInfo = 160 // linux/fb.h, 32-bit

	if got := varScreenInfoWords * 4; got != sizeofFbVarScreenInfo {
		t.Fatalf("geometry buffer is %d bytes, want %d", got, sizeofFbVarScreenInfo)
	}
}
