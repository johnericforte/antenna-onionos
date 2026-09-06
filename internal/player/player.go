// Package player hands a resolved stream to the external video player and
// waits for it to finish.
//
// Antenna does not decode anything itself. OnionOS ships ffplay as an
// installable package, it is already tuned for this panel, and duplicating any
// part of it would mean carrying a decoder in a 6 MB app.
package player

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"antenna/internal/dbg"
)

// Path forces a particular ffplay binary. Empty means search knownPaths, which
// is what a release does. Tests set it to point somewhere predictable.
var Path string

// knownPaths is where ffplay has been documented to live on OnionOS. Onion's
// own docs describe renaming the binary inside .tmp_update/bin to switch
// aspect ratio, while the upstream port for this device ships it under a
// folder of its own. Which one is on a given card depends on the Onion version
// and how the package was installed, and guessing one wrong is indispensable
// from the app simply not working, so all of them are tried and the search is
// logged.
var knownPaths = []string{
	"/mnt/SDCARD/.tmp_update/bin/ffplay",
	"/mnt/SDCARD/Emu/ffplay/bin/ffplay",
	"/mnt/SDCARD/App/ffplay/bin/ffplay",
}

// binary returns the ffplay to run. The result is not cached: a card can be
// swapped and the search costs a handful of stat calls once per video.
func binary() string {
	if Path != "" {
		return Path
	}
	for _, candidate := range knownPaths {
		info, err := os.Stat(candidate)
		if err != nil {
			dbg.Printf("player: %s: %v", candidate, err)
			continue
		}
		if info.IsDir() {
			dbg.Printf("player: %s is a directory", candidate)
			continue
		}
		dbg.Printf("player: found %s, mode %v", candidate, info.Mode())
		return candidate
	}
	if found, err := exec.LookPath("ffplay"); err == nil {
		dbg.Printf("player: found %s on PATH", found)
		return found
	}
	// Nothing found. Return the first candidate so the failure names a real
	// path rather than an empty string.
	dbg.Printf("player: no ffplay in %v or on PATH", knownPaths)
	return knownPaths[0]
}

// stderrLimit caps how much of ffplay's complaint is kept. The device has
// 128 MB of RAM, and since the useful part of an ffmpeg error is its last
// line, what is kept is the tail rather than the head.
const stderrLimit = 4 << 10

// waitDelay bounds how long Run waits for the stderr pipe to close once
// ffplay itself has exited.
const waitDelay = 5 * time.Second

// Player runs one video at a time.
type Player struct {
	// run executes the player and blocks until it exits. It is a field so
	// tests can assert on the arguments without starting a process.
	run func(ctx context.Context, path string, args []string) error
}

// New returns a Player backed by the real ffplay binary.
func New() *Player {
	return &Player{run: runFFplay}
}

// Play blocks until playback ends, whether the video finished or the user
// quit. Errors are phrased for a person to read, since they end up on the
// screen, and the caller capitalizes the first letter for display.
func (p *Player) Play(ctx context.Context, url string) error {
	if url == "" {
		return errors.New("no video to play")
	}
	return p.run(ctx, binary(), Args(url))
}

// Args builds the ffplay command line.
//
// The panel is mounted rotated, so the picture is flipped on both axes. This
// is the same correction OnionOS applies to its own video player, and without
// it the video plays upside down and mirrored.
//
// autoexit returns to Antenna when the file ends rather than sitting on a
// frozen last frame, and the quiet log keeps ffmpeg's banner off a 640x480
// screen.
func Args(url string) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-autoexit",
		"-fs",
		"-vf", "hflip,vflip",
		"-i", url,
	}
}

func runFFplay(ctx context.Context, path string, args []string) error {
	dbg.Printf("player: exec %s %q", path, args)

	started := time.Now()
	cmd := exec.CommandContext(ctx, path, args...)

	stderr := &tailWriter{limit: stderrLimit}
	// Stdout goes to /dev/null rather than io.Discard: a non-file writer makes
	// os/exec build a pipe and a copying goroutine for it, and Run then blocks
	// until every holder of that pipe closes it. WaitDelay covers the same
	// hazard on stderr, so a descendant holding the descriptor cannot hang the
	// app on a black screen with no way out but the power button.
	cmd.Stdout = nil
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay

	err := cmd.Run()
	detail := stderr.String()

	// ffplay's own output is the best evidence there is about why a stream did
	// not play, so it goes to the trace whether or not the process failed. A
	// clean exit after two seconds means something very different from a clean
	// exit after eight minutes, so the duration goes with it.
	dbg.Printf("player: exit after %s: err=%v state=%v stderr=%q",
		time.Since(started).Round(time.Millisecond), err, cmd.ProcessState, detail)

	if err != nil {
		dbg.Fail("player: %s: %v: %s", path, err, lastLine(detail))
	}
	return playbackError(err, detail)
}

// fatalStderr are things ffplay prints when it gives up, while still exiting
// with status zero. On this device the whole failure is one line on stderr and
// a successful exit code, so the exit code alone cannot be trusted.
//
// "Protocol not found" is the one that matters: the ffplay OnionOS ships has
// no TLS, so an https URL dies here, silently, in under a second.
var fatalStderr = []string{
	"Protocol not found",
	"Invalid data found",
	"Server returned",
	"Connection refused",
	"No such file or directory",
	"Immediate exit requested",
	"could not find codec",
	"Decoder not found",
}

// playbackError turns an exec result into a line worth showing. It is separate
// from runFFplay so the classification can be tested without starting a
// process, which matters because these strings are the only diagnostics a
// handheld with no console ever shows.
func playbackError(err error, stderr string) error {
	if err == nil {
		// ffplay reports a dead stream by printing one line and exiting zero.
		// Trusting the exit code here is what made a failed video look like a
		// video the user simply watched.
		if detail := fatalLine(stderr); detail != "" {
			return fmt.Errorf("playback failed: %s", detail)
		}
		return nil
	}

	// A missing or non-executable binary is the likely failure on a fresh
	// card, and it has a fix the user can act on. Path is absolute, so exec
	// never calls LookPath and never returns exec.ErrNotFound; the real
	// error is a *fs.PathError wrapping ENOENT or EACCES.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return errors.New("install Video Player (FFplay) from Onion's Package Manager")
	}

	// Keep the cause attached. A benign decoder warning on stderr must not
	// erase the fact that the process was killed, which is what a caller
	// needs to tell a deliberate stop from a crash.
	if detail := lastLine(stderr); detail != "" {
		return fmt.Errorf("playback failed: %s: %w", detail, err)
	}
	return fmt.Errorf("playback failed: %w", err)
}

// fatalLine returns the line on which ffplay reported giving up, or empty when
// it said nothing fatal. It searches rather than taking the last line, since
// the interesting line is followed by driver chatter as often as not.
func fatalLine(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, marker := range fatalStderr {
			if strings.Contains(line, marker) {
				return line
			}
		}
	}
	return ""
}

// lastLine returns the final non-empty line, which is where ffmpeg puts the
// reason it gave up.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// tailWriter keeps the last limit bytes and discards what scrolls off the
// front. ffmpeg says why it gave up on its final line, so a head-limited
// buffer would fill with connection chatter and report a stale reason.
type tailWriter struct {
	buf   []byte
	limit int
}

// Write always reports the full length as written. Reporting a shorter count
// is a short write, which os/exec turns into an error and a broken stderr
// pipe, so capping the buffer would break playback rather than just logging.
func (t *tailWriter) Write(p []byte) (int, error) {
	total := len(p)
	if t.limit <= 0 {
		return total, nil
	}
	if len(p) >= t.limit {
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return total, nil
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return total, nil
}

func (t *tailWriter) String() string { return string(t.buf) }
