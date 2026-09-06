package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"antenna/internal/config"
	"antenna/internal/dbg"

	"antenna/internal/fb"
	"antenna/internal/input"
	"antenna/internal/provider"
)

// fakeScreen records nothing but the text drawn, which is enough to assert
// what the user would be looking at.
type fakeScreen struct {
	lines   []string
	changed bool
}

func (f *fakeScreen) Width() int                           { return 640 }
func (f *fakeScreen) Height() int                          { return 480 }
func (f *fakeScreen) Clear(fb.Color)                       {}
func (f *fakeScreen) Rect(_, _, _, _ int, _ fb.Color)      {}
func (f *fakeScreen) Border(_, _, _, _, _ int, _ fb.Color) {}
func (f *fakeScreen) DrawText(_, _ int, s string, _ int, _ fb.Color) {
	f.lines = append(f.lines, s)
}
func (f *fakeScreen) Present()              {}
func (f *fakeScreen) GeometryChanged() bool { return f.changed }

type fakePlayer struct {
	calls int
	url   string
	err   error
	block chan struct{}
}

func (f *fakePlayer) Play(ctx context.Context, url string) error {
	f.calls++
	f.url = url
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

type fakeButtons struct{ drained int }

func (f *fakeButtons) Drain(time.Duration) { f.drained++ }

// fakeSource serves one item with one title, or the errors it is given.
type fakeSource struct {
	entries    []provider.Entry
	stream     *provider.Stream
	browseErr  error
	rootErr    error
	resolveErr error
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) Browse(_ context.Context, path string) ([]provider.Entry, error) {
	if path == "" {
		if f.rootErr != nil {
			return nil, f.rootErr
		}
		return []provider.Entry{{ID: "item", Title: "Item", IsFolder: true}}, nil
	}
	if f.browseErr != nil {
		return nil, f.browseErr
	}
	return f.entries, nil
}

func (f *fakeSource) Resolve(context.Context, string) (*provider.Stream, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.stream, nil
}

func newTestApp(source provider.Provider) (*app, *fakeScreen, *fakePlayer, *fakeButtons) {
	screen := &fakeScreen{}
	video := &fakePlayer{}
	buttons := &fakeButtons{}
	a := &app{
		screen:  screen,
		source:  source,
		video:   video,
		buttons: buttons,
		quit:    context.Background(),
		rows:    12,
	}
	return a, screen, video, buttons
}

func playableStream() *provider.Stream {
	return &provider.Stream{
		Kind:    provider.Progressive,
		Codec:   provider.H264,
		URL:     "https://example.invalid/v.mp4",
		Height:  480,
		Bitrate: 830_000,
	}
}

// The player must never be handed the https URL. The ffplay OnionOS ships has
// no TLS: given https it prints "Protocol not found" and exits with status
// zero, so the video silently does not play. Everything goes through the
// loopback relay instead.
func TestPlayHandsThePlayerALoopbackURL(t *testing.T) {
	source := &fakeSource{
		entries: []provider.Entry{{ID: "item/a.mp4", Title: "A Coy Decoy"}},
		stream:  playableStream(),
	}
	a, _, video, _ := newTestApp(source)
	a.load(context.Background())

	a.play()

	if video.calls != 1 {
		t.Fatalf("player called %d times, want 1", video.calls)
	}
	if strings.HasPrefix(video.url, "https://") {
		t.Errorf("played %q, which this ffplay cannot open", video.url)
	}
	if !strings.HasPrefix(video.url, "http://127.0.0.1:") {
		t.Errorf("played %q, want a loopback address", video.url)
	}
	if a.notice != "" {
		t.Errorf("a clean playback left %q on screen", a.notice)
	}
}

// Presses made during a video must not replay into the list. Nothing takes the
// buttons on the paths that fail before playback starts, so draining there
// would only eat a press the user made while reading the footer, and stall the
// message by the quiet window on its way out.
func TestPlayDrainsOnlyAfterFFplayHadTheButtons(t *testing.T) {
	tests := []struct {
		name   string
		source *fakeSource
		want   int
	}{
		{
			name: "after a video, the queue is dropped",
			want: 1,
			source: &fakeSource{
				entries: []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
				stream:  playableStream(),
			},
		},
		{
			name: "a failed resolve keeps the user's presses",
			want: 0,
			source: &fakeSource{
				entries:    []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
				resolveErr: errors.New("archive.org returned 404 Not Found for item"),
			},
		},
		{
			name: "an undecodable stream keeps them too",
			want: 0,
			source: &fakeSource{
				entries: []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
				stream:  &provider.Stream{Kind: provider.Progressive, Codec: provider.H264, Height: 1080, Bitrate: 900_000},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _, buttons := newTestApp(tc.source)
			a.load(context.Background())

			a.play()

			if buttons.drained != tc.want {
				t.Fatalf("drained %d times, want %d", buttons.drained, tc.want)
			}
		})
	}
}

func TestPlayShowsWhyAStreamWasRefused(t *testing.T) {
	source := &fakeSource{
		entries: []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
		stream:  &provider.Stream{Kind: provider.Progressive, Codec: provider.H264, Height: 1080, Bitrate: 900_000},
	}
	a, _, video, _ := newTestApp(source)
	a.load(context.Background())

	a.play()

	if video.calls != 0 {
		t.Error("an undecodable stream reached the player")
	}
	if a.notice == "" {
		t.Fatal("nothing told the user why")
	}
	if !strings.Contains(a.notice, "480p") {
		t.Errorf("notice is %q, want the device limit", a.notice)
	}
}

// A silent no-op is indistinguishable from a broken button on a device with
// no other feedback.
func TestPlayOnAnEmptyListSaysSomething(t *testing.T) {
	a, _, video, _ := newTestApp(&fakeSource{})

	a.play()

	if video.calls != 0 {
		t.Error("the player ran with nothing selected")
	}
	if a.notice == "" {
		t.Error("pressing A did nothing at all, visibly or otherwise")
	}
}

func TestPlaySurvivesAFailedResolve(t *testing.T) {
	source := &fakeSource{
		entries:    []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
		resolveErr: errors.New("a.mp4 is no longer in item"),
	}
	a, _, _, _ := newTestApp(source)
	a.load(context.Background())

	a.play()

	if a.notice == "" {
		t.Fatal("the failure was swallowed")
	}
	if len(a.entries) != 1 {
		t.Error("the list did not survive the failure")
	}
}

// Onion asking the app to quit kills ffplay. That is the intended outcome and
// must not be reported as a playback failure.
func TestPlayDoesNotReportAShutdownAsAFailure(t *testing.T) {
	source := &fakeSource{
		entries: []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
		stream:  playableStream(),
	}
	a, _, video, _ := newTestApp(source)
	a.load(context.Background())

	quit, stop := context.WithCancel(context.Background())
	a.quit = quit
	video.block = make(chan struct{})

	done := make(chan struct{})
	go func() {
		a.play()
		close(done)
	}()

	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("play did not return when the app was told to quit")
	}

	if a.notice != "" {
		t.Errorf("shutdown reported as %q", a.notice)
	}
}

// If ffplay hands back a panel in a different mode, every later draw lands
// somewhere the screen does not read. Silence there looks like a dead device.
func TestPlayReportsALostFramebuffer(t *testing.T) {
	source := &fakeSource{
		entries: []provider.Entry{{ID: "item/a.mp4", Title: "A"}},
		stream:  playableStream(),
	}
	a, screen, _, _ := newTestApp(source)
	a.load(context.Background())
	screen.changed = true

	a.play()

	if a.notice == "" {
		t.Fatal("a changed framebuffer was not reported")
	}
}

// A dead identifier must not be reported as a device that cannot decode
// anything. They send whoever is debugging in opposite directions.
func TestLoadShowsTheRealFailure(t *testing.T) {
	source := &fakeSource{browseErr: errors.New("archive.org has no files for pdcartooncollection")}
	a, _, _, _ := newTestApp(source)

	a.load(context.Background())

	if strings.Contains(a.status, "Nothing here plays") {
		t.Fatalf("status is %q, which blames the hardware", a.status)
	}
	if !strings.Contains(a.status, "no files") {
		t.Errorf("status is %q, want the reason", a.status)
	}
}

func TestLoadSaysWhenTheListIsPartial(t *testing.T) {
	// One root succeeds, so entries exist, but the failure must still show.
	source := &partialSource{}
	a, _, _, _ := newTestApp(source)

	a.load(context.Background())

	if len(a.entries) == 0 {
		t.Fatal("expected a partial list")
	}
	if a.banner == "" {
		t.Fatal("a partial list looked complete")
	}
	if !strings.Contains(a.banner, "did not load") {
		t.Errorf("banner is %q", a.banner)
	}
}

// partialSource has two items where the second one always fails.
type partialSource struct{}

func (p *partialSource) Name() string { return "partial" }

func (p *partialSource) Browse(_ context.Context, path string) ([]provider.Entry, error) {
	switch path {
	case "":
		return []provider.Entry{
			{ID: "good", Title: "Good", IsFolder: true},
			{ID: "bad", Title: "Bad", IsFolder: true},
		}, nil
	case "good":
		return []provider.Entry{{ID: "good/a.mp4", Title: "A"}}, nil
	default:
		return nil, errors.New("archive.org returned 404 Not Found for bad")
	}
}

func (p *partialSource) Resolve(context.Context, string) (*provider.Stream, error) {
	return playableStream(), nil
}

func TestForScreen(t *testing.T) {
	a, _, _, _ := newTestApp(&fakeSource{})

	long := "cannot reach archive.org: Get \"https://archive.org/metadata/classic_cartoons_201603\": dial tcp: lookup archive.org: no such host"

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "empty message still says something",
			err:  errors.New(""),
			want: "Something went wrong",
		},
		{
			name: "lowercase Go error is capitalized",
			err:  errors.New("playback failed: 404"),
			want: "Playback failed: 404",
		},
		{
			name: "an already capitalized reason is left alone",
			err:  errors.New("HLS streams are not supported"),
			want: "HLS streams are not supported",
		},
		{
			name: "a long chain keeps the cause",
			err:  errors.New(long),
			want: "Cannot reach archive.org: no such host",
		},
		{
			// A multi-byte first rune must survive the capitalization.
			name: "multi-byte text is not mangled",
			err:  errors.New("ñandú failed"),
			want: "Ñandú failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.forScreen(tc.err); got != tc.want {
				t.Errorf("forScreen() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMoveClearsTheNotice(t *testing.T) {
	source := &fakeSource{entries: []provider.Entry{
		{ID: "item/a.mp4", Title: "A"},
		{ID: "item/b.mp4", Title: "B"},
	}}
	a, _, _, _ := newTestApp(source)
	a.load(context.Background())
	a.notice = "Playback failed: 404"

	a.move(1)

	if a.notice != "" {
		t.Errorf("notice survived a d-pad press: %q", a.notice)
	}
}

func TestRenderShowsTheNoticeInsteadOfTheHint(t *testing.T) {
	source := &fakeSource{entries: []provider.Entry{{ID: "item/a.mp4", Title: "A"}}}
	a, screen, _, _ := newTestApp(source)
	a.load(context.Background())
	a.notice = "Playback failed"

	a.render()

	var sawNotice, sawHint bool
	for _, line := range screen.lines {
		if strings.Contains(line, "Playback failed") {
			sawNotice = true
		}
		if strings.Contains(line, "A PLAY") {
			sawHint = true
		}
	}
	if !sawNotice {
		t.Error("the notice was never drawn")
	}
	if sawHint {
		t.Error("the hint covered the notice")
	}
}

// The footer is the only place the controls are stated, so it must not name a
// button that does nothing. Paging was removed in favour of holding the d-pad;
// L and R now jump by letter, which the assertions below prove rather than
// assume.
func TestFooterHintNamesOnlyWorkingButtons(t *testing.T) {
	source := &fakeSource{entries: []provider.Entry{
		{ID: "item/a.mp4", Title: "Alpha"},
		{ID: "item/b.mp4", Title: "Beta"},
	}}
	a, screen, _, _ := newTestApp(source)
	a.load(context.Background())

	a.render()

	var hint string
	for _, line := range screen.lines {
		if strings.Contains(line, "D-PAD") {
			hint = line
		}
	}
	if hint == "" {
		t.Fatal("the controls hint was never drawn")
	}
	if strings.Contains(hint, "PAGE") {
		t.Errorf("hint %q still offers paging", hint)
	}

	// Every button the hint names has to move something.
	if strings.Contains(hint, "L/R JUMP") {
		a.handle(input.R1)
		if a.selected == 0 {
			t.Error("the hint offers L/R JUMP but R did not move the selection")
		}
	}
	if strings.Contains(hint, "START SETTINGS") {
		a.handle(input.Start)
		if a.view != settingsView {
			t.Error("the hint offers START SETTINGS but START did not open them")
		}
	}
	for _, want := range []string{"A PLAY", "B EXIT"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q is missing %q", hint, want)
		}
	}
}

func listOf(titles ...string) *fakeSource {
	entries := make([]provider.Entry, 0, len(titles))
	for i, title := range titles {
		entries = append(entries, provider.Entry{ID: fmt.Sprintf("item/%d.mp4", i), Title: title})
	}
	return &fakeSource{entries: entries, stream: playableStream()}
}

// Holding the d-pad across a long list is the problem this solves, so a jump
// has to land on the next letter rather than the next row.
func TestJumpLetterMovesToTheNextLetter(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha", "Anvil", "Apple", "Bravo", "Cello"))
	a.load(context.Background())

	a.jumpLetter(1)
	if got := a.entries[a.selected].Title; got != "Bravo" {
		t.Fatalf("forward jump landed on %q, want Bravo", got)
	}

	a.jumpLetter(1)
	if got := a.entries[a.selected].Title; got != "Cello" {
		t.Fatalf("second jump landed on %q, want Cello", got)
	}

	a.jumpLetter(-1)
	if got := a.entries[a.selected].Title; got != "Bravo" {
		t.Fatalf("back jump landed on %q, want Bravo", got)
	}
}

// Jumping past the last letter goes to the end rather than doing nothing,
// which is what makes the button useful for reaching the bottom of a list.
func TestJumpLetterStopsAtTheEnds(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha", "Bravo", "Cello"))
	a.load(context.Background())

	a.moveTo(2)
	a.jumpLetter(1)
	if a.selected != 2 {
		t.Errorf("forward jump from the last entry moved to %d", a.selected)
	}

	a.moveTo(0)
	a.jumpLetter(-1)
	if a.selected != 0 {
		t.Errorf("back jump from the first entry moved to %d", a.selected)
	}
}

// Case and leading punctuation would otherwise split one letter into several
// groups, so a jump would stop on titles that look identical to the user.
func TestFirstLetterIgnoresCaseAndPunctuation(t *testing.T) {
	tests := map[string]rune{
		"Alpha":     'a',
		"alpha":     'a',
		"\"Alpha\"": 'a',
		"  Alpha":   'a',
		"1941":      '1',
		"...":       0,
		"":          0,
	}
	for title, want := range tests {
		if got := firstLetter(title); got != want {
			t.Errorf("firstLetter(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestJumpLetterOnAnEmptyListDoesNothing(t *testing.T) {
	a, _, _, _ := newTestApp(&fakeSource{})
	a.jumpLetter(1)
	if a.selected != 0 {
		t.Errorf("selection moved to %d on an empty list", a.selected)
	}
}

func TestStartOpensSettingsAndBackReturns(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha"))
	a.load(context.Background())

	if got := a.handle(input.Start); got != stayOpen {
		t.Fatal("opening settings should not exit the app")
	}
	if a.view != settingsView {
		t.Fatal("START did not open the settings")
	}

	if got := a.handle(input.B); got != stayOpen {
		t.Fatal("B on the settings screen exited the app instead of going back")
	}
	if a.view != listView {
		t.Error("B did not return to the list")
	}
}

// B means "back" in settings and "quit" in the list. Losing the whole app to a
// mispress while changing a setting is a bad trade.
func TestBExitsFromTheListOnly(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha"))
	a.load(context.Background())

	if got := a.handle(input.B); got != exitApp {
		t.Error("B in the list should exit")
	}
}

func TestSettingsTogglePersistsAndAppliesImmediately(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha"))
	a.settingsPath = filepath.Join(t.TempDir(), config.SettingsFileName)
	before := dbg.On()
	t.Cleanup(func() { dbg.SetEnabled(before) })

	a.view = settingsView
	a.settingIndex = 0
	a.handle(input.A)

	if !a.settings.Logging {
		t.Fatal("the toggle did not change the setting")
	}
	if !dbg.On() {
		t.Error("the trace was not turned on for the running app")
	}

	saved, _, err := config.LoadSettings(a.settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !saved.Logging {
		t.Error("the choice was not written down, so it dies at reboot")
	}
}

func TestSettingsScreenDrawsEveryRowAndItsValue(t *testing.T) {
	a, screen, _, _ := newTestApp(listOf("Alpha"))
	a.view = settingsView

	a.render()

	joined := strings.Join(screen.lines, "|")
	for _, row := range settingRows() {
		if !strings.Contains(joined, row.label) {
			t.Errorf("settings screen never drew %q", row.label)
		}
	}
	if !strings.Contains(joined, "Off") {
		t.Error("the logging row drew no value")
	}
	if !strings.Contains(joined, "B BACK") {
		t.Error("the settings footer does not say how to leave")
	}
}

// Wi-Fi that was not up at startup is the common case. Without a reload the
// only cure is quitting to the Onion menu and starting again.
func TestReloadBrowsesAgainAndReturnsToTheList(t *testing.T) {
	source := &fakeSource{browseErr: errors.New("cannot reach archive.org")}
	a, _, _, _ := newTestApp(source)
	a.load(context.Background())
	if len(a.entries) != 0 {
		t.Fatal("expected the first load to fail")
	}

	// The network comes back.
	source.browseErr = nil
	source.entries = []provider.Entry{{ID: "item/a.mp4", Title: "Alpha"}}

	a.view = settingsView
	a.settingIndex = 1
	a.handle(input.A)

	if a.view != listView {
		t.Error("reload left the user on the settings screen")
	}
	if len(a.entries) != 1 {
		t.Fatalf("reload found %d entries, want 1", len(a.entries))
	}
}

// typeSearch types a query and re-filters exactly as a keypress does, without
// walking the cursor across the grid. Cursor movement is the keyboard's own
// concern and has its own tests.
func typeSearch(t *testing.T, a *app, text string) {
	t.Helper()
	for _, r := range text {
		if !a.keys.Type(r) {
			t.Fatalf("%q is not on the keyboard", r)
		}
		a.applyFilter()
	}
}

func TestSearchFiltersTheListAsYouType(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye", "Bunny Tales", "Daffy"))
	a.load(context.Background())

	a.handle(input.Select)
	if a.view != searchView {
		t.Fatal("SELECT did not open the search")
	}

	typeSearch(t, a, "bunny")

	if len(a.entries) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(a.entries), a.entries)
	}
	for _, e := range a.entries {
		if !strings.Contains(strings.ToLower(e.Title), "bunny") {
			t.Errorf("%q does not match the query", e.Title)
		}
	}
	// The full list is kept so the filter can be undone without refetching.
	if len(a.allEntries) != 4 {
		t.Errorf("the unfiltered list holds %d entries, want 4", len(a.allEntries))
	}
}

func TestBackspaceWidensTheResults(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye", "Bunny Tales"))
	a.load(context.Background())
	a.handle(input.Select)

	typeSearch(t, a, "bugs")
	if len(a.entries) != 1 {
		t.Fatalf("got %d matches for bugs, want 1", len(a.entries))
	}

	for range "bugs" {
		a.handle(input.B)
	}
	if len(a.entries) != 3 {
		t.Fatalf("deleting the query left %d entries, want the whole list back", len(a.entries))
	}
}

// START keeps the filter, which is what makes searching worth doing: you come
// back to a short list.
func TestStartConfirmsTheSearch(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye"))
	a.load(context.Background())
	a.handle(input.Select)
	typeSearch(t, a, "popeye")

	a.handle(input.Start)

	if a.view != listView {
		t.Fatal("START did not return to the list")
	}
	if len(a.entries) != 1 || a.entries[0].Title != "Popeye" {
		t.Errorf("the filter was lost on confirm: %+v", a.entries)
	}
}

// SELECT cancels, which has to restore the whole list. Leaving a filter behind
// after a cancel looks exactly like titles having gone missing.
func TestSelectCancelsAndRestoresTheList(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye"))
	a.load(context.Background())
	a.handle(input.Select)
	typeSearch(t, a, "popeye")

	a.handle(input.Select)

	if a.view != listView {
		t.Fatal("SELECT did not return to the list")
	}
	if len(a.entries) != 2 {
		t.Errorf("cancel left %d entries, want the full list", len(a.entries))
	}
	if a.keys.Text() != "" {
		t.Errorf("cancel left %q in the query", a.keys.Text())
	}
}

// Reopening with the previous query saves retyping it on a d-pad.
func TestSearchRemembersTheLastQuery(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye"))
	a.load(context.Background())
	a.handle(input.Select)
	typeSearch(t, a, "bugs")
	a.handle(input.Start)

	a.handle(input.Select)
	if a.keys.Text() != "bugs" {
		t.Errorf("reopening the search shows %q, want the previous query", a.keys.Text())
	}
}

// A search that filtered everything out must not leave a stale selection
// pointing past the end of the list.
func TestSearchWithNoMatchesIsSafe(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye"))
	a.load(context.Background())
	a.moveTo(1)
	a.handle(input.Select)

	typeSearch(t, a, "zzz")

	if len(a.entries) != 0 {
		t.Fatalf("got %d matches for zzz", len(a.entries))
	}
	if a.selected != 0 {
		t.Errorf("selection is %d with an empty list", a.selected)
	}
	// Nothing selected means nothing to play, and that must not panic.
	a.handle(input.Start)
	a.play()
}

// A reload while a search is active keeps the search applied, rather than
// silently showing the full list under a query the user can still see.
func TestReloadKeepsAnActiveSearch(t *testing.T) {
	source := listOf("Bugs Bunny", "Popeye")
	a, _, _, _ := newTestApp(source)
	a.load(context.Background())
	a.handle(input.Select)
	typeSearch(t, a, "bugs")
	a.handle(input.Start)

	a.view = settingsView
	a.settingIndex = 1
	a.handle(input.A)

	if len(a.entries) != 1 {
		t.Errorf("after reload the list holds %d entries, want the search still applied", len(a.entries))
	}
}

func TestSearchScreenDrawsTheGridAndTheQuery(t *testing.T) {
	a, screen, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye"))
	a.load(context.Background())
	a.handle(input.Select)
	typeSearch(t, a, "bu")

	a.render()

	joined := strings.Join(screen.lines, "|")
	if !strings.Contains(joined, "bu") {
		t.Error("the query was never drawn")
	}
	if !strings.Contains(joined, "SPACE") {
		t.Error("the space key was never drawn")
	}
	// The match count is what tells the user whether to keep typing.
	if !strings.Contains(joined, "1/2") {
		t.Errorf("the match count was not drawn: %q", joined)
	}
	if !strings.Contains(joined, "SELECT CANCEL") {
		t.Error("the search footer does not say how to leave")
	}
}

// The row has to report what is actually happening. With a debug file on the
// card and logging=false saved, the two disagree, and a row showing only the
// saved value said Off while the log filled up.
func TestLoggingRowReportsTheLiveState(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha"))
	before := dbg.On()
	t.Cleanup(func() { dbg.SetEnabled(before) })

	dbg.SetEnabled(true)
	a.settings.Logging = false

	row := settingRows()[0]
	if got := row.value(a); got != "On" {
		t.Errorf("row shows %q while tracing is on", got)
	}

	// And one press turns it off, rather than agreeing with the stale value.
	a.view = settingsView
	a.settingIndex = 0
	a.handle(input.A)
	if dbg.On() {
		t.Error("one press did not turn tracing off")
	}
}

// A read only card is routine after an unclean shutdown. Leaving the change
// applied would show a state that quietly reverts at the next launch.
func TestFailedSaveRollsBackTheToggle(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha"))
	before := dbg.On()
	t.Cleanup(func() { dbg.SetEnabled(before) })
	dbg.SetEnabled(false)

	// A path inside a file, so creating the temporary file cannot work.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	a.settingsPath = filepath.Join(blocked, config.SettingsFileName)

	a.view = settingsView
	a.settingIndex = 0
	a.handle(input.A)

	if a.settings.Logging || dbg.On() {
		t.Error("the change stayed applied after the save failed")
	}
	if a.notice == "" {
		t.Error("the failed save was not reported")
	}
}

// An empty screen with no message reads as the app being broken, and a search
// matching nothing looks identical to a failed load.
func TestEmptyListSaysWhetherItIsAFilterOrAFailure(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Bugs Bunny", "Popeye"))
	a.load(context.Background())
	a.handle(input.Select)
	typeSearch(t, a, "zzz")
	a.handle(input.Start)

	if got := a.emptyReason(); !strings.Contains(got, "zzz") {
		t.Errorf("empty list says %q, which does not mention the search", got)
	}

	// A genuine failure still reports itself.
	failing := &fakeSource{browseErr: errors.New("archive.org has no files for x")}
	b, _, _, _ := newTestApp(failing)
	b.load(context.Background())
	if got := b.emptyReason(); !strings.Contains(got, "no files") {
		t.Errorf("a failed load says %q", got)
	}
}

// Reloading after a rejected items.txt used to replace the message naming the
// bad line with a claim about the hardware.
func TestReloadRereadsTheItemList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte("some/item\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	a, _, _, _ := newTestApp(listOf("Alpha"))
	a.itemsPath = path

	a.view = settingsView
	a.settingIndex = 1
	a.handle(input.A)

	if !strings.Contains(a.status, "line 1") {
		t.Errorf("status is %q, want the rejected line named", a.status)
	}
	if strings.Contains(a.status, "Nothing here plays") {
		t.Error("a bad item list was reported as a device limitation")
	}
}

// Errors now often start with a URL the user wrote. Capitalising that turns
// https into Https, which looks like the app mangled their line.
func TestForScreenLeavesAURLAlone(t *testing.T) {
	a, _, _, _ := newTestApp(&fakeSource{})

	got := a.forScreen(errors.New("https://example.org/list.m3u lists no video URLs"))
	if !strings.HasPrefix(got, "https://") {
		t.Errorf("forScreen returned %q, want the URL untouched", got)
	}

	// A normal Go error is still capitalised for the screen.
	if got := a.forScreen(errors.New("playback failed")); got != "Playback failed" {
		t.Errorf("forScreen returned %q", got)
	}
}

// Searching a list that failed to load would otherwise show 0/0 and no reason,
// inviting the user to type into nothing.
func TestSearchShowsWhyTheListIsEmpty(t *testing.T) {
	source := &fakeSource{browseErr: errors.New("cannot reach archive.org")}
	a, screen, _, _ := newTestApp(source)
	a.load(context.Background())
	a.handle(input.Select)

	a.render()

	joined := strings.Join(screen.lines, "|")
	if !strings.Contains(joined, "archive.org") {
		t.Errorf("the search screen never said why there is nothing to search: %q", joined)
	}
}

// Holding A on a toggle would flip it and rewrite the card once per repeat,
// landing on whichever state the release happened to hit.
func TestHeldButtonDoesNotToggleRepeatedly(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("Alpha"))
	a.settingsPath = filepath.Join(t.TempDir(), config.SettingsFileName)
	before := dbg.On()
	t.Cleanup(func() { dbg.SetEnabled(before) })
	dbg.SetEnabled(false)

	a.view = settingsView
	a.settingIndex = 0

	a.handle2(input.Event{Button: input.A, Pressed: true})
	first := dbg.On()
	for i := 0; i < 5; i++ {
		a.handle2(input.Event{Button: input.A, Pressed: true, Repeat: true})
	}

	if dbg.On() != first {
		t.Error("repeats toggled the setting again")
	}
}

// Holding a direction still has to scroll, which is what replaced paging.
func TestHeldDirectionStillScrolls(t *testing.T) {
	a, _, _, _ := newTestApp(listOf("A", "B", "C", "D"))
	a.load(context.Background())

	for i := 0; i < 3; i++ {
		a.handle2(input.Event{Button: input.Down, Pressed: true, Repeat: i > 0})
	}
	if a.selected != 3 {
		t.Errorf("selection is %d after holding down, want 3", a.selected)
	}
}

// The partial load message is the only trace that something is missing, and
// clearing it on the first press meant a user who pressed Down never saw it.
func TestPartialLoadMessageSurvivesNavigation(t *testing.T) {
	a, screen, _, _ := newTestApp(&partialSource{})
	a.load(context.Background())

	if a.banner == "" {
		t.Fatal("a partial load said nothing")
	}
	a.handle(input.Down)
	a.render()

	joined := strings.Join(screen.lines, "|")
	if !strings.Contains(joined, "did not load") {
		t.Errorf("the partial load message did not survive a d-pad press: %q", joined)
	}
}
