package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are real metadata responses captured from archive.org on
// 2026-09-06, trimmed to the video files. The thumbnails were dropped because
// they were nine tenths of the bytes and none of the behaviour.
//
// item-ideal        classic_cartoons_201603, an h.264 derivative on every title
// item-partial      disneycartoons-publicdomain, 9 derivatives across 20 titles
// item-mixed        pdcartooncollection, no h.264 derivatives at all
// item-unplayable   titles from the same item where nothing clears the gate
// item-empty        the {} archive.org returns for an identifier that is gone
// item-malformed    a truncated response
const (
	itemIdeal      = "item-ideal"
	itemPartial    = "item-partial"
	itemMixed      = "item-mixed"
	itemUnplayable = "item-unplayable"
	itemEmpty      = "item-empty"
	itemMalformed  = "item-malformed"
)

// testArchive returns a provider that reads fixtures from disk. Nothing in
// this file opens a socket, so the suite passes with networking unavailable.
func testArchive(t *testing.T, items ...Item) *Archive {
	t.Helper()
	return &Archive{
		items: items,
		fetch: func(_ context.Context, itemID string) ([]byte, error) {
			body, err := os.ReadFile(filepath.Join("testdata", itemID+".json"))
			if err != nil {
				return nil, err
			}
			return body, nil
		},
	}
}

func TestBrowseRootListsCuratedItems(t *testing.T) {
	a := testArchive(t,
		Item{ID: itemIdeal, Title: "Classic Cartoons"},
		Item{ID: itemPartial, Title: "Disney Public Domain"},
	)

	entries, err := a.Browse(context.Background(), "")
	if err != nil {
		t.Fatalf("Browse root: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d root entries, want 2", len(entries))
	}
	// Curated order is the order supplied, not alphabetical.
	if entries[0].Title != "Classic Cartoons" || entries[1].Title != "Disney Public Domain" {
		t.Fatalf("root order changed: %q, %q", entries[0].Title, entries[1].Title)
	}
	for _, e := range entries {
		if !e.IsFolder {
			t.Errorf("%s should browse as a folder", e.ID)
		}
	}
}

// TestBrowseItemPlayableCounts pins the measured behaviour of each fixture.
// The counts are what makes derivative scoring worth having: two of the three
// real items are majority undecodable, and the app has to survive that.
func TestBrowseItemPlayableCounts(t *testing.T) {
	tests := []struct {
		item     string
		titles   int
		playable int
	}{
		{item: itemIdeal, titles: 12, playable: 12},
		{item: itemPartial, titles: 20, playable: 6},
		{item: itemMixed, titles: 14, playable: 6},
		{item: itemUnplayable, titles: 6, playable: 0},
	}

	for _, tc := range tests {
		t.Run(tc.item, func(t *testing.T) {
			a := testArchive(t)

			titles, err := a.titles(context.Background(), tc.item)
			if err != nil {
				t.Fatalf("titles: %v", err)
			}
			if len(titles) != tc.titles {
				t.Errorf("grouped %d titles, want %d", len(titles), tc.titles)
			}

			entries, err := a.Browse(context.Background(), tc.item)
			if err != nil {
				t.Fatalf("Browse: %v", err)
			}
			if len(entries) != tc.playable {
				t.Errorf("browsed %d playable, want %d", len(entries), tc.playable)
			}

			for _, e := range entries {
				if !strings.HasPrefix(e.ID, tc.item+"/") {
					t.Errorf("entry id %q is not rooted at its item", e.ID)
				}
				if e.IsFolder {
					t.Errorf("%q is a title, not a folder", e.Title)
				}
			}
		})
	}
}

// An item where nothing is decodable browses empty and does not fail. This is
// the graceful skip: no error reaches the user, the list is simply short.
func TestBrowseUndecodableItemIsEmptyNotAnError(t *testing.T) {
	a := testArchive(t)

	entries, err := a.Browse(context.Background(), itemUnplayable)
	if err != nil {
		t.Fatalf("an undecodable item must not error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want none", len(entries))
	}
}

// archive.org answers 200 with {} for an identifier that is gone. Reporting
// that as an empty item makes a dead configuration look like a device that
// cannot decode anything, which sends the user to the wrong problem.
func TestBrowseMissingItemIsAnError(t *testing.T) {
	a := testArchive(t)

	_, err := a.Browse(context.Background(), itemEmpty)
	if err == nil {
		t.Fatal("an identifier that no longer exists should be reported")
	}
	if !strings.Contains(err.Error(), "no files") {
		t.Errorf("error should say the item is empty, got %q", err)
	}
}

func TestBrowseMalformedResponseErrorsWithoutPanicking(t *testing.T) {
	a := testArchive(t)

	if _, err := a.Browse(context.Background(), itemMalformed); err == nil {
		t.Fatal("truncated JSON should be reported, not ignored")
	}
}

func TestBrowseFetchFailurePropagates(t *testing.T) {
	wantErr := errors.New("cannot reach archive.org")
	a := &Archive{fetch: func(context.Context, string) ([]byte, error) {
		return nil, wantErr
	}}

	_, err := a.Browse(context.Background(), itemIdeal)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}

// The ideal item has three renditions of every title. The h.264 derivative is
// the only one that should ever be chosen: the .Mov original is an unreadable
// container and the .ogv is Theora.
func TestBrowsePrefersTheDerivativeOverOriginalAndTheora(t *testing.T) {
	a := testArchive(t)

	entries, err := a.Browse(context.Background(), itemIdeal)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}

	for _, e := range entries {
		if !strings.HasSuffix(e.ID, ".mp4") {
			t.Errorf("%q resolved to %q, want the .mp4 derivative", e.Title, e.ID)
		}
	}

	if got := entries[0]; got.Title != "A Coy Decoy" {
		t.Errorf("first title is %q, want %q", got.Title, "A Coy Decoy")
	}
	if got := entries[0].ID; got != itemIdeal+"/A Coy Decoy.mp4" {
		t.Errorf("first entry id is %q", got)
	}
	if got := entries[0].Duration; got != 470 {
		t.Errorf("duration is %d, want 470", got)
	}
}

// On the partial item every ".ia.mp4" is a byte for byte copy of its original,
// so the two group under one title and only one entry may appear.
func TestBrowseGroupsPassthroughDerivativeWithItsOriginal(t *testing.T) {
	a := testArchive(t)

	entries, err := a.Browse(context.Background(), itemPartial)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}

	// Either file is fine to play, since they are the same bytes. What must not
	// happen is the pair showing up as two separate titles.
	seen := make(map[string]int)
	for _, e := range entries {
		seen[e.Title]++
		if strings.HasSuffix(e.Title, ".ia") {
			t.Errorf("%q kept the .ia suffix in its display name", e.Title)
		}
	}
	for title, n := range seen {
		if n > 1 {
			t.Errorf("%q appears %d times, want once", title, n)
		}
	}

	if _, ok := seen["Cleanliness Brings Health (1945)"]; !ok {
		t.Error("an 830 kbps title should have survived the gate")
	}
	if _, ok := seen["All Together (1942)"]; ok {
		t.Error("a 1.5 Mbps title should have been skipped")
	}
}

func TestResolveBuildsDirectNodeURL(t *testing.T) {
	a := testArchive(t)

	stream, err := a.Resolve(context.Background(), itemIdeal+"/A Coy Decoy.mp4")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	const want = "https://dn600209.us.archive.org/0/items/classic_cartoons_201603/A%20Coy%20Decoy.mp4"
	if stream.URL != want {
		t.Errorf("URL is\n  %s\nwant\n  %s", stream.URL, want)
	}
	if stream.Kind != Progressive || stream.Codec != H264 {
		t.Errorf("got %s %s, want progressive H.264", stream.Kind, stream.Codec)
	}
	if stream.Height != 480 || stream.Width != 640 {
		t.Errorf("got %dx%d, want 640x480", stream.Width, stream.Height)
	}
	// 48820761 bytes over 470.4 seconds.
	if stream.Bitrate < 820_000 || stream.Bitrate > 840_000 {
		t.Errorf("bitrate is %d, want about 830 kbps", stream.Bitrate)
	}
	if playable, reason := stream.Playable(); !playable {
		t.Errorf("resolved stream is not playable: %s", reason)
	}
}

func TestResolveRefusesUndecodableFile(t *testing.T) {
	a := testArchive(t)

	// A real 1.5 Mbps passthrough from the partial item.
	_, err := a.Resolve(context.Background(), itemPartial+"/All Together (1942).mp4")
	if err == nil {
		t.Fatal("resolving a file over the bitrate limit should fail")
	}
	if !strings.Contains(err.Error(), "kbps") {
		t.Errorf("error should explain the limit to the user, got %q", err)
	}
}

func TestResolveRejectsUnknownInputs(t *testing.T) {
	a := testArchive(t)

	tests := []struct {
		name string
		id   string
	}{
		{name: "no separator", id: "item-ideal"},
		{name: "empty file", id: "item-ideal/"},
		{name: "empty item", id: "/A Coy Decoy.mp4"},
		{name: "file not in item", id: itemIdeal + "/Nothing Here.mp4"},
		{name: "Theora rendition", id: itemIdeal + "/A Coy Decoy.ogv"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.Resolve(context.Background(), tc.id); err == nil {
				t.Fatalf("Resolve(%q) should fail", tc.id)
			}
		})
	}
}

func TestCodecFor(t *testing.T) {
	tests := map[string]Codec{
		"h.264":        H264,
		"h.264 IA":     H264,
		"MPEG4":        H264,
		"Ogg Video":    Theora,
		"QuickTime":    CodecUnknown,
		"Thumbnail":    CodecUnknown,
		"Animated GIF": CodecUnknown,
		"":             CodecUnknown,
	}

	for format, want := range tests {
		if got := codecFor(format); got != want {
			t.Errorf("codecFor(%q) = %s, want %s", format, got, want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	tests := map[string]string{
		"A Coy Decoy.mp4":                             "A Coy Decoy",
		"All Together (1942).ia.mp4":                  "All Together (1942)",
		"COMICOLOR - 1935 - _Old Mother Hubbard_.mp4": "COMICOLOR - 1935 - _Old Mother Hubbard_",
		"no extension":                                "no extension",
	}

	for in, want := range tests {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBitrate(t *testing.T) {
	tests := []struct {
		name         string
		size, length string
		want         int
	}{
		{name: "typical derivative", size: "48820761", length: "470.4", want: 830_285},
		{name: "missing length", size: "48820761", length: "", want: 0},
		{name: "zero length", size: "48820761", length: "0", want: 0},
		{name: "missing size", size: "", length: "470.4", want: 0},
		{name: "not a number", size: "big", length: "470.4", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitrate(tc.size, tc.length); got != tc.want {
				t.Errorf("bitrate(%q, %q) = %d, want %d", tc.size, tc.length, got, tc.want)
			}
		})
	}
}
