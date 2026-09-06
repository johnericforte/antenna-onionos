package keyboard

import (
	"strings"
	"testing"
)

// typeOn presses a sequence of characters by walking the cursor to each one.
// It fails the test if a character is not on the grid, which is the check that
// the layout can actually produce the strings the tests claim.
func typeOn(t *testing.T, k *Keyboard, text string) {
	t.Helper()
	for _, want := range text {
		row, col, ok := find(want)
		if !ok {
			t.Fatalf("%q is not on the keyboard", want)
		}
		k.row, k.col = row, col
		if !k.Press() {
			t.Fatalf("pressing %q did nothing", want)
		}
	}
}

func find(r rune) (row, col int, ok bool) {
	for i, line := range rows {
		for j, key := range line {
			if key.Rune == r {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

func TestPressBuildsTheQuery(t *testing.T) {
	k := New()
	typeOn(t, k, "bugs bunny")

	if got := k.Text(); got != "bugs bunny" {
		t.Errorf("text is %q", got)
	}
}

func TestBackspaceRemovesOneCharacter(t *testing.T) {
	k := New()
	typeOn(t, k, "abc")

	if !k.Backspace() {
		t.Fatal("backspace on a non empty query did nothing")
	}
	if got := k.Text(); got != "ab" {
		t.Errorf("text is %q, want %q", got, "ab")
	}

	k.Clear()
	if k.Backspace() {
		t.Error("backspace on an empty query reported a change")
	}
	if got := k.Text(); got != "" {
		t.Errorf("text is %q after clearing", got)
	}
}

// Rows are different lengths. A cursor that jumps sideways when you press down
// is disorienting, so the column clamps instead of wrapping.
func TestMoveClampsBetweenRowsOfDifferentLengths(t *testing.T) {
	k := New()

	// Far right of the top row, which is the longest.
	k.row, k.col = 0, len(rows[0])-1
	k.Move(1, 0)
	if row, col := k.Cursor(); row != 1 || col > len(rows[1])-1 {
		t.Errorf("cursor is %d,%d which is off the row", row, col)
	}

	// Down onto the space bar, a row with one key.
	k.row, k.col = 3, len(rows[3])-1
	k.Move(1, 0)
	if row, col := k.Cursor(); row != 4 || col != 0 {
		t.Errorf("cursor is %d,%d, want the only key on the last row", row, col)
	}
}

func TestMoveStopsAtTheEdges(t *testing.T) {
	k := New()

	k.row, k.col = 0, 0
	k.Move(-1, 0)
	k.Move(0, -1)
	if row, col := k.Cursor(); row != 0 || col != 0 {
		t.Errorf("cursor left the grid at the top left: %d,%d", row, col)
	}

	last := len(rows) - 1
	k.row, k.col = last, len(rows[last])-1
	k.Move(1, 0)
	k.Move(0, 1)
	if row, col := k.Cursor(); row != last || col != len(rows[last])-1 {
		t.Errorf("cursor left the grid at the bottom right: %d,%d", row, col)
	}
}

// The space bar has to be reachable and has to type a space, not its label.
func TestSpaceKeyTypesASpace(t *testing.T) {
	k := New()
	row, col, ok := find(' ')
	if !ok {
		t.Fatal("there is no space key")
	}
	if rows[row][col].Label == " " {
		t.Error("the space key draws as a blank, so it looks like an empty slot")
	}

	k.row, k.col = row, col
	k.Press()
	if got := k.Text(); got != " " {
		t.Errorf("space typed %q", got)
	}
}

func TestMatchesIsCaseInsensitiveAndAnywhere(t *testing.T) {
	k := New()
	typeOn(t, k, "bunny")

	for _, title := range []string{"Bugs Bunny", "BUNNY", "a bunny tale"} {
		if !k.Matches(title) {
			t.Errorf("%q should match %q", title, k.Text())
		}
	}
	if k.Matches("Popeye") {
		t.Errorf("%q should not match %q", "Popeye", k.Text())
	}
}

// An empty query matches everything, which is what makes the list reappear
// when the user deletes what they typed.
func TestEmptyQueryMatchesEverything(t *testing.T) {
	k := New()
	if !k.Matches("anything at all") {
		t.Error("an empty query filtered something out")
	}
}

func TestTextIsCapped(t *testing.T) {
	k := New()
	k.SetText(strings.Repeat("a", maxText+20))

	if len([]rune(k.Text())) != maxText {
		t.Fatalf("text is %d runes, want it capped at %d", len([]rune(k.Text())), maxText)
	}
	if k.Press() {
		t.Error("a full query accepted another character")
	}
}

func TestSetTextRestoresAPreviousQuery(t *testing.T) {
	k := New()
	k.SetText("popeye")

	if got := k.Text(); got != "popeye" {
		t.Errorf("text is %q", got)
	}
	if !k.Backspace() {
		t.Error("a restored query could not be edited")
	}
	if got := k.Text(); got != "popey" {
		t.Errorf("text is %q after backspace", got)
	}
}

// Every key has to draw as something. A blank label is an invisible key.
func TestEveryKeyHasALabel(t *testing.T) {
	for i, line := range rows {
		if len(line) == 0 {
			t.Errorf("row %d is empty", i)
		}
		for j, key := range line {
			if strings.TrimSpace(key.Label) == "" {
				t.Errorf("key %d,%d has no label", i, j)
			}
			if key.Rune == 0 {
				t.Errorf("key %d,%d types nothing", i, j)
			}
		}
	}
}

// The cursor starts on letters, since most searches begin with one.
func TestNewStartsOnALetterRow(t *testing.T) {
	row, col := New().Cursor()
	r := rows[row][col].Rune
	if r < 'a' || r > 'z' {
		t.Errorf("cursor starts on %q, want a letter", r)
	}
}

// Type is how a caller enters a query without walking the cursor. It must
// accept only what the grid offers, so it cannot produce a query the user
// could not have typed.
func TestTypeAcceptsOnlyKeysOnTheGrid(t *testing.T) {
	k := New()

	for _, r := range "abc 123" {
		if !k.Type(r) {
			t.Fatalf("%q is on the grid but was rejected", r)
		}
	}
	if got := k.Text(); got != "abc 123" {
		t.Errorf("text is %q", got)
	}

	for _, r := range []rune{'!', 'A', '\n', 'é'} {
		if k.Type(r) {
			t.Errorf("%q is not on the grid but was accepted", r)
		}
	}
	if got := k.Text(); got != "abc 123" {
		t.Errorf("a rejected character changed the text to %q", got)
	}
}
