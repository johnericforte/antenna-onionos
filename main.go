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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"antenna/internal/config"
	"antenna/internal/dbg"
	"antenna/internal/fb"
	"antenna/internal/input"
	"antenna/internal/keyboard"
	"antenna/internal/player"
	"antenna/internal/provider"
	"antenna/internal/relay"
)

const (
	headerHeight = 52
	footerHeight = 40
	rowHeight    = 32
	sidePadding  = 16
	textScale    = 2
	titleScale   = 3

	// Keyboard grid. Ten keys plus the side padding have to fit across 640
	// pixels, and a key has to be big enough to see which one the cursor is on.
	keyWidth  = 60
	keyHeight = 46
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

// appDir is where OnionOS installs the app, and where the item list lives.
const appDir = "/mnt/SDCARD/App/Antenna"

// defaultItems is what the app browses when the card carries no item list.
// These three archive.org items were measured on 2026-09-06: the first has an
// h.264 derivative on every title, the other two are majority undecodable and
// exist here so the skip path is exercised in the real app, not only in tests.
var defaultItems = []provider.Item{
	{Kind: provider.ArchiveItem, Ref: "classic_cartoons_201603", Title: "Classic Cartoons"},
	{Kind: provider.ArchiveItem, Ref: "disneycartoons-publicdomain", Title: "Disney Public Domain"},
	{Kind: provider.ArchiveItem, Ref: "pdcartooncollection", Title: "Public Domain Cartoons"},
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
	Border(x, y, w, h, thickness int, c fb.Color)
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

// screen is which view the app is showing. There are two, and they are a
// value rather than a stack: a handheld with six buttons does not want
// nested navigation.
type view int

const (
	listView view = iota
	settingsView
	searchView
)

// settingRow is one line on the settings screen. Actions and toggles share the
// screen, so each row says how to draw itself and what A does to it.
type settingRow struct {
	label string
	// value renders the right hand side, empty for an action.
	value func(*app) string
	// activate runs when A is pressed on the row.
	activate func(*app)
}

// settingRows is a function rather than a package variable because one of its
// actions renders, and rendering reads the rows: as a variable that is an
// initialization cycle. Two rows are cheap to build on demand.
func settingRows() []settingRow {
	return []settingRow{
		{
			label: "Trace logging",
			// Reads the live state, not the saved one. The environment can
			// turn tracing on too, and a row that showed only what was saved
			// would say Off while the log filled up.
			value: func(*app) string {
				if dbg.On() {
					return "On"
				}
				return "Off"
			},
			activate: func(a *app) { a.toggleLogging() },
		},
		{
			label:    "Reload sources",
			activate: func(a *app) { a.reload() },
		},
	}
}

type app struct {
	screen  display
	source  provider.Provider
	video   videoPlayer
	buttons buttonSource

	// settingsPath is where the settings screen persists to. Empty in tests
	// that do not care, which skips the write.
	settingsPath string
	settings     config.Settings

	// itemsPath is the item list. Reload re-reads it, so a corrected file
	// takes effect without restarting. Empty when the caller supplied the
	// provider directly, as tests do.
	itemsPath string

	view         view
	settingIndex int

	// keys is the on-screen keyboard, and query is what it last confirmed.
	// The full list is kept so deleting a search restores it without going
	// back to the network.
	keys       *keyboard.Keyboard
	allEntries []provider.Entry

	// quit is cancelled when Onion asks the app to stop. It is what makes a
	// video interruptible, since the event loop is blocked while one plays.
	quit context.Context

	entries []provider.Entry
	status  string
	notice  string

	// banner is what loading found, and unlike notice it survives navigation.
	// It is the only trace of a partial load, and clearing it on the first
	// press meant a user who pressed Down never saw it again.
	banner   string
	selected int
	offset   int
	rows     int
}

func main() {
	// launch.sh points stderr at antenna.log on the card, which is the only
	// place a handheld can report anything after the fact.
	if dbg.Init() {
		dbg.Printf("antenna: tracing on, set %s=0 to quiet it", dbg.EnvVar)
	}

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

	settingsPath := filepath.Join(appDir, config.SettingsFileName)
	settings, problems, settingsErr := config.LoadSettings(settingsPath)
	if settingsErr != nil {
		dbg.Fail("settings: %v", settingsErr)
	}
	for _, problem := range problems {
		dbg.Fail("settings: %s", problem)
	}
	// The saved choice wins over the environment in both directions, so
	// turning the trace off in the app is not undone by a debug file left on
	// the card. A settings file that could not be read leaves the environment
	// alone rather than silently overriding it.
	if settingsErr != nil {
		settings.Logging = dbg.On()
	} else {
		dbg.SetEnabled(settings.Logging)
	}

	itemsPath := filepath.Join(appDir, config.FileName)
	items, itemsErr := config.Load(itemsPath)
	switch {
	case itemsErr != nil:
		// The user edited the list and got it wrong. Browsing the defaults
		// instead would hide that, so say so and browse nothing.
		dbg.Fail("config: %v", itemsErr)
	case items == nil:
		dbg.Printf("config: no %s, using the %d shipped items", config.FileName, len(defaultItems))
		items = defaultItems
	default:
		dbg.Printf("config: %d items from %s", len(items), config.FileName)
	}

	listHeight := screen.Height() - headerHeight - footerHeight
	a := &app{
		screen:       screen,
		source:       provider.NewSources(items),
		video:        player.New(),
		buttons:      buttons,
		settingsPath: settingsPath,
		settings:     settings,
		itemsPath:    itemsPath,
		quit:         quit,
		status:       "Loading...",
		rows:         listHeight / rowHeight,
	}
	a.render()

	dbg.Printf("antenna: screen %dx%d, %d rows", screen.Width(), screen.Height(), a.rows)

	// A rejected item list is shown and then the app keeps running, so B still
	// exits and the user can read the reason before going to fix the file.
	if itemsErr != nil {
		a.status = a.forScreen(itemsErr)
	} else {
		ctx, cancel := context.WithTimeout(quit, loadTimeout)
		loadStart := time.Now()
		a.load(ctx)
		cancel()
		dbg.Printf("antenna: loaded %d titles in %s, status=%q notice=%q",
			len(a.entries), time.Since(loadStart).Round(time.Millisecond), a.status, a.notice)
	}

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
			dbg.Printf("input: button %d pressed", ev.Button)
			if a.handle2(ev) == exitApp {
				return nil
			}
			a.render()
		}
	}
}

// action is what the event loop does after a button press.
type action int

const (
	stayOpen action = iota
	exitApp
)

// handle routes one button press to whichever view is showing.
func (a *app) handle(button input.Button) action {
	return a.handle2(input.Event{Button: button, Pressed: true})
}

// handle2 is handle with the repeat flag, which only the settings screen cares
// about. Holding a direction to scroll is wanted; holding A on a toggle that
// writes to the card at the repeat rate is not.
func (a *app) handle2(ev input.Event) action {
	switch a.view {
	case settingsView:
		return a.handleSettings(ev)
	case searchView:
		return a.handleSearch(ev.Button)
	default:
		return a.handleList(ev.Button)
	}
}

func (a *app) handleList(button input.Button) action {
	switch button {
	case input.Up:
		a.move(-1)
	case input.Down:
		a.move(1)
	case input.L1:
		a.jumpLetter(-1)
	case input.R1:
		a.jumpLetter(1)
	case input.A:
		a.play()
	case input.Start:
		a.view = settingsView
		a.settingIndex = 0
		a.notice = ""
	case input.Select:
		a.openSearch()
	case input.B, input.Menu:
		return exitApp
	}
	return stayOpen
}

// openSearch shows the keyboard, carrying whatever was searched for last so a
// small correction does not mean typing the whole thing again on a d-pad.
func (a *app) openSearch() {
	if a.keys == nil {
		a.keys = keyboard.New()
	}
	a.view = searchView
	a.notice = ""
}

// handleSearch drives the keyboard. The bindings follow the terminal keyboard
// OnionOS ships, because a user who has typed a Wi-Fi password on this device
// already knows them.
func (a *app) handleSearch(button input.Button) action {
	switch button {
	case input.Up:
		a.keys.Move(-1, 0)
	case input.Down:
		a.keys.Move(1, 0)
	case input.Left:
		a.keys.Move(0, -1)
	case input.Right:
		a.keys.Move(0, 1)
	case input.A:
		if a.keys.Press() {
			a.applyFilter()
		}
	case input.B:
		if a.keys.Backspace() {
			a.applyFilter()
		}
	case input.Start:
		// Confirm: keep the filter and go back to the list.
		a.view = listView
	case input.Select, input.Menu:
		// Cancel: drop the filter entirely and restore the full list.
		a.keys.Clear()
		a.applyFilter()
		a.view = listView
	}
	return stayOpen
}

// applyFilter narrows the list to the titles matching what has been typed. It
// runs on every keystroke, so the list is already filtered by the time the
// user looks up from the keyboard.
func (a *app) applyFilter() {
	if a.allEntries == nil {
		a.allEntries = a.entries
	}

	if a.keys == nil || a.keys.Text() == "" {
		a.entries = a.allEntries
	} else {
		filtered := make([]provider.Entry, 0, len(a.allEntries))
		for _, entry := range a.allEntries {
			if a.keys.Matches(entry.Title) {
				filtered = append(filtered, entry)
			}
		}
		a.entries = filtered
	}

	// The old selection means nothing once the list changed underneath it.
	a.selected, a.offset = 0, 0
}

// handleSettings keeps B on the settings screen meaning "back to the list"
// rather than "quit", because losing the whole app to a mispress while
// changing a setting is a bad trade.
func (a *app) handleSettings(ev input.Event) action {
	switch ev.Button {
	case input.Up:
		if a.settingIndex > 0 {
			a.settingIndex--
		}
	case input.Down:
		if a.settingIndex < len(settingRows())-1 {
			a.settingIndex++
		}
	case input.A:
		// A repeat here would toggle the setting and rewrite the card once per
		// repeat, landing on whichever state the release happened to hit.
		if ev.Repeat {
			return stayOpen
		}
		settingRows()[a.settingIndex].activate(a)
	case input.B, input.Start, input.Menu:
		a.view = listView
	}
	return stayOpen
}

// toggleLogging flips the trace on or off, applies it immediately and writes it
// down, so the choice survives a reboot.
func (a *app) toggleLogging() {
	// Flip what is actually happening rather than what was last saved. With a
	// debug file on the card and logging=false in settings, the two disagree,
	// and toggling the saved value would take two presses to do anything.
	next := !dbg.On()
	dbg.SetEnabled(next)
	a.settings.Logging = next
	dbg.Printf("settings: logging now %t", next)

	if a.settingsPath == "" {
		return
	}
	if err := config.SaveSettings(a.settingsPath, a.settings); err != nil {
		// A read only card is a routine outcome after an unclean shutdown.
		// Leaving the change applied would show a state that quietly reverts
		// at the next launch, so it is rolled back and said out loud.
		dbg.Fail("settings: %v", err)
		a.settings.Logging = !next
		dbg.SetEnabled(!next)
		a.notice = "Could not save to the card, so the change was undone"
	}
}

// reload browses every source again. Wi-Fi that was not up at startup is the
// common case, and without this the only cure is quitting to the Onion menu
// and starting over.
func (a *app) reload() {
	a.view = listView
	a.status = "Loading..."
	a.entries = nil
	a.allEntries = nil
	a.selected, a.offset = 0, 0
	a.render()

	// Re-read the item list first. Without this, reloading after a rejected
	// items.txt would replace the message naming the bad line with "Nothing
	// here plays on this device", which is both wrong and unactionable.
	if a.itemsPath != "" {
		items, err := config.Load(a.itemsPath)
		if err != nil {
			dbg.Fail("config: %v", err)
			a.status = a.forScreen(err)
			return
		}
		if items == nil {
			items = defaultItems
		}
		a.source = provider.NewSources(items)
	}

	ctx, cancel := context.WithTimeout(a.quit, loadTimeout)
	defer cancel()
	a.load(ctx)
}

// jumpLetter moves to the next title starting with a different letter, which
// is how you cross a long list on a d-pad without holding a direction for
// twenty seconds.
func (a *app) jumpLetter(direction int) {
	if len(a.entries) == 0 {
		return
	}
	a.notice = ""

	current := firstLetter(a.entries[a.selected].Title)
	for i := a.selected + direction; i >= 0 && i < len(a.entries); i += direction {
		if firstLetter(a.entries[i].Title) == current {
			continue
		}
		if direction > 0 {
			a.moveTo(i)
			return
		}
		// Going back, the first entry of a different letter is that group's
		// last title. Keep walking to its first, or the group is unreachable
		// from below and only holding Up gets you there.
		letter := firstLetter(a.entries[i].Title)
		for i > 0 && firstLetter(a.entries[i-1].Title) == letter {
			i--
		}
		a.moveTo(i)
		return
	}
	// Nothing further along starts differently, so go to that end of the list.
	if direction > 0 {
		a.moveTo(len(a.entries) - 1)
		return
	}
	a.moveTo(0)
}

// firstLetter is what two titles are compared on when jumping. Case and
// leading punctuation would otherwise split a letter into several groups.
func firstLetter(title string) rune {
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
	}
	return 0
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
	a.allEntries = nil
	a.notice = ""
	a.banner = ""

	roots, err := a.source.Browse(ctx, "")
	if err != nil {
		dbg.Fail("browse root: %v", err)
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
			dbg.Fail("browse %s: %v", root.ID, err)
			failures++
			failed = err
			continue
		}
		a.entries = append(a.entries, titles...)
	}

	a.allEntries = a.entries

	// The status describes what loading found, so it is decided before any
	// search narrows the list. Otherwise a filter matching nothing reports
	// that the device cannot decode anything, which is a different problem.
	switch {
	case len(a.allEntries) > 0:
		a.status = ""
		if failures > 0 {
			// A partial list looks complete, so say what is missing.
			a.banner = fmt.Sprintf("%d of %d sources did not load", failures, len(roots))
		}
	case failures > 0:
		a.status = a.forScreen(failed)
	default:
		a.status = "Nothing here plays on this device."
	}

	// A search that was running before a reload still applies afterwards.
	a.applyFilter()
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
	dbg.Printf("play: selected %d/%d id=%q title=%q", a.selected+1, len(a.entries), entry.ID, entry.Title)

	// Resolving needs a network round trip, so say something first.
	a.notice = "Opening " + entry.Title
	a.render()

	ctx, cancel := context.WithTimeout(a.quit, resolveTimeout)
	started := time.Now()
	stream, err := a.source.Resolve(ctx, entry.ID)
	cancel()
	dbg.Printf("play: resolve took %s", time.Since(started).Round(time.Millisecond))
	if err != nil {
		dbg.Fail("resolve %s: %v", entry.ID, err)
		a.notice = a.forScreen(err)
		return
	}
	dbg.Printf("play: stream %s %s %dx%d %d bps url=%s",
		stream.Kind, stream.Codec, stream.Width, stream.Height, stream.Bitrate, stream.URL)

	if playable, reason := stream.Playable(); !playable {
		// Playable writes its reasons for the screen already.
		dbg.Fail("play: refused: %s", reason)
		a.notice = reason
		return
	}

	a.notice = ""
	// From here ffplay owns the framebuffer and every button pressed while it
	// runs, so the queued presses have to go whether or not playback worked.
	// Before this point nothing took the buttons, and a deferred drain would
	// only stall the footer message.
	defer a.buttons.Drain(drainQuiet)

	// The ffplay OnionOS ships has no TLS, and archive.org is https only, so
	// the stream is fetched here and served to the player over loopback.
	feed, err := relay.Start(stream.URL)
	if err != nil {
		dbg.Fail("play: relay: %v", err)
		a.notice = a.forScreen(err)
		return
	}
	defer func() {
		if err := feed.Close(); err != nil {
			dbg.Fail("play: closing relay: %v", err)
		}
	}()

	// No timeout. A feature runs as long as it runs, and only Onion asking the
	// app to quit stops it early.
	dbg.Printf("play: handing off to the player at %s", feed.URL())
	if err := a.video.Play(a.quit, feed.URL()); err != nil {
		// A cancelled context means the app is shutting down, so ffplay dying
		// is the intended outcome rather than something to report.
		if a.quit.Err() == nil {
			a.notice = a.forScreen(err)
		}
	}

	// A relay failure explains a player failure, and it is more specific.
	if err := feed.Err(); err != nil {
		dbg.Fail("play: relay reported %v", err)
		if a.notice == "" {
			a.notice = a.forScreen(err)
		}
	}

	dbg.Printf("play: back from the player, notice=%q", a.notice)

	if a.screen.GeometryChanged() {
		dbg.Fail("framebuffer geometry changed during playback")
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

	// Go errors start lowercase by convention, but these now often start with
	// a URL the user wrote. Capitalising that turns https into Https, which
	// looks like the app mangled their line.
	if first, _, _ := strings.Cut(msg, " "); strings.Contains(first, "://") {
		return msg
	}

	r := []rune(msg)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// move shifts the selection by delta, clamping at both ends.
func (a *app) move(delta int) {
	if len(a.entries) == 0 {
		return
	}
	a.notice = ""
	a.moveTo(a.selected + delta)
}

// moveTo selects index, clamping it, and scrolls the window so the selection
// stays visible.
func (a *app) moveTo(index int) {
	if len(a.entries) == 0 {
		return
	}
	a.selected = index
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
	title := "Antenna"
	switch a.view {
	case settingsView:
		title = "Settings"
	case searchView:
		title = "Search"
	}
	s.DrawText(sidePadding, 14, title, titleScale, colorText)

	switch a.view {
	case settingsView:
		a.renderSettings()
		return
	case searchView:
		a.renderSearch()
		return
	}

	// List, or the reason there is not one.
	maxTextWidth := width - sidePadding*2
	if len(a.entries) == 0 {
		label := fb.Truncate(a.emptyReason(), maxTextWidth, textScale)
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

	hint := "D-PAD MOVE   L/R JUMP   A PLAY   SELECT FIND   START SETTINGS   B EXIT"
	switch {
	case a.notice != "":
		hint = a.notice
	case a.banner != "":
		hint = a.banner
	}
	s.DrawText(sidePadding, footerY+13, fb.Truncate(hint, maxTextWidth, 1), 1, colorTextDim)

	if a.notice == "" && a.banner == "" && len(a.entries) > 0 {
		position := fmt.Sprintf("%d/%d", a.selected+1, len(a.entries))
		s.DrawText(width-sidePadding-fb.TextWidth(position, 1), footerY+13, position, 1, colorTextDim)
	}

	s.Present()
}

// renderSettings draws the settings screen: one row per setting, the value on
// the right, and the same footer shape as the list so the two do not feel like
// different apps.
func (a *app) renderSettings() {
	s := a.screen
	width := s.Width()
	maxTextWidth := width - sidePadding*2

	for i, row := range settingRows() {
		y := headerHeight + i*rowHeight
		textY := y + (rowHeight-fb.CellHeight*textScale)/2

		label := row.label
		colour := colorText
		if i == a.settingIndex {
			s.Rect(0, y, width, rowHeight, colorSelectedBg)
			colour = colorSelectedFg
		} else {
			s.Rect(sidePadding, y+rowHeight-1, width-sidePadding*2, 1, colorDivider)
		}
		s.DrawText(sidePadding, textY, fb.Truncate(label, maxTextWidth, textScale), textScale, colour)

		if row.value != nil {
			value := row.value(a)
			s.DrawText(width-sidePadding-fb.TextWidth(value, textScale), textY, value, textScale, colour)
		}
	}

	footerY := s.Height() - footerHeight
	s.Rect(0, footerY, width, footerHeight, colorHeader)
	s.Rect(0, footerY, width, 1, colorDivider)

	hint := "D-PAD MOVE   A CHANGE   B BACK"
	if a.notice != "" {
		hint = a.notice
	}
	s.DrawText(sidePadding, footerY+13, fb.Truncate(hint, maxTextWidth, 1), 1, colorTextDim)

	s.Present()
}

// renderSearch draws the query, how many titles still match, and the grid.
//
// The match count is the point of filtering as you type: it tells the user
// whether to keep typing without leaving the keyboard to look.
func (a *app) renderSearch() {
	s := a.screen
	width := s.Width()
	maxTextWidth := width - sidePadding*2

	// Query line.
	query := a.keys.Text()
	if query == "" {
		query = "Type to search"
	}
	s.DrawText(sidePadding, headerHeight+8, fb.Truncate(query, maxTextWidth, textScale), textScale, colorText)

	count := fmt.Sprintf("%d/%d", len(a.entries), len(a.allEntries))
	s.DrawText(width-sidePadding-fb.TextWidth(count, 1), headerHeight+12, count, 1, colorTextDim)
	s.Rect(sidePadding, headerHeight+8+fb.CellHeight*textScale+6, maxTextWidth, 1, colorDivider)

	// Grid.
	cursorRow, cursorCol := a.keys.Cursor()
	gridTop := headerHeight + 8 + fb.CellHeight*textScale + 18

	for row, line := range keyboard.Rows() {
		y := gridTop + row*keyHeight
		for col, key := range line {
			w := keyWidth
			if len(key.Label) > 1 {
				// A wide key, the space bar, spans what a run of keys would.
				w = keyWidth * 5
			}
			x := sidePadding + col*keyWidth

			colour := colorText
			if row == cursorRow && col == cursorCol {
				s.Rect(x, y, w-2, keyHeight-2, colorSelectedBg)
				colour = colorSelectedFg
			} else {
				s.Border(x, y, w-2, keyHeight-2, 1, colorDivider)
			}
			labelX := x + (w-2-fb.TextWidth(key.Label, textScale))/2
			labelY := y + (keyHeight-2-fb.CellHeight*textScale)/2
			s.DrawText(labelX, labelY, key.Label, textScale, colour)
		}
	}

	footerY := s.Height() - footerHeight
	s.Rect(0, footerY, width, footerHeight, colorHeader)
	s.Rect(0, footerY, width, 1, colorDivider)
	hint := "A TYPE   B DELETE   START DONE   SELECT CANCEL"
	// A search opened over a list that failed to load would otherwise show
	// 0/0 and no reason, inviting the user to type into nothing.
	if len(a.allEntries) == 0 && a.status != "" {
		hint = a.status
	}
	s.DrawText(sidePadding, footerY+13, fb.Truncate(hint, maxTextWidth, 1), 1, colorTextDim)

	s.Present()
}

// emptyReason explains an empty list. A search that matches nothing is the
// common case and looks identical to a failed load, so it has to say which it
// is: an empty screen with no message reads as the app being broken.
func (a *app) emptyReason() string {
	if query := a.query(); query != "" && len(a.allEntries) > 0 {
		return fmt.Sprintf("Nothing matches %q", query)
	}
	return a.status
}

// query is what the search box currently holds, empty when nothing is filtered.
func (a *app) query() string {
	if a.keys == nil {
		return ""
	}
	return a.keys.Text()
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
