package config

import (
	"antenna/internal/provider"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadReadsIdentifiersAndNames(t *testing.T) {
	path := write(t, `# Antenna item list
classic_cartoons_201603 Classic Cartoons
pdcartooncollection

   disneycartoons-publicdomain   Disney Public Domain
bare_identifier
`)

	items, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4", len(items))
	}

	if items[0].Ref != "classic_cartoons_201603" || items[0].Title != "Classic Cartoons" {
		t.Errorf("first item is %+v", items[0])
	}
	// No name given, so the identifier is the name.
	if items[1].Ref != "pdcartooncollection" || items[1].Title != "pdcartooncollection" {
		t.Errorf("second item is %+v", items[1])
	}
	if items[2].Title != "Disney Public Domain" {
		t.Errorf("surrounding space was not trimmed: %+v", items[2])
	}
	if items[3].Ref != "bare_identifier" {
		t.Errorf("fourth item is %+v", items[3])
	}
}

// The order in the file is the order on screen, so a user can put what they
// watch most at the top.
func TestLoadKeepsFileOrder(t *testing.T) {
	items, err := Load(write(t, "third_one\nfirst_one\nsecond_one\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"third_one", "first_one", "second_one"}
	for i, id := range want {
		if items[i].Ref != id {
			t.Errorf("item %d is %q, want %q", i, items[i].Ref, id)
		}
	}
}

// A missing file means the user has not chosen anything, so the app falls back
// to what it ships with rather than showing an empty list.
func TestLoadTreatsAMissingFileAsNoChoice(t *testing.T) {
	items, err := Load(filepath.Join(t.TempDir(), "absent.txt"))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if items != nil {
		t.Errorf("got %v, want nothing", items)
	}
}

// An edited file that cannot be read is different: browsing the defaults after
// someone deliberately changed the list hides the mistake.
func TestLoadRejectsABadIdentifier(t *testing.T) {
	tests := map[string]string{
		"a slash would change the URL fetched": "some/item\n",
		"a query string is not an identifier":  "item?raw=1\n",
	}

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, contents))
			if err == nil {
				t.Fatal("the line was accepted")
			}
			if !strings.Contains(err.Error(), "line 1") {
				t.Errorf("error should name the line, got %q", err)
			}
		})
	}
}

func TestLoadRejectsAnOverlongList(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= maxItems; i++ {
		b.WriteString("item_")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("\n")
	}

	if _, err := Load(write(t, b.String())); err == nil {
		t.Fatal("a list past the cap was accepted")
	}
}

func TestLoadIgnoresCommentsAndBlankLines(t *testing.T) {
	items, err := Load(write(t, "\n# just a comment\n\n   # indented comment\nreal_item\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(items) != 1 || items[0].Ref != "real_item" {
		t.Fatalf("got %+v, want one real item", items)
	}
}

// An empty file is a deliberate choice to browse nothing, and it is reported
// as such rather than silently restoring the defaults.
func TestLoadOnAnEmptyFileReturnsNothing(t *testing.T) {
	items, err := Load(write(t, "# everything commented out\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %+v, want nothing", items)
	}
}

// The whole point of Phase A: a source that is not archive.org is one line in
// a text file, not a code change.
func TestLoadReadsTheThreeLineShapes(t *testing.T) {
	items, err := Load(write(t, `classic_cartoons_201603 Classic Cartoons
https://example.org/films/short.mp4 A Short Film
https://example.org/lists/mine.m3u My List
https://example.org/lists/plain.txt Plain List
https://example.org/films/nameless.mp4
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}

	want := []struct {
		kind  provider.SourceKind
		ref   string
		title string
	}{
		{provider.ArchiveItem, "classic_cartoons_201603", "Classic Cartoons"},
		{provider.DirectVideo, "https://example.org/films/short.mp4", "A Short Film"},
		{provider.PlaylistFile, "https://example.org/lists/mine.m3u", "My List"},
		{provider.PlaylistFile, "https://example.org/lists/plain.txt", "Plain List"},
		// No name given, so the file name stands in. A bare URL fills the
		// screen and says nothing.
		{provider.DirectVideo, "https://example.org/films/nameless.mp4", "nameless"},
	}
	for i, w := range want {
		got := items[i]
		if got.Kind != w.kind || got.Ref != w.ref || got.Title != w.title {
			t.Errorf("item %d is %+v, want kind %v ref %q title %q", i, got, w.kind, w.ref, w.title)
		}
	}
}

func TestLoadRejectsAUnusableURL(t *testing.T) {
	tests := map[string]string{
		"a scheme this app cannot fetch": "ftp://example.org/film.mp4\n",
		"a file path is not a source":    "file:///mnt/SDCARD/film.mp4\n",
		"a URL with no host":             "https:///film.mp4\n",
	}

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, contents)); err == nil {
				t.Fatal("the line was accepted")
			}
		})
	}
}

// An m3u8 is HLS. Its playlist lists seconds of video rather than titles, and
// the device cannot play HLS at all, so expanding one would fill the list with
// hundreds of unplayable fragments. It stays a single source, which the device
// gate then refuses with a reason the user can read.
func TestLoadReadsM3U8AsASingleSourceNotAPlaylist(t *testing.T) {
	items, err := Load(write(t, "https://example.org/live.m3u8 Stream\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if items[0].Kind != provider.DirectVideo {
		t.Errorf("kind is %v, want a single source", items[0].Kind)
	}
}
