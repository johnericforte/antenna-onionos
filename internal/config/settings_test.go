package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), SettingsFileName)
}

func TestLoadSettingsReadsWhatWasSaved(t *testing.T) {
	path := settingsPath(t)

	if err := SaveSettings(path, Settings{Logging: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, problems, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("a file this app wrote should parse cleanly, got %v", problems)
	}
	if !got.Logging {
		t.Error("logging did not survive the round trip")
	}
}

// Nothing changed yet is the normal first run, not a failure.
func TestLoadSettingsOnAMissingFileReturnsDefaults(t *testing.T) {
	got, problems, err := LoadSettings(filepath.Join(t.TempDir(), "absent.txt"))
	if err != nil {
		t.Fatalf("a missing settings file must not be an error: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("got problems %v", problems)
	}
	if got.Logging {
		t.Error("logging should default to off")
	}
}

// A settings file is a convenience. Refusing to start because one line in it is
// wrong would be a worse outcome than starting with the defaults, so the bad
// lines are reported rather than fatal.
func TestLoadSettingsReportsBadLinesWithoutFailing(t *testing.T) {
	path := settingsPath(t)
	contents := strings.Join([]string{
		"# a comment",
		"",
		"logging=maybe",
		"colour=blue",
		"a line with no equals sign",
		"logging=true",
	}, "\n")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, problems, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !got.Logging {
		t.Error("the valid line later in the file should still apply")
	}
	if len(problems) != 2 {
		t.Fatalf("got %d problems, want 2: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "line 3") || !strings.Contains(problems[1], "line 4") {
		t.Errorf("problems should name their lines: %v", problems)
	}
}

func TestSaveSettingsIsReadableByHand(t *testing.T) {
	path := settingsPath(t)
	if err := SaveSettings(path, Settings{Logging: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "logging=true") {
		t.Errorf("file does not carry the setting:\n%s", text)
	}
	if !strings.HasPrefix(text, "#") {
		t.Error("the file should say what it is, since a user may open it")
	}
}

// The card is removable. A half written settings file turns a working install
// into a puzzle, so the write lands whole or not at all.
func TestSaveSettingsLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SettingsFileName)

	if err := SaveSettings(path, Settings{Logging: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(names) != 1 || names[0].Name() != SettingsFileName {
		var got []string
		for _, n := range names {
			got = append(got, n.Name())
		}
		t.Errorf("directory holds %v, want only %s", got, SettingsFileName)
	}
}

func TestSaveSettingsOverwrites(t *testing.T) {
	path := settingsPath(t)

	if err := SaveSettings(path, Settings{Logging: true}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := SaveSettings(path, Settings{Logging: false}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, _, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.Logging {
		t.Error("the second save did not replace the first")
	}
}

func TestParseSetting(t *testing.T) {
	tests := []struct {
		line  string
		key   string
		value string
		ok    bool
	}{
		{line: "logging=true", key: "logging", value: "true", ok: true},
		{line: "  Logging = TRUE  ", key: "logging", value: "TRUE", ok: true},
		{line: "# comment", ok: false},
		{line: "", ok: false},
		{line: "no equals here", ok: false},
	}

	for _, tc := range tests {
		key, value, ok := parseSetting(tc.line)
		if key != tc.key || value != tc.value || ok != tc.ok {
			t.Errorf("parseSetting(%q) = %q, %q, %v; want %q, %q, %v",
				tc.line, key, value, ok, tc.key, tc.value, tc.ok)
		}
	}
}
