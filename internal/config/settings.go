package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SettingsFileName sits beside the item list, in the app folder on the card.
const SettingsFileName = "settings.txt"

// Settings is what the user can change from inside the app. It is deliberately
// small: a settings screen on a handheld is a list you scroll with a d-pad,
// and anything that does not earn its row belongs in a text file instead.
type Settings struct {
	// Logging turns the trace log on. It is the same switch as the debug file
	// launch.sh looks for, exposed in the app so turning it on does not mean
	// pulling the card, editing it on a computer and putting it back.
	Logging bool
}

// LoadSettings reads path. A missing file means nothing has been changed yet,
// which is not an error and returns the defaults.
//
// A file that cannot be parsed is also not an error. Settings are conveniences,
// and refusing to start over a stray line in one would be a worse outcome than
// starting with the defaults. The unreadable lines are reported to the caller
// so they can reach the log.
func LoadSettings(path string) (Settings, []string, error) {
	var settings Settings

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return settings, nil, nil
		}
		return settings, nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var problems []string
	scanner := bufio.NewScanner(file)

	for line := 1; scanner.Scan(); line++ {
		key, value, ok := parseSetting(scanner.Text())
		if !ok {
			continue
		}
		switch key {
		case "logging":
			on, err := strconv.ParseBool(value)
			if err != nil {
				problems = append(problems, fmt.Sprintf("line %d: logging is %q, want true or false", line, value))
				continue
			}
			settings.Logging = on
		default:
			problems = append(problems, fmt.Sprintf("line %d: unknown setting %q", line, key))
		}
	}
	if err := scanner.Err(); err != nil {
		// The file was not read to the end, so what was parsed is a fragment
		// rather than the user's settings. Returning the defaults keeps a
		// half read file from being treated as authoritative.
		return Settings{}, problems, fmt.Errorf("read %s: %w", path, err)
	}
	return settings, problems, nil
}

// SaveSettings writes path, replacing whatever was there.
//
// The file is written whole and then moved into place, because the card is
// removable and a half written settings file is the kind of thing that turns
// a working install into a puzzle.
func SaveSettings(path string, settings Settings) error {
	contents := fmt.Sprintf(`# Antenna settings. Written by the app, safe to edit by hand.
logging=%t
`, settings.Logging)

	temp, err := os.CreateTemp(filepath.Dir(path), "settings-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := temp.Name()

	if _, err := temp.WriteString(contents); err != nil {
		_ = temp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// parseSetting reads one "key=value" line. Blank lines and comments are
// skipped, and so is anything without an "=", since that is a comment someone
// forgot to mark.
func parseSetting(raw string) (key, value string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value), true
}
