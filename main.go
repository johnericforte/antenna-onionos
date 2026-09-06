// Command antenna is an OnionOS app for the Miyoo Mini Plus.
//
// Milestone 1 is the walking skeleton: it proves the cross-compile, the
// OnionOS package layout, framebuffer rendering, button input, and a clean
// exit back to the Onion menu. It does no networking.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"antenna/internal/fb"
	"antenna/internal/input"
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

// placeholderItems stands in for provider results until Milestone 2. The list
// is deliberately longer than one screen so scrolling is exercised.
var placeholderItems = []string{
	"Steamboat Willie (1928)",
	"Plane Crazy (1928)",
	"The Gallopin' Gaucho (1928)",
	"Balloon Land (1935)",
	"Summertime (1935)",
	"The Valiant Tailor (1934)",
	"Don Quixote (1934)",
	"Ali Baba (1936)",
	"The Cookie Carnival (1935)",
	"Susie, The Little Blue Coupe (1952)",
	"A Coy Decoy (1941)",
	"Ali Baba Bound (1940)",
	"Porky's Cafe (1942)",
	"Sailor (1940)",
	"Betty Boop: Snow White (1933)",
	"Popeye: A Dream Walking (1934)",
	"Superman: The Mad Scientist (1941)",
	"Gulliver's Travels (1939)",
	"Little Lulu: Eggs Don't Bounce (1943)",
	"Mighty Mouse: The Wreck of the Hesperus (1944)",
}

type app struct {
	screen   *fb.Framebuffer
	items    []string
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
		items:  placeholderItems,
		rows:   listHeight / rowHeight,
	}

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

// move shifts the selection by delta, clamping at both ends, and scrolls the
// window so the selection stays visible.
func (a *app) move(delta int) {
	a.selected += delta
	if a.selected < 0 {
		a.selected = 0
	}
	if a.selected >= len(a.items) {
		a.selected = len(a.items) - 1
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

	// List.
	maxTextWidth := width - sidePadding*2
	for row := 0; row < a.rows; row++ {
		index := a.offset + row
		if index >= len(a.items) {
			break
		}
		y := headerHeight + row*rowHeight
		label := fb.Truncate(a.items[index], maxTextWidth, textScale)
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

	position := fmt.Sprintf("%d/%d", a.selected+1, len(a.items))
	s.DrawText(width-sidePadding-fb.TextWidth(position, 1), footerY+13, position, 1, colorTextDim)

	s.Present()
}

// renderScrollbar draws a proportional thumb on the right edge of the list,
// and nothing at all when everything already fits.
func (a *app) renderScrollbar() {
	if len(a.items) <= a.rows {
		return
	}
	s := a.screen
	trackX := s.Width() - 4
	trackY := headerHeight
	trackH := a.rows * rowHeight

	s.Rect(trackX, trackY, 2, trackH, colorDivider)

	thumbH := trackH * a.rows / len(a.items)
	if thumbH < 12 {
		thumbH = 12
	}
	span := len(a.items) - a.rows
	thumbY := trackY
	if span > 0 {
		thumbY += (trackH - thumbH) * a.offset / span
	}
	s.Rect(trackX, thumbY, 2, thumbH, colorAccent)
}
