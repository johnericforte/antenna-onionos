// Command antenna is an OnionOS app for the Miyoo Mini Plus.
//
// It browses the Internet Archive and hands the chosen stream to ffplay. The
// shipped item list is public domain animation, but nothing below knows that:
// it renders whatever the wired-in provider returns, and the provider drops
// anything this hardware cannot decode.
//
// launch.sh redirects both streams to antenna.log on the card, so anything
// written to stderr here is the only forensic trail a handheld with no console
// ever leaves.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"antenna/internal/fb"
	"antenna/internal/input"
	"antenna/internal/player"
	"antenna/internal/provider"
)

const (
	headerHeight = 52
	footerHeight = 40
	rowHeight    = 32
	sidePadding  = 16
	textScale    = 2
	titleScale   = 3
)

// Palette. Dark by default: the panel is small, transflective, and often used
// in the dark, and a light background at this size is genuinely unpleasant.
var (
	colorBackground = fb.RGB(18, 18, 22)
	colorHeader     = fb.RGB(28, 28, 36)
	colorAccent     = fb.RGB(255, 138, 0)
	colorText       = fb.RGB(236, 236, 240)
	colorTextDim    = fb.RGB(130, 130, 142)
	colorSelectedBg = fb.RGB(255, 138, 0)
	colorSelectedFg = fb.RGB(18, 18, 22)
	colorDivider    = fb.RGB(44, 44, 54)
)

// defaultItems is the shipped provider configuration. These three archive.org
// items were measured on 2026-09-06: the first has an h.264 derivative on every
// title, the other two are majority undecodable and exist here so the skip path
// is exercised in the real app, not only in tests.
var defaultItems = []provider.Item{
	{ID: "classic_cartoons_201603", Title: "Classic Cartoons"},
	{ID: "disneycartoons-publicdomain", Title: "Disney Public Domain"},
	{ID: "pdcartooncollection", Title: "Public Domain Cartoons"},
}

const (
	// loadTimeout bounds the whole startup fetch. The device is often out of
	// Wi-Fi range, and an app that hangs on a black screen looks broken.
	loadTimeout = 30 * time.Second

	// resolveTimeout bounds the single metadata call made when a title is
	// chosen. It is shorter than the startup fetch because the user has just
	// pressed a button and is watching for something to happen.
	resolveTimeout = 20 * time.Second

	// drainQuiet is how long the buttons must be silent before the app trusts
	// that the press which quit the video has been discarded.
	drainQuiet = 150 * time.Millisecond
)

// display is what the app draws through. The interface exists so the list and
// every failure path can be tested without /dev/fb0.
type display interface {
	Width() int
	Height() int
	Clear(fb.Color)
	Rect(x, y, w, h int, c fb.Color)
	DrawText(x, y int, s string, scale int, c fb.Color)
	Present()
	GeometryChanged() bool
}

// videoPlayer hands a resolved stream to something that can decode it.
type videoPlayer interface {
	Play(ctx context.Context, url string) error
}

// buttonSource is the part of the input reader the app uses after playback.
type buttonSource interface {
	Drain(quiet time.Duration)
}

type app struct {
	screen  display
	source  provider.Provider
	video   videoPlayer
	buttons buttonSource

	// quit is cancelled when Onion asks the app to stop. It is what makes a
	// video interruptible, since the event loop is blocked while one plays.
	quit context.Context

	entries  []provider.Entry
	status   string
	notice   string
	selected int
	offset   int
	rows     int
}

func main() {
	// launch.sh points stderr at antenna.log on the card. Timestamps matter
	// there: without them the log cannot say whether something failed at
	// startup or an hour in.
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "antenna: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	screen, err := fb.Open()
	if err != nil {
		return err
	}
	defer screen.Close()

	buttons, err := input.Open(input.Device)
	if err != nil {
		return err
	}
	defer buttons.Close()

	// Onion sends SIGTERM when the user backs out from the menu side. The
	// event loop cannot see that while a video is playing, so the signal lands
	// on a context that reaches ffplay instead of only on a channel.
	quit, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listHeight := screen.Height() - headerHeight - footerHeight
	a := &app{
		screen:  screen,
		source:  provider.NewArchive(defaultItems),
		video:   player.New(),
		buttons: buttons,
		quit:    quit,
		status:  "Loading...",
		rows:    listHeight / rowHeight,
	}
	a.render()

	ctx, cancel := context.WithTimeout(quit, loadTimeout)
	a.load(ctx)
	cancel()

	a.render()

	for {
		select {
		case <-quit.Done():
			return nil
		case ev, ok := <-buttons.Events():
			if !ok {
				// The device closing is a clean exit. The device failing is
				// not, and must not look like the user pressing B.
				return buttons.Err()
			}
			if !ev.Pressed {
				continue
			}
			switch ev.Button {
			case input.Up:
				a.move(-1)
			case input.Down:
				a.move(1)
			case input.L1:
				a.move(-a.rows)
			case input.R1:
				a.move(a.rows)
			case input.A:
				a.play()
			case input.B, input.Menu:
				return nil
			default:
				continue
			}
			a.render()
		}
	}
}

// load fills the list from the provider. Every item is browsed and the
// playable titles are flattened into one list, because two of the three
// shipped items are mostly undecodable and folders of four titles are not
// worth a level of navigation.
//
// A failure is shown rather than returned. The user can still read the reason
// and press B, which beats exiting to the Onion menu with no explanation.
func (a *app) load(ctx context.Context) {
	a.entries = nil
	a.notice = ""

	roots, err := a.source.Browse(ctx, "")
	if err != nil {
		log.Printf("browse root: %v", err)
		a.status = a.forScreen(err)
		return
	}

	var failed error
	var failures int
	for _, root := range roots {
		titles, err := a.source.Browse(ctx, root.ID)
		if err != nil {
			// The text is the only thing separating a dead identifier from an
			// expired certificate from a real network fault, and all three
			// would otherwise be reported as bad Wi-Fi.
			log.Printf("browse %s: %v", root.ID, err)
			failures++
			failed = err
			continue
		}
		a.entries = append(a.entries, titles...)
	}

	switch {
	case len(a.entries) > 0:
		a.status = ""
		if failures > 0 {
			// A partial list looks complete, so say what is missing.
			a.notice = fmt.Sprintf("%d of %d collections did not load", failures, len(roots))
		}
	case failures > 0:
		a.status = a.forScreen(failed)
	default:
		a.status = "Nothing here plays on this device."
	}
}

// play resolves the selected title and hands it to ffplay, then takes the
// screen and the buttons back when playback ends.
//
// Every failure here lands in the footer instead of ending the app. Losing a
// video to a dead node or a stale identifier is normal, and the list is still
// worth browsing afterwards.
func (a *app) play() {
	if len(a.entries) == 0 {
		a.notice = "Nothing to play"
		return
	}
	entry := a.entries[a.selected]

	// Resolving needs a network round trip, so say something first.
	a.notice = "Opening " + entry.Title
	a.render()

	ctx, cancel := context.WithTimeout(a.quit, resolveTimeout)
	stream, err := a.source.Resolve(ctx, entry.ID)
	cancel()
	if err != nil {
		log.Printf("resolve %s: %v", entry.ID, err)
		a.notice = a.forScreen(err)
		return
	}
	if playable, reason := stream.Playable(); !playable {
		// Playable writes its reasons for the screen already.
		a.notice = reason
		return
	}

	a.notice = ""
	// From here ffplay owns the framebuffer and every button pressed while it
	// runs, so the queued presses have to go whether or not playback worked.
	// Before this point nothing took the buttons, and a deferred drain would
	// only stall the footer message.
	defer a.buttons.Drain(drainQuiet)

	// No timeout. A feature runs as long as it runs, and only Onion asking the
	// app to quit stops it early.
	if err := a.video.Play(a.quit, stream.URL); err != nil {
		// A cancelled context means the app is shutting down, so ffplay dying
		// is the intended outcome rather than something to report.
		if a.quit.Err() == nil {
			a.notice = a.forScreen(err)
		}
	}

	if a.screen.GeometryChanged() {
		log.Print("framebuffer geometry changed during playback")
		a.notice = "Screen mode changed. Restart Antenna."
	}
}

// forScreen turns an error into a line that fits the footer.
//
// Go errors are written lowercase by convention and wrap outward, so the
// useful part sits at the end, which is exactly where truncation eats it. When
// the message is too long the first clause and the last one survive: the first
// says what failed, the last says why.
func (a *app) forScreen(err error) string {
	msg := err.Error()
	if msg == "" {
		return "Something went wrong"
	}

	// Measured against the real panel in the font's own units, rather than
	// against a byte count that disagrees with it on any non-ASCII message.
	if fb.TextWidth(msg, 1) > a.screen.Width()-sidePadding*2 {
		if first, rest, ok := strings.Cut(msg, ": "); ok {
			if i := strings.LastIndex(rest, ": "); i >= 0 {
				rest = rest[i+2:]
			}
			msg = first + ": " + rest
		}
	}

	r := []rune(msg)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// move shifts the selection by delta, clamping at both ends, and scrolls the
// window so the selection stays visible.
func (a *app) move(delta int) {
	if len(a.entries) == 0 {
		return
	}
	a.notice = ""
	a.selected += delta
	if a.selected < 0 {
		a.selected = 0
	}
	if a.selected >= len(a.entries) {
		a.selected = len(a.entries) - 1
	}
	if a.selected < a.offset {
		a.offset = a.selected
	}
	if a.selected >= a.offset+a.rows {
		a.offset = a.selected - a.rows + 1
	}
}

func (a *app) render() {
	s := a.screen
	width := s.Width()

	s.Clear(colorBackground)

	// Header.
	s.Rect(0, 0, width, headerHeight, colorHeader)
	s.Rect(0, headerHeight-2, width, 2, colorAccent)
	s.DrawText(sidePadding, 14, "Antenna", titleScale, colorText)

	// List, or the reason there is not one.
	maxTextWidth := width - sidePadding*2
	if len(a.entries) == 0 {
		label := fb.Truncate(a.status, maxTextWidth, textScale)
		s.DrawText(sidePadding, headerHeight+rowHeight, label, textScale, colorTextDim)
	}
	for row := 0; row < a.rows; row++ {
		index := a.offset + row
		if index >= len(a.entries) {
			break
		}
		y := headerHeight + row*rowHeight
		label := fb.Truncate(a.entries[index].Title, maxTextWidth, textScale)
		textY := y + (rowHeight-fb.CellHeight*textScale)/2

		if index == a.selected {
			s.Rect(0, y, width, rowHeight, colorSelectedBg)
			s.DrawText(sidePadding, textY, label, textScale, colorSelectedFg)
		} else {
			s.DrawText(sidePadding, textY, label, textScale, colorText)
			s.Rect(sidePadding, y+rowHeight-1, width-sidePadding*2, 1, colorDivider)
		}
	}

	a.renderScrollbar()

	// Footer.
	footerY := s.Height() - footerHeight
	s.Rect(0, footerY, width, footerHeight, colorHeader)
	s.Rect(0, footerY, width, 1, colorDivider)

	hint := "D-PAD MOVE   A PLAY   L/R PAGE   B EXIT"
	if a.notice != "" {
		hint = a.notice
	}
	s.DrawText(sidePadding, footerY+13, fb.Truncate(hint, maxTextWidth, 1), 1, colorTextDim)

	if a.notice == "" && len(a.entries) > 0 {
		position := fmt.Sprintf("%d/%d", a.selected+1, len(a.entries))
		s.DrawText(width-sidePadding-fb.TextWidth(position, 1), footerY+13, position, 1, colorTextDim)
	}

	s.Present()
}

// renderScrollbar draws a proportional thumb on the right edge of the list,
// and nothing at all when everything already fits.
func (a *app) renderScrollbar() {
	if len(a.entries) <= a.rows {
		return
	}
	s := a.screen
	trackX := s.Width() - 4
	trackY := headerHeight
	trackH := a.rows * rowHeight

	s.Rect(trackX, trackY, 2, trackH, colorDivider)

	thumbH := trackH * a.rows / len(a.entries)
	if thumbH < 12 {
		thumbH = 12
	}
	span := len(a.entries) - a.rows
	thumbY := trackY
	if span > 0 {
		thumbY += (trackH - thumbH) * a.offset / span
	}
	s.Rect(trackX, thumbY, 2, thumbH, colorAccent)
}
