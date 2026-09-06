// Package keyboard is an on-screen keyboard driven by a d-pad.
//
// There is no keyboard on this device and no system one an app can call. The
// terminal OnionOS ships draws its own, and this follows the same shape,
// because a user who has typed a Wi-Fi password on this handheld already knows
// how it works: the pad moves a cursor over a grid and one button presses the
// key under it.
//
// The package holds the grid and the text and nothing else. Drawing belongs to
// whatever owns the screen, which keeps every rule in here testable without a
// framebuffer.
package keyboard

import "strings"

// maxText caps the query. A search box on a 640 pixel panel stops being
// readable long before this, and an unbounded string typed one key at a time
// is not a real risk, but a cap costs nothing.
const maxText = 64

// Key is one position on the grid. Label is what to draw, which is not always
// the character it types: a space bar has to say something.
type Key struct {
	Label string
	Rune  rune
}

// rows is the layout. Digits first because titles often start with a year, then
// the usual three letter rows, then a space bar of its own so it cannot be
// pressed by accident while aiming for M.
var rows = [][]Key{
	keysOf("1234567890"),
	keysOf("qwertyuiop"),
	keysOf("asdfghjkl"),
	keysOf("zxcvbnm"),
	{{Label: "SPACE", Rune: ' '}},
}

func keysOf(chars string) []Key {
	keys := make([]Key, 0, len(chars))
	for _, r := range chars {
		keys = append(keys, Key{Label: string(r), Rune: r})
	}
	return keys
}

// Keyboard is the grid cursor and the text typed so far.
type Keyboard struct {
	row  int
	col  int
	text []rune
}

// New returns a keyboard with an empty query, cursor on the first letter row
// rather than the digits, since most searches start with a letter.
func New() *Keyboard {
	return &Keyboard{row: 1}
}

// Rows returns the layout for drawing. The caller must not modify it.
func Rows() [][]Key { return rows }

// Cursor is the row and column the cursor sits on.
func (k *Keyboard) Cursor() (row, col int) { return k.row, k.col }

// Type appends one character without moving the cursor, and reports whether it
// was accepted. Only characters the grid offers are, so this cannot produce a
// query the user could not have typed themselves.
func (k *Keyboard) Type(r rune) bool {
	if len(k.text) >= maxText || !onGrid(r) {
		return false
	}
	k.text = append(k.text, r)
	return true
}

func onGrid(r rune) bool {
	for _, line := range rows {
		for _, key := range line {
			if key.Rune == r {
				return true
			}
		}
	}
	return false
}

// Text is what has been typed.
func (k *Keyboard) Text() string { return string(k.text) }

// SetText replaces the query, which is how a search reopens with what was
// typed last time rather than making the user start again.
func (k *Keyboard) SetText(text string) {
	runes := []rune(text)
	if len(runes) > maxText {
		runes = runes[:maxText]
	}
	k.text = runes
}

// Move steps the cursor. Rows are different lengths, so moving between them
// clamps the column rather than wrapping to somewhere unrelated: a cursor that
// jumps sideways when you press down is disorienting.
func (k *Keyboard) Move(dRow, dCol int) {
	if dRow != 0 {
		k.row = clamp(k.row+dRow, 0, len(rows)-1)
		k.col = clamp(k.col, 0, len(rows[k.row])-1)
	}
	if dCol != 0 {
		k.col = clamp(k.col+dCol, 0, len(rows[k.row])-1)
	}
}

// Press types the key under the cursor and reports whether the text changed.
func (k *Keyboard) Press() bool {
	if len(k.text) >= maxText {
		return false
	}
	k.text = append(k.text, rows[k.row][k.col].Rune)
	return true
}

// Backspace removes the last character and reports whether anything changed.
func (k *Keyboard) Backspace() bool {
	if len(k.text) == 0 {
		return false
	}
	k.text = k.text[:len(k.text)-1]
	return true
}

// Clear empties the query.
func (k *Keyboard) Clear() { k.text = k.text[:0] }

// Matches reports whether title matches the query. Matching is case
// insensitive and anywhere in the title, because a user searching a list of
// cartoons is far more likely to remember a word from the middle of a name
// than how it starts.
func (k *Keyboard) Matches(title string) bool {
	if len(k.text) == 0 {
		return true
	}
	return strings.Contains(strings.ToLower(title), strings.ToLower(string(k.text)))
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
