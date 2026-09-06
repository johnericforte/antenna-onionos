package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MetadataEndpoint is the read-only Internet Archive metadata API. It needs no
// authentication and returns the server, directory and file list for one item.
const MetadataEndpoint = "https://archive.org/metadata/"

// userAgent identifies the app to archive.org. They ask that clients say who
// they are, and a named client is far less likely to be rate limited.
const userAgent = "antenna/0.1 (+https://github.com/ericforte/antenna-onionos)"

// requestTimeout covers the whole metadata request. The device is on Wi-Fi and
// the user is staring at a list that cannot draw until this returns, so it is
// short on purpose.
const requestTimeout = 15 * time.Second

// maxResponseBytes caps what a single metadata response may occupy. Items with
// thousands of files exist, and the device has very little RAM.
const maxResponseBytes = 8 << 20

// archive reads the Internet Archive metadata API. It is not a Provider on its
// own: Sources owns the interface and hands archive identifiers here, because
// which API answers a source is decided by the shape of its line in the item
// list, not by the user picking a provider.
type archive struct {
	// fetch returns the raw metadata document for one item. It is a field so
	// tests can serve fixtures without opening a socket.
	fetch func(ctx context.Context, itemID string) ([]byte, error)
}

// newArchive returns a client backed by the live metadata API.
func newArchive(client *http.Client) *archive {
	return &archive{
		fetch: func(ctx context.Context, itemID string) ([]byte, error) {
			return fetchMetadata(ctx, client, itemID)
		},
	}
}

// entries lists the playable titles inside one item.
//
// Titles the device cannot decode are omitted rather than shown and refused.
// An item whose every title is undecodable browses as an empty list, which is
// a real and fairly common outcome: whole collections exist with no usable
// derivative on any title.
func (a *archive) entries(ctx context.Context, path string) ([]Entry, error) {
	titles, err := a.titles(ctx, path)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(titles))
	for _, t := range titles {
		if _, file := t.best(); file != "" {
			entries = append(entries, Entry{
				ID:       path + "/" + file,
				Title:    t.name,
				Duration: t.duration,
			})
		}
	}
	return entries, nil
}

// resolve turns an entry id back into a stream. The id is the item identifier
// and the chosen file name joined by a slash, as produced by entries.
func (a *archive) resolve(ctx context.Context, id string) (*Stream, error) {
	itemID, fileName, ok := strings.Cut(id, "/")
	if !ok || itemID == "" || fileName == "" {
		return nil, fmt.Errorf("malformed entry id %q", id)
	}

	doc, err := a.document(ctx, itemID)
	if err != nil {
		return nil, err
	}

	for i := range doc.Files {
		f := &doc.Files[i]
		if f.Name != fileName {
			continue
		}
		stream := doc.stream(f)
		if stream == nil {
			return nil, fmt.Errorf("%s has no usable stream", fileName)
		}
		if playable, reason := stream.Playable(); !playable {
			return nil, errors.New(reason)
		}
		return stream, nil
	}
	return nil, fmt.Errorf("%s is no longer in %s", fileName, itemID)
}

// title is one video grouped with every rendition of it the item holds.
type title struct {
	name       string
	duration   int
	candidates []*Stream
	files      []string
}

// best returns the rendition to play and the file it came from, or nil and an
// empty name when the device can decode none of them. Taller wins first,
// because a 480p derivative looks better on the panel than a 240p one, and the
// lighter file breaks the tie.
func (t *title) best() (*Stream, string) {
	var bestStream *Stream
	var bestFile string
	for i, s := range t.candidates {
		if playable, _ := s.Playable(); !playable {
			continue
		}
		if bestStream == nil ||
			s.Height > bestStream.Height ||
			(s.Height == bestStream.Height && s.Bitrate < bestStream.Bitrate) {
			bestStream, bestFile = s, t.files[i]
		}
	}
	return bestStream, bestFile
}

// titles groups an item's files by the video they are a rendition of, sorted
// by display name so the list does not reshuffle between runs.
func (a *archive) titles(ctx context.Context, itemID string) ([]*title, error) {
	doc, err := a.document(ctx, itemID)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*title)
	var order []string

	for i := range doc.Files {
		f := &doc.Files[i]
		stream := doc.stream(f)
		if stream == nil {
			continue
		}
		// A derivative names the file it was made from, so the original is the
		// stable grouping key. Files with no original are their own group.
		source := f.Original
		if source == "" {
			source = f.Name
		}
		name := displayName(source)

		t, seen := byName[name]
		if !seen {
			t = &title{name: name, duration: seconds(f.Length)}
			byName[name] = t
			order = append(order, name)
		}
		if t.duration == 0 {
			t.duration = seconds(f.Length)
		}
		t.candidates = append(t.candidates, stream)
		t.files = append(t.files, f.Name)
	}

	sort.Strings(order)
	titles := make([]*title, 0, len(order))
	for _, name := range order {
		titles = append(titles, byName[name])
	}
	return titles, nil
}

// document fetches and decodes one item's metadata.
//
// archive.org answers 200 with {} for an identifier that no longer exists, so
// an empty document is a broken configuration rather than an item with
// nothing worth playing. Those two look identical from the outside and they
// send whoever is debugging in opposite directions, so they are separated
// here.
func (a *archive) document(ctx context.Context, itemID string) (*metadataDoc, error) {
	body, err := a.fetch(ctx, itemID)
	if err != nil {
		return nil, err
	}

	var doc metadataDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("metadata for %s is not valid JSON: %w", itemID, err)
	}
	if len(doc.Files) == 0 {
		return nil, fmt.Errorf("archive.org has no files for %s", itemID)
	}
	return &doc, nil
}

// metadataDoc is the subset of the metadata response this app reads. Every
// numeric field arrives as a string, and any of them can be absent.
type metadataDoc struct {
	Server string     `json:"d1"`
	Dir    string     `json:"dir"`
	Files  []metaFile `json:"files"`
}

type metaFile struct {
	Name     string `json:"name"`
	Format   string `json:"format"`
	Source   string `json:"source"`
	Original string `json:"original"`
	Size     string `json:"size"`
	Length   string `json:"length"`
	Width    string `json:"width"`
	Height   string `json:"height"`
}

// stream turns one file entry into a Stream, or returns nil when the entry is
// not a video this app will ever consider. It does not judge playability; that
// is the gate's job.
func (d *metadataDoc) stream(f *metaFile) *Stream {
	codec := codecFor(f.Format)
	if codec == CodecUnknown {
		return nil
	}
	if d.Server == "" || d.Dir == "" || f.Name == "" {
		return nil
	}
	return &Stream{
		Kind:    Progressive,
		URL:     downloadURL(d.Server, d.Dir, f.Name),
		Width:   atoi(f.Width),
		Height:  atoi(f.Height),
		Bitrate: bitrate(f.Size, f.Length),
		Codec:   codec,
	}
}

// codecFor maps an archive.org format label to what the file actually decodes
// as. The three h.264 labels differ only in how the file was produced: "MPEG4"
// is an uploader's own MP4, "h.264" is a derivative, and "h.264 IA" is a
// passthrough copy that is often byte for byte the original. All three are
// MP4 containers carrying h.264.
//
// Anything else returns CodecUnknown and is dropped. "QuickTime" says nothing
// about the codec inside, and Ogg Theora has never been confirmed to decode on
// this hardware, so neither is offered.
func codecFor(format string) Codec {
	switch format {
	case "h.264", "h.264 IA", "MPEG4":
		return H264
	case "Ogg Video":
		return Theora
	default:
		return CodecUnknown
	}
}

// downloadURL builds the direct node URL for a file. archive.org redirects
// http to https on every path, so this is https from the start.
func downloadURL(server, dir, name string) string {
	u := url.URL{
		Scheme: "https",
		Host:   server,
		Path:   dir + "/" + name,
	}
	return u.String()
}

// displayName turns a file name into something worth showing: no extension,
// and no ".ia" left behind by an Internet Archive passthrough derivative.
func displayName(fileName string) string {
	name := strings.TrimSuffix(fileName, path.Ext(fileName))
	return strings.TrimSuffix(name, ".ia")
}

// bitrate derives bits per second from the file size and duration, because the
// metadata API does not report it. Returns 0 when either input is unusable,
// which the gate then treats as unknown rather than free.
func bitrate(size, length string) int {
	bytes, err := strconv.ParseFloat(size, 64)
	if err != nil || bytes <= 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(length, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int(bytes * 8 / seconds)
}

func seconds(length string) int {
	v, err := strconv.ParseFloat(length, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int(v)
}

func atoi(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// fetchMetadata performs the HTTP request. Any non-200 is an error rather than
// a partial result, so a caller never mistakes a failure for an empty item.
func fetchMetadata(ctx context.Context, client *http.Client, itemID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, MetadataEndpoint+url.PathEscape(itemID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach archive.org: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive.org returned %s for %s", resp.Status, itemID)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}
