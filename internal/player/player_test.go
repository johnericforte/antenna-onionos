package player

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// testPlayer records what would have been executed instead of executing it, so
// nothing here starts a process.
type recorder struct {
	path string
	args []string
	ctx  context.Context
	err  error
}

func testPlayer(err error) (*Player, *recorder) {
	rec := &recorder{err: err}
	p := &Player{run: func(ctx context.Context, path string, args []string) error {
		rec.ctx, rec.path, rec.args = ctx, path, args
		return rec.err
	}}
	return p, rec
}

// TestPlayHandsFFplayTheWholeCommandLine asserts on what Play passes down
// rather than on Args in isolation. Testing Args alone lets Play drop the
// flip, the fullscreen flag or autoexit while the suite stays green, and all
// three of those are only visible on the device.
func TestPlayHandsFFplayTheWholeCommandLine(t *testing.T) {
	p, rec := testPlayer(nil)

	const url = "https://dn600209.us.archive.org/0/items/x/A%20Coy%20Decoy.mp4"
	if err := p.Play(context.Background(), url); err != nil {
		t.Fatalf("Play: %v", err)
	}

	if rec.path == "" {
		t.Error("no binary was chosen")
	}

	want := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-autoexit",
		"-fs",
		"-vf", "hflip,vflip",
		"-i", url,
	}
	if !slices.Equal(rec.args, want) {
		t.Errorf("arguments are\n  %q\nwant\n  %q", rec.args, want)
	}
}

// The panel is mounted rotated. Without both flips the video plays upside down
// and mirrored, which is obvious on a device and invisible in a unit test.
func TestPlayFlipsBothAxes(t *testing.T) {
	p, rec := testPlayer(nil)
	if err := p.Play(context.Background(), "https://example.invalid/v.mp4"); err != nil {
		t.Fatalf("Play: %v", err)
	}

	var filter string
	for i, a := range rec.args {
		if a == "-vf" && i+1 < len(rec.args) {
			filter = rec.args[i+1]
		}
	}
	if filter != "hflip,vflip" {
		t.Fatalf("filter is %q, want %q", filter, "hflip,vflip")
	}
}

func TestPlayExitsWhenTheVideoEnds(t *testing.T) {
	p, rec := testPlayer(nil)
	if err := p.Play(context.Background(), "https://example.invalid/v.mp4"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !slices.Contains(rec.args, "-autoexit") {
		t.Error("without -autoexit the app sits on a frozen frame after the video ends")
	}
}

// Playback has to be interruptible. Onion sends SIGTERM when the user backs
// out from the menu side, and the only way that reaches ffplay is the context.
func TestPlayForwardsTheCallersContext(t *testing.T) {
	p, rec := testPlayer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Play(ctx, "https://example.invalid/v.mp4"); err != nil {
		t.Fatalf("Play: %v", err)
	}

	if rec.ctx == nil || rec.ctx.Err() == nil {
		t.Error("the cancelled context did not reach the process")
	}
}

func TestPlayRejectsAnEmptyURL(t *testing.T) {
	p, rec := testPlayer(nil)

	if err := p.Play(context.Background(), ""); err == nil {
		t.Fatal("an empty URL should not reach ffplay")
	}
	if rec.args != nil {
		t.Errorf("ffplay was called anyway, with %q", rec.args)
	}
}

// playbackError is where every message a user can see about playback is
// decided, so each branch is pinned here.
func TestPlaybackError(t *testing.T) {
	missing := &fs.PathError{Op: "fork/exec", Path: Path, Err: syscall.ENOENT}
	denied := &fs.PathError{Op: "fork/exec", Path: Path, Err: syscall.EACCES}
	exited := &exec.ExitError{}

	tests := []struct {
		name   string
		err    error
		stderr string
		want   string
	}{
		{
			name: "success stays nil",
			err:  nil,
		},
		{
			// The absolute path means exec never returns exec.ErrNotFound, so
			// matching that sentinel silently disabled this message.
			name: "missing binary explains the fix",
			err:  missing,
			want: "install Video Player (FFplay) from Onion's Package Manager",
		},
		{
			name: "binary without the exec bit explains the same fix",
			err:  denied,
			want: "install Video Player (FFplay) from Onion's Package Manager",
		},
		{
			name:   "the last stderr line is the reason",
			err:    exited,
			stderr: "opening stream...\nServer returned 404 Not Found\n",
			want:   "playback failed: Server returned 404 Not Found",
		},
		{
			name: "no stderr falls back to the exec error",
			err:  exited,
			want: "playback failed: ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := playbackError(tc.err, tc.stderr)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("got nil, want an error")
			}
			if !strings.HasPrefix(got.Error(), tc.want) {
				t.Errorf("got %q, want it to start with %q", got, tc.want)
			}
		})
	}
}

// A benign decoder warning on stderr must not erase the reason the process
// stopped. Without the cause attached, a killed ffplay is indistinguishable
// from a corrupt macroblock.
func TestPlaybackErrorKeepsTheCause(t *testing.T) {
	cause := errors.New("signal: killed")

	got := playbackError(cause, "[h264] error while decoding MB 41 12\n")
	if !errors.Is(got, cause) {
		t.Fatalf("%v lost the cause %v", got, cause)
	}
	if !strings.Contains(got.Error(), "decoding MB") {
		t.Errorf("%v dropped the stderr detail", got)
	}
}

// The documented install paths are worth pinning: getting one wrong is
// indistinguishable from the app being broken, and the device is the only
// place that shows it.
func TestKnownPathsCoverTheDocumentedInstalls(t *testing.T) {
	want := map[string]bool{
		"/mnt/SDCARD/.tmp_update/bin/ffplay": false,
		"/mnt/SDCARD/Emu/ffplay/bin/ffplay":  false,
	}
	for _, p := range knownPaths {
		want[p] = true
	}
	for path, found := range want {
		if !found {
			t.Errorf("%s is no longer searched", path)
		}
	}
}

// An explicit Path wins over the search, which is how a test or a smoke run
// points at a binary of its own.
func TestPathOverridesTheSearch(t *testing.T) {
	restore := Path
	Path = "/somewhere/else/ffplay"
	t.Cleanup(func() { Path = restore })

	if got := binary(); got != "/somewhere/else/ffplay" {
		t.Errorf("binary() = %q, want the override", got)
	}
}

// End to end against a real missing binary, which is the case that shipped
// broken. Everything else in this file injects a fake.
func TestMissingFFplayTellsTheUserToInstallIt(t *testing.T) {
	restore := Path
	Path = filepath.Join(t.TempDir(), "ffplay")
	t.Cleanup(func() { Path = restore })

	err := New().Play(context.Background(), "https://example.invalid/v.mp4")
	if err == nil {
		t.Fatal("a missing binary should fail")
	}
	if !strings.Contains(err.Error(), "Package Manager") {
		t.Fatalf("got %q, want the install instruction", err)
	}
}

func TestLastLine(t *testing.T) {
	tests := map[string]string{
		"":                                  "",
		"   \n\n":                           "",
		"only line":                         "only line",
		"opening...\nServer returned 404\n": "Server returned 404",
		"trailing blank\n\n\n":              "trailing blank",
	}

	for in, want := range tests {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// The tail is what matters: ffmpeg says why it gave up on its final line, so
// keeping the head would report connection chatter instead of the reason.
func TestTailWriterKeepsTheEnd(t *testing.T) {
	w := &tailWriter{limit: 10}

	n, err := w.Write([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The caller must see a full write, or os/exec reports a short write and
	// breaks the stderr pipe.
	if n != 16 {
		t.Errorf("reported %d bytes written, want 16", n)
	}
	if got := w.String(); got != "6789abcdef" {
		t.Errorf("kept %q, want the last 10 bytes", got)
	}

	if _, err := w.Write([]byte("XY")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if got := w.String(); got != "89abcdefXY" {
		t.Errorf("kept %q after a second write", got)
	}
}

func TestTailWriterAcrossManySmallWrites(t *testing.T) {
	w := &tailWriter{limit: 8}
	for _, chunk := range []string{"aaa", "bbb", "ccc", "ddd"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q): %v", chunk, err)
		}
	}
	if got := w.String(); got != "bcccddd"[len("bcccddd")-7:] && got != "bbcccddd" {
		t.Errorf("kept %q, want the last 8 bytes", got)
	}
	if len(w.String()) != 8 {
		t.Errorf("kept %d bytes, want 8", len(w.String()))
	}
}

// The failure that shipped: OnionOS ffplay has no TLS, so an https URL makes
// it print one line and exit with status zero. Trusting the exit code meant a
// dead video looked like a watched one, with nothing on screen and nothing in
// the log.
func TestExitZeroWithAFatalLineIsAFailure(t *testing.T) {
	const stderr = "vtpath = /dev/vc/2, fd = -1\n" +
		"Current VT: 2\n" +
		"1788734734336.000000 FB_VideoInit shadow_fb is 0\n" +
		"https://dn600209.us.archive.org/0/items/x/BugsBny.mp4: Protocol not found\n"

	err := playbackError(nil, stderr)
	if err == nil {
		t.Fatal("a fatal stderr line with exit status 0 was reported as success")
	}
	if !strings.Contains(err.Error(), "Protocol not found") {
		t.Errorf("got %q, want the reason ffplay gave", err)
	}
}

// Driver chatter on a healthy run must not be mistaken for a failure.
func TestExitZeroWithOnlyChatterIsSuccess(t *testing.T) {
	const stderr = "vtpath = /dev/vc/2, fd = -1\n" +
		"vtpath = /dev/tty2, fd = 6\n" +
		"Current VT: 2\n" +
		"1788734734336.000000 FB_VideoInit shadow_fb is 0\n"

	if err := playbackError(nil, stderr); err != nil {
		t.Errorf("a clean run was reported as %v", err)
	}
}

func TestFatalLineFindsTheReasonAnywhere(t *testing.T) {
	// The reason is not always last: driver noise follows it as often as not.
	const stderr = "x.mp4: Protocol not found\nCurrent VT: 2\nFB_VideoInit shadow_fb is 0\n"

	if got := fatalLine(stderr); !strings.Contains(got, "Protocol not found") {
		t.Errorf("fatalLine() = %q", got)
	}
	if got := fatalLine("all quiet\n"); got != "" {
		t.Errorf("fatalLine() = %q, want empty", got)
	}
}
