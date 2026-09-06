package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"antenna/internal/dbg"
)

// maxPlaylistBytes caps a playlist download. A playlist is a list of URLs, so
// anything larger than this is not one.
const maxPlaylistBytes = 1 << 20

// maxPlaylistEntries caps how many titles one playlist contributes. The list is
// browsed on a d-pad, and a playlist long enough to exceed this is not
// navigable on this device anyway.
const maxPlaylistEntries = 500

// notVideoExtensions are what a line in a playlist must not end in. A server
// that answers 200 with an error page is the case this catches: every line of
// "<html><h1>Not Found</h1>" parses as a relative path and resolves into a
// plausible looking URL, so without this a broken playlist browses as a screen
// of titles that each fail when chosen.
var notVideoExtensions = []string{
	".html", ".htm", ".php", ".asp", ".aspx", ".json", ".xml", ".txt",
	".m3u", ".m3u8", ".css", ".js", ".jpg", ".jpeg", ".png", ".gif",
}

// Sources is the one Provider. It browses whatever the item list points at,
// and the shape of each line decides how that source is read: an archive.org
// identifier goes to the metadata API, a video URL is played as it stands, and
// a playlist URL is expanded into the videos it names.
//
// There is deliberately no second Provider for non archive.org sources. A user
// adds one by writing a line, not by choosing an implementation.
type Sources struct {
	items   []Item
	archive *archive

	// get fetches a URL for the playlist path. It is a field so tests can
	// serve playlists without opening a socket.
	get func(ctx context.Context, rawURL string) ([]byte, error)
}

// NewSources returns a provider over the given items, in the order supplied.
func NewSources(items []Item) *Sources {
	client := &http.Client{Timeout: requestTimeout}
	return &Sources{
		items:   items,
		archive: newArchive(client),
		get: func(ctx context.Context, rawURL string) ([]byte, error) {
			return fetchURL(ctx, client, rawURL)
		},
	}
}

// Name implements Provider.
func (s *Sources) Name() string { return "Antenna" }

// Browse implements Provider. An empty path lists the configured sources; any
// other path is one source's Ref, and lists the titles it holds.
func (s *Sources) Browse(ctx context.Context, path string) ([]Entry, error) {
	if path == "" {
		entries := make([]Entry, 0, len(s.items))
		for _, item := range s.items {
			entries = append(entries, Entry{ID: item.Ref, Title: item.Title, IsFolder: true})
		}
		return entries, nil
	}

	item, ok := s.item(path)
	if !ok {
		// Not a configured source. It is still an archive identifier as far as
		// anything here can tell, which keeps ids produced by an earlier run
		// working.
		return s.archive.entries(ctx, path)
	}

	switch item.Kind {
	case DirectVideo:
		// One URL is one title. There is nothing to list and nothing to fetch.
		return []Entry{{ID: item.Ref, Title: item.Title}}, nil
	case PlaylistFile:
		return s.playlist(ctx, item)
	default:
		return s.archive.entries(ctx, item.Ref)
	}
}

// Resolve implements Provider.
func (s *Sources) Resolve(ctx context.Context, id string) (*Stream, error) {
	if isURL(id) {
		return directStream(id)
	}
	return s.archive.resolve(ctx, id)
}

func (s *Sources) item(ref string) (Item, bool) {
	for _, item := range s.items {
		if item.Ref == ref {
			return item, true
		}
	}
	return Item{}, false
}

// playlist downloads a playlist and turns each video URL in it into a title.
func (s *Sources) playlist(ctx context.Context, item Item) ([]Entry, error) {
	body, err := s.get(ctx, item.Ref)
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(item.Ref)
	if err != nil {
		return nil, fmt.Errorf("playlist %s: %w", item.Ref, err)
	}

	entries, dropped, err := parsePlaylist(body, base)
	if err != nil {
		return nil, fmt.Errorf("playlist %s: %w", item.Ref, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s lists no video URLs", item.Ref)
	}
	// Mostly rejected means the file was probably never a playlist. An error
	// page answered with 200 is the usual cause, and showing the handful of
	// lines that happened to parse would hide that.
	if dropped > len(entries) {
		return nil, fmt.Errorf("%s does not look like a playlist: %d of %d lines are not video URLs",
			item.Ref, dropped, dropped+len(entries))
	}
	if dropped > 0 {
		dbg.Printf("playlist %s: skipped %d lines that are not video URLs", item.Ref, dropped)
	}
	return entries, nil
}

// parsePlaylist reads the m3u subset that matters here, which is also exactly
// what a plain list of URLs looks like: one URL per line, blank lines and
// comments skipped. An "#EXTINF" line names the video that follows it, so its
// title is used when there is one.
func parsePlaylist(body []byte, base *url.URL) (entries []Entry, dropped int, err error) {
	var nextTitle string

	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() && len(entries) < maxPlaylistEntries {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if title, ok := extinfTitle(line); ok {
				nextTitle = title
			}
			continue
		}

		if !couldBeURL(line) {
			dropped++
			continue
		}
		resolved, err := resolveAgainst(base, line)
		if err != nil {
			dropped++
			continue
		}
		if !looksLikeVideo(resolved) {
			dropped++
			continue
		}
		title := nextTitle
		if title == "" {
			title = displayName(path.Base(resolved.Path))
		}
		if title == "" {
			title = resolved.String()
		}
		entries = append(entries, Entry{ID: resolved.String(), Title: title})
		nextTitle = ""
	}
	// A line longer than the scanner's buffer stops it dead. Without this the
	// playlist comes back short, or empty and reported as listing no videos,
	// which sends the user looking at the wrong file.
	if err := scanner.Err(); err != nil {
		return nil, dropped, fmt.Errorf("cannot read the playlist: %w", err)
	}
	// Hitting the cap is not an error, but the titles past it are gone and the
	// user has no way to tell from the list alone.
	if len(entries) == maxPlaylistEntries {
		dbg.Printf("playlist: stopped at %d entries, the rest were not read", maxPlaylistEntries)
	}
	return entries, dropped, nil
}

// couldBeURL rejects a line before it is resolved, because url.Parse is far
// more permissive than a URL actually is: "<h1>Not Found</h1>" and "The
// requested URL was not found on this server." both parse as relative paths
// and resolve into plausible looking links.
//
// Angle brackets and raw spaces are never valid unencoded in a URL, so their
// presence means the line is prose or markup. A sloppy playlist with unescaped
// spaces in a file name loses those lines, which is reported rather than
// silent, and is the better trade against browsing a screen of HTML.
func couldBeURL(line string) bool {
	return !strings.ContainsAny(line, "<> \t")
}

// looksLikeVideo rejects a playlist line that resolved cleanly but is plainly
// not a video: a page, a stylesheet, or another playlist. A playlist naming a
// playlist is how a list ends up pointing at itself.
func looksLikeVideo(u *url.URL) bool {
	ext := strings.ToLower(path.Ext(u.Path))
	if ext == "" {
		// No extension is ambiguous rather than wrong, and plenty of media
		// URLs have none, so it is allowed through.
		return true
	}
	for _, bad := range notVideoExtensions {
		if ext == bad {
			return false
		}
	}
	return true
}

// extinfTitle pulls the display name out of an EXTINF line, which is written
// "#EXTINF:<seconds>,<title>".
func extinfTitle(line string) (string, bool) {
	if !strings.HasPrefix(line, "#EXTINF:") {
		return "", false
	}
	_, title, ok := strings.Cut(line, ",")
	if !ok {
		return "", false
	}
	title = strings.TrimSpace(title)
	return title, title != ""
}

// resolveAgainst turns a playlist entry into an absolute URL. Relative entries
// are normal in an m3u, and rejecting them would refuse most real playlists.
func resolveAgainst(base *url.URL, raw string) (*url.URL, error) {
	ref, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return nil, fmt.Errorf("%s is not an http URL", resolved)
	}
	return resolved, nil
}

// directStream describes a video the app was pointed at rather than told
// about. Nothing published its dimensions, so the stream is marked unverified
// and the size gate stands aside.
func directStream(rawURL string) (*Stream, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("bad video URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%s is not an http URL", rawURL)
	}
	// An .m3u8 is HLS. The gate refuses it, but only if it is told what it is:
	// claiming everything is progressive would slip HLS past the one check
	// that a source is not allowed to opt out of.
	kind := Progressive
	if strings.EqualFold(path.Ext(parsed.Path), ".m3u8") {
		kind = HLS
	}

	return &Stream{
		Kind:       kind,
		URL:        rawURL,
		Codec:      H264,
		Unverified: true,
	}, nil
}

// isURL reports whether an entry id is a URL rather than an archive.org
// identifier plus file name. Identifiers cannot contain a colon, so the scheme
// is enough to tell them apart.
func isURL(id string) bool {
	return strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://")
}

func fetchURL(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", rawURL, resp.Status)
	}
	// A server answering an error with a branded HTML page and a 200 is common
	// enough that the status alone does not say the body is a playlist.
	if mediaType := resp.Header.Get("Content-Type"); strings.HasPrefix(mediaType, "text/html") {
		return nil, fmt.Errorf("%s returned a web page, not a playlist", rawURL)
	}

	// One byte past the cap, so truncation is detectable. Silently cutting the
	// file would leave a half written URL on the last line, which resolves
	// against the base and becomes a title that fails when chosen.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", rawURL, err)
	}
	if len(body) > maxPlaylistBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes, which is not a playlist", rawURL, maxPlaylistBytes)
	}
	return body, nil
}
