package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"antenna/internal/fb"
	"antenna/internal/provider"
)

// fakeScreen records nothing but the text drawn, which is enough to assert
// what the user would be looking at.
type fakeScreen struct {
	lines   []string
	changed bool
}

func (f *fakeScreen) Width() int                      { return 640 }
func (f *fakeScreen) Height() int                     { return 480 }
func (f *fakeScreen) Clear(fb.Color)                  {}
func (f *fakeScreen) Rect(_, _, _, _ int, _ fb.Color) {}
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

func TestPlayHandsTheResolvedURLToThePlayer(t *testing.T) {
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
	if video.url != "https://example.invalid/v.mp4" {
		t.Errorf("played %q", video.url)
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
	if a.notice == "" {
		t.Fatal("a partial list looked complete")
	}
	if !strings.Contains(a.notice, "did not load") {
		t.Errorf("notice is %q", a.notice)
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
