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
	"os"
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

// parseLine reads one entry: the archive.org identifier, then optionally a
// name to show instead of it. Blank lines and comments are skipped.
//
//	classic_cartoons_201603 Classic Cartoons
//	# a comment
//	pdcartooncollection
func parseLine(raw string) (provider.Item, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return provider.Item{}, false, nil
	}

	id, title, _ := strings.Cut(line, " ")
	title = strings.TrimSpace(title)

	if err := validID(id); err != nil {
		return provider.Item{}, false, err
	}
	if title == "" {
		title = id
	}
	return provider.Item{ID: id, Title: title}, true, nil
}

// validID rejects anything that is not an archive.org identifier. A slash
// would change which URL is fetched, and the rest are simply not identifiers,
// so catching them here turns a silent empty list into a stated reason.
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
