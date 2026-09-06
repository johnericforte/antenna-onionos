package provider

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testSources serves archive fixtures from disk and playlists from memory, so
// nothing here opens a socket.
func testSources(items []Item, playlists map[string]string) *Sources {
	return &Sources{
		items: items,
		archive: &archive{
			fetch: func(_ context.Context, itemID string) ([]byte, error) {
				return os.ReadFile(filepath.Join("testdata", itemID+".json"))
			},
		},
		get: func(_ context.Context, rawURL string) ([]byte, error) {
			body, ok := playlists[rawURL]
			if !ok {
				return nil, errors.New("no such playlist: " + rawURL)
			}
			return []byte(body), nil
		},
	}
}

func TestBrowseRootListsConfiguredSources(t *testing.T) {
	s := testSources([]Item{
		{Kind: ArchiveItem, Ref: itemIdeal, Title: "Classic Cartoons"},
		{Kind: DirectVideo, Ref: "https://example.org/film.mp4", Title: "A Film"},
	}, nil)

	entries, err := s.Browse(context.Background(), "")
	if err != nil {
		t.Fatalf("Browse root: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d root entries, want 2", len(entries))
	}
	// Configured order is the order supplied, not alphabetical.
	if entries[0].Title != "Classic Cartoons" || entries[1].Title != "A Film" {
		t.Fatalf("root order changed: %q, %q", entries[0].Title, entries[1].Title)
	}
	for _, e := range entries {
		if !e.IsFolder {
			t.Errorf("%s should browse as a folder", e.ID)
		}
	}
}

// A direct video needs no request at all: the line already says everything.
func TestBrowseDirectVideoIsOneTitleAndNoFetch(t *testing.T) {
	const ref = "https://example.org/films/short.mp4"
	s := testSources([]Item{{Kind: DirectVideo, Ref: ref, Title: "A Short Film"}}, nil)

	entries, err := s.Browse(context.Background(), ref)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].ID != ref || entries[0].Title != "A Short Film" {
		t.Errorf("entry is %+v", entries[0])
	}
	if entries[0].IsFolder {
		t.Error("a single video is not a folder")
	}
}

func TestBrowsePlaylistExpandsToTitles(t *testing.T) {
	const ref = "https://example.org/lists/mine.m3u"
	s := testSources(
		[]Item{{Kind: PlaylistFile, Ref: ref, Title: "My List"}},
		map[string]string{ref: `#EXTM3U
#EXTINF:212,First Film
https://example.org/films/first.mp4

# a comment line
#EXTINF:98,Second Film
../films/second.mp4
https://example.org/films/third.mp4
`},
	)

	entries, err := s.Browse(context.Background(), ref)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, wanted 3: %+v", len(entries), entries)
	}

	if entries[0].Title != "First Film" || entries[0].ID != "https://example.org/films/first.mp4" {
		t.Errorf("first entry is %+v", entries[0])
	}
	// Relative entries are normal in an m3u, so they resolve against the
	// playlist's own URL rather than being dropped.
	if entries[1].ID != "https://example.org/films/second.mp4" {
		t.Errorf("relative entry resolved to %q", entries[1].ID)
	}
	if entries[1].Title != "Second Film" {
		t.Errorf("second entry title is %q", entries[1].Title)
	}
	// No EXTINF, so the file name stands in.
	if entries[2].Title != "third" {
		t.Errorf("third entry title is %q, want the file name", entries[2].Title)
	}
}

// A plain list of URLs is a playlist too. That is the format someone writes by
// hand, and it needs no m3u header.
func TestBrowsePlainURLListWorks(t *testing.T) {
	const ref = "https://example.org/lists/plain.txt"
	s := testSources(
		[]Item{{Kind: PlaylistFile, Ref: ref, Title: "Plain"}},
		map[string]string{ref: "https://example.org/a.mp4\nhttps://example.org/b.mp4\n"},
	)

	entries, err := s.Browse(context.Background(), ref)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

func TestBrowsePlaylistWithNoVideosIsReported(t *testing.T) {
	const ref = "https://example.org/lists/empty.m3u"
	s := testSources(
		[]Item{{Kind: PlaylistFile, Ref: ref, Title: "Empty"}},
		map[string]string{ref: "#EXTM3U\n# nothing here\n"},
	)

	_, err := s.Browse(context.Background(), ref)
	if err == nil {
		t.Fatal("an empty playlist should be reported, not shown as no titles")
	}
	if !strings.Contains(err.Error(), "no video URLs") {
		t.Errorf("error is %q", err)
	}
}

func TestBrowsePlaylistFetchFailurePropagates(t *testing.T) {
	s := testSources([]Item{{Kind: PlaylistFile, Ref: "https://example.org/gone.m3u"}}, nil)

	if _, err := s.Browse(context.Background(), "https://example.org/gone.m3u"); err == nil {
		t.Fatal("a playlist that cannot be fetched should be reported")
	}
}

// Nothing describes a user supplied URL, so the size gate has nothing to judge
// and must stand aside rather than refuse the video.
func TestResolveDirectURLIsPlayable(t *testing.T) {
	const ref = "https://example.org/films/short.mp4"
	s := testSources(nil, nil)

	stream, err := s.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if stream.URL != ref {
		t.Errorf("URL is %q", stream.URL)
	}
	if !stream.Unverified {
		t.Error("a URL nothing described should be marked unverified")
	}
	if playable, reason := stream.Playable(); !playable {
		t.Fatalf("a user supplied URL was refused: %s", reason)
	}
}

// The gate still rejects what it knows it cannot play, unverified or not.
func TestUnverifiedStreamStillFailsTheKindGate(t *testing.T) {
	s := &Stream{Kind: HLS, Codec: H264, Unverified: true}
	if playable, _ := s.Playable(); playable {
		t.Error("HLS was accepted because it was unverified")
	}
}

func TestResolveRejectsAURLItCannotFetch(t *testing.T) {
	s := testSources(nil, nil)

	for _, id := range []string{"ftp://example.org/v.mp4", "https://exa mple.org/v.mp4"} {
		if _, err := s.Resolve(context.Background(), id); err == nil {
			t.Errorf("Resolve(%q) should fail", id)
		}
	}
}

// An archive source still goes to the metadata API, so the shape of the line
// is genuinely what decides the path taken.
func TestBrowseArchiveItemStillUsesTheMetadataAPI(t *testing.T) {
	s := testSources([]Item{{Kind: ArchiveItem, Ref: itemIdeal, Title: "Ideal"}}, nil)

	entries, err := s.Browse(context.Background(), itemIdeal)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 12 {
		t.Fatalf("got %d entries, want the 12 from the fixture", len(entries))
	}
	if !strings.HasPrefix(entries[0].ID, itemIdeal+"/") {
		t.Errorf("entry id %q is not rooted at its item", entries[0].ID)
	}
}

func TestIsURL(t *testing.T) {
	urls := []string{"http://example.org/v.mp4", "https://example.org/v.mp4"}
	ids := []string{"classic_cartoons_201603", "classic_cartoons_201603/A Coy Decoy.mp4"}

	for _, u := range urls {
		if !isURL(u) {
			t.Errorf("%q should read as a URL", u)
		}
	}
	for _, id := range ids {
		if isURL(id) {
			t.Errorf("%q should read as an archive id", id)
		}
	}
}

func TestExtinfTitle(t *testing.T) {
	tests := map[string]string{
		"#EXTINF:212,First Film": "First Film",
		"#EXTINF:-1, Spaced ":    "Spaced",
		"#EXTINF:212":            "",
		"#EXTM3U":                "",
		"#EXTINF:212,":           "",
		"not a comment at all":   "",
	}

	for line, want := range tests {
		got, ok := extinfTitle(line)
		if got != want || ok != (want != "") {
			t.Errorf("extinfTitle(%q) = %q, %v; want %q", line, got, ok, want)
		}
	}
}

// A playlist that names something this app cannot fetch drops that line rather
// than failing the whole list.
func TestParsePlaylistSkipsUnusableEntries(t *testing.T) {
	base, err := url.Parse("https://example.org/lists/mine.m3u")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	entries, _, err := parsePlaylist([]byte("ftp://example.org/a.mp4\nhttps://example.org/b.mp4\n"), base)
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want only the http one: %+v", len(entries), entries)
	}
	if entries[0].ID != "https://example.org/b.mp4" {
		t.Errorf("kept %q", entries[0].ID)
	}
}

// HLS is the one thing the gate is meant to catch, and nothing set Kind to it,
// so an .m3u8 sailed through as progressive and reached ffplay.
func TestResolveMarksHLSSoTheGateCanRefuseIt(t *testing.T) {
	s := testSources(nil, nil)

	stream, err := s.Resolve(context.Background(), "https://example.org/live.m3u8")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if stream.Kind != HLS {
		t.Fatalf("kind is %v, want HLS", stream.Kind)
	}
	playable, reason := stream.Playable()
	if playable {
		t.Fatal("HLS was accepted")
	}
	if !strings.Contains(reason, "HLS") {
		t.Errorf("reason is %q, want it to name HLS", reason)
	}
}

func TestResolvePlainVideoStaysProgressive(t *testing.T) {
	s := testSources(nil, nil)

	stream, err := s.Resolve(context.Background(), "https://example.org/film.mp4")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if stream.Kind != Progressive {
		t.Errorf("kind is %v, want progressive", stream.Kind)
	}
}

// A line longer than the scanner's buffer stops it dead. Reported as "lists no
// video URLs", that sends the user to look at the wrong thing.
func TestParsePlaylistReportsAnUnreadableLine(t *testing.T) {
	base, err := url.Parse("https://example.org/lists/mine.m3u")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	huge := "https://example.org/" + strings.Repeat("a", 128*1024) + ".mp4\n"
	if _, _, err := parsePlaylist([]byte(huge), base); err == nil {
		t.Fatal("a line the scanner cannot read should be reported")
	}
}

// A server that answers 200 with a branded error page is common, and every
// line of HTML resolves into a plausible looking URL. Without a check, a dead
// playlist browses as a screen of titles that each fail when chosen.
func TestBrowsePlaylistRejectsAnErrorPage(t *testing.T) {
	const ref = "https://example.org/lists/gone.m3u"
	s := testSources(
		[]Item{{Kind: PlaylistFile, Ref: ref, Title: "Gone"}},
		map[string]string{ref: `<html>
<head><title>404 Not Found</title></head>
<body>
<h1>Not Found</h1>
The requested URL was not found on this server.
</body>
</html>`},
	)

	_, err := s.Browse(context.Background(), ref)
	if err == nil {
		t.Fatal("an HTML error page was accepted as a playlist")
	}
	if !strings.Contains(err.Error(), "does not look like a playlist") &&
		!strings.Contains(err.Error(), "no video URLs") {
		t.Errorf("error is %q, which does not explain the problem", err)
	}
}

// A playlist naming another playlist is how a list ends up pointing at itself.
func TestParsePlaylistDropsPagesAndPlaylists(t *testing.T) {
	base, err := url.Parse("https://example.org/lists/mine.m3u")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	body := strings.Join([]string{
		"https://example.org/films/good.mp4",
		"https://example.org/lists/mine.m3u",
		"https://example.org/index.html",
		"https://example.org/art.jpg",
		"https://example.org/stream/no-extension",
	}, "\n")

	entries, dropped, err := parsePlaylist([]byte(body), base)
	if err != nil {
		t.Fatalf("parsePlaylist: %v", err)
	}
	if dropped != 3 {
		t.Errorf("dropped %d lines, want 3", dropped)
	}
	// No extension is ambiguous rather than wrong, so it is kept.
	if len(entries) != 2 {
		t.Fatalf("kept %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].ID != "https://example.org/films/good.mp4" {
		t.Errorf("first kept entry is %q", entries[0].ID)
	}
}

// A mostly rejected file was probably never a playlist, and showing the few
// lines that happened to parse would hide that.
func TestBrowsePlaylistRefusesWhenMostLinesAreNotVideos(t *testing.T) {
	const ref = "https://example.org/lists/mixed.txt"
	s := testSources(
		[]Item{{Kind: PlaylistFile, Ref: ref, Title: "Mixed"}},
		map[string]string{ref: strings.Join([]string{
			"https://example.org/a.html",
			"https://example.org/b.html",
			"https://example.org/c.html",
			"https://example.org/good.mp4",
		}, "\n")},
	)

	if _, err := s.Browse(context.Background(), ref); err == nil {
		t.Fatal("a file that is mostly not video URLs was accepted")
	}
}
