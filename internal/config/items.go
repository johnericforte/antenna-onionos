// Package config reads the list of archive.org items the app browses.
//
// The list lives in a plain text file on the card so it can be edited with any
// text editor on any machine, without rebuilding the app. Nothing here knows
// what the items contain: point it at lectures, concerts or home video and the
// app behaves the same way.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"

	"antenna/internal/provider"
)

// FileName is the item list, read from the app folder on the card.
const FileName = "items.txt"

// maxItems caps the list. Every item costs one network round trip at startup,
// and a list long enough to matter would leave the user on a loading screen.
const maxItems = 50

// Load reads path and returns the items listed in it.
//
// A missing file is not an error: the app ships a default list and a user who
// has not chosen anything should still get something to watch. Anything else
// wrong with the file is an error, because silently browsing the defaults
// after someone edited the file is worse than saying the edit was rejected.
func Load(path string) ([]provider.Item, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var items []provider.Item
	scanner := bufio.NewScanner(file)

	for line := 1; scanner.Scan(); line++ {
		item, ok, err := parseLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		if !ok {
			continue
		}
		if len(items) == maxItems {
			return nil, fmt.Errorf("%s: more than %d items", path, maxItems)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return items, nil
}

// playlistExtensions mark a URL as a list of videos rather than one video.
//
// .m3u8 is deliberately absent. It is HLS, whose playlists list a few seconds
// of video each rather than whole titles, and this device cannot play HLS at
// all. Expanding one would fill the list with hundreds of unplayable segments.
// Left as a single source, it reaches the gate and is refused with a reason.
var playlistExtensions = []string{".m3u", ".txt"}

// parseLine reads one source: a reference, then optionally a name to show
// instead of it. Blank lines and comments are skipped.
//
// The shape of the reference decides how it is read, which is what makes a non
// archive.org source a one line change rather than a code change.
//
//	classic_cartoons_201603 Classic Cartoons     an archive.org item
//	https://example.org/film.mp4 A Film          one video
//	https://example.org/list.m3u My List         a playlist of videos
//	# a comment
func parseLine(raw string) (provider.Item, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return provider.Item{}, false, nil
	}

	ref, title, _ := strings.Cut(line, " ")
	title = strings.TrimSpace(title)

	kind, err := kindOf(ref)
	if err != nil {
		return provider.Item{}, false, err
	}
	if title == "" {
		title = defaultTitle(kind, ref)
	}
	return provider.Item{Kind: kind, Ref: ref, Title: title}, true, nil
}

// kindOf decides how a reference is read, and rejects anything that is neither
// an identifier nor a usable URL.
func kindOf(ref string) (provider.SourceKind, error) {
	if !strings.Contains(ref, "://") {
		return provider.ArchiveItem, validID(ref)
	}

	parsed, err := url.Parse(ref)
	if err != nil {
		return 0, fmt.Errorf("%q is not a URL: %w", ref, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return 0, fmt.Errorf("%q is not an http or https URL", ref)
	}
	if parsed.Host == "" {
		return 0, fmt.Errorf("%q has no host", ref)
	}

	ext := strings.ToLower(path.Ext(parsed.Path))
	if slices.Contains(playlistExtensions, ext) {
		return provider.PlaylistFile, nil
	}
	return provider.DirectVideo, nil
}

// defaultTitle is what shows when a line gives no name. A bare URL fills the
// screen and tells the user nothing, so the file name is used instead.
func defaultTitle(kind provider.SourceKind, ref string) string {
	if kind == provider.ArchiveItem {
		return ref
	}
	if parsed, err := url.Parse(ref); err == nil {
		if base := path.Base(parsed.Path); base != "" && base != "/" && base != "." {
			if name := strings.TrimSuffix(base, path.Ext(base)); name != "" {
				return name
			}
		}
		if parsed.Host != "" {
			return parsed.Host
		}
	}
	return ref
}

// validID rejects anything that is not an archive.org identifier. A slash
// would change which URL is fetched, and the rest are simply not identifiers,
// so catching them here turns a silent empty list into a stated reason. A
// reference containing "://" never reaches this: it is read as a URL.
func validID(id string) error {
	if id == "" {
		return errors.New("no identifier")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("%q is not an archive.org identifier, %q is not allowed", id, r)
		}
	}
	return nil
}
