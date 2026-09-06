// Command antenna is an OnionOS app for the Miyoo Mini Plus.
//
// Milestone 2 browses the Internet Archive. The shipped item list is public
// domain animation, but nothing below knows that: it renders whatever the
// wired-in provider returns, and the provider drops anything this hardware
// cannot decode.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"antenna/internal/fb"
	"antenna/internal/input"
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

// loadTimeout bounds the whole startup fetch. The device is often out of Wi-Fi
// range, and an app that hangs on a black screen looks broken.
const loadTimeout = 30 * time.Second

type app struct {
	screen   *fb.Framebuffer
	source   provider.Provider
	entries  []provider.Entry
	status   string
	selected int
	offset   int
	rows     int
}

func main() {
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

	listHeight := screen.Height() - headerHeight - footerHeight
	a := &app{
		screen: screen,
		source: provider.NewArchive(defaultItems),
		status: "Loading...",
		rows:   listHeight / rowHeight,
	}
	a.render()

	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	a.load(ctx)

	// Onion sends SIGTERM when the user backs out from the menu side.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	a.render()

	for {
		select {
		case <-signals:
			return nil
		case ev, ok := <-buttons.Events():
			if !ok {
				return nil
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
// A failure is shown in the list area rather than returned. The user can still
// read the reason and press B, which beats exiting to the Onion menu with no
// explanation at all.
func (a *app) load(ctx context.Context) {
	var failures int

	roots, err := a.source.Browse(ctx, "")
	if err != nil {
		a.status = err.Error()
		return
	}

	for _, root := range roots {
		titles, err := a.source.Browse(ctx, root.ID)
		if err != nil {
			failures++
			continue
		}
		a.entries = append(a.entries, titles...)
	}

	switch {
	case len(a.entries) > 0:
		a.status = ""
	case failures > 0:
		a.status = "Cannot reach archive.org. Check Wi-Fi."
	default:
		a.status = "Nothing here plays on this device."
	}
}

// move shifts the selection by delta, clamping at both ends, and scrolls the
// window so the selection stays visible.
func (a *app) move(delta int) {
	if len(a.entries) == 0 {
		return
	}
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
	s.DrawText(sidePadding, footerY+13, "D-PAD MOVE   L/R PAGE   B EXIT", 1, colorTextDim)

	if len(a.entries) > 0 {
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
