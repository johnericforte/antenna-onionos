// Package provider defines the source abstraction and the device playability
// gate that every source is filtered through.
//
// The gate deliberately sits below the interface: whether a stream can play is
// a property of this hardware, not of where the stream came from. A source
// cannot opt out of it.
package provider

import (
	"context"
	"fmt"
)

// Kind describes how a stream is delivered.
type Kind int

const (
	// Progressive is a single file fetched over HTTP with Range support.
	Progressive Kind = iota
	// HLS is a segmented playlist. Not currently playable on this device.
	HLS
)

func (k Kind) String() string {
	switch k {
	case Progressive:
		return "progressive"
	case HLS:
		return "HLS"
	default:
		return "unknown"
	}
}

// Codec is the video codec inside the container.
type Codec int

const (
	// CodecUnknown is a stream whose codec could not be established. It is
	// never offered: an unplayable file that hangs the player is worse than a
	// title that quietly does not appear.
	CodecUnknown Codec = iota
	// H264 is the only codec this device decodes at a watchable frame rate.
	H264
	// Theora arrives on older Internet Archive items as .ogv derivatives.
	// Unconfirmed on this hardware, so not offered.
	Theora
)

func (c Codec) String() string {
	switch c {
	case H264:
		return "H.264"
	case Theora:
		return "Theora"
	default:
		return "Unrecognised"
	}
}

// SourceKind is how a configured source is read. The shape of the line in the
// item list decides it, so a user adds a source of any kind by writing one
// line rather than by choosing a provider.
type SourceKind int

const (
	// ArchiveItem is an archive.org identifier. Its titles come from the
	// metadata API, and the app knows their size and bitrate before playing.
	ArchiveItem SourceKind = iota
	// DirectVideo is a URL to one video file. Nothing describes it ahead of
	// time, so the app plays what it is pointed at.
	DirectVideo
	// PlaylistFile is a URL to an m3u or a plain list of video URLs.
	PlaylistFile
)

func (k SourceKind) String() string {
	switch k {
	case ArchiveItem:
		return "archive item"
	case DirectVideo:
		return "direct video"
	case PlaylistFile:
		return "playlist"
	default:
		return "unknown source"
	}
}

// Item is one configured source. Ref is an archive.org identifier or a URL,
// depending on Kind.
type Item struct {
	Kind  SourceKind
	Ref   string
	Title string
}

// Entry is one browsable item: a folder to descend into or a playable title.
type Entry struct {
	ID       string
	Title    string
	IsFolder bool
	Duration int // seconds; 0 when unknown
}

// Stream is a resolved, playable location for one entry.
type Stream struct {
	Kind    Kind
	URL     string
	Headers map[string]string
	Width   int
	Height  int
	Bitrate int // bits per second
	Codec   Codec

	// Unverified marks a stream nothing described in advance, which is every
	// stream that came from a URL the user wrote down themselves. The size
	// gate cannot judge what it cannot measure, and refusing a video because
	// the app failed to learn its dimensions would make user supplied sources
	// useless. The device limits still apply physically: an oversized video
	// stutters rather than being refused.
	Unverified bool
}

// Provider is a browsable source of video.
type Provider interface {
	Name() string
	Browse(ctx context.Context, path string) ([]Entry, error)
	Resolve(ctx context.Context, id string) (*Stream, error)
}

// Device decode limits. The Miyoo Mini Plus decodes H.264 in software on two
// Cortex-A7 cores, so these are conservative on purpose. Tune here and nowhere
// else.
const (
	MaxHeight  = 480
	MaxBitrate = 1_000_000
)

// Playable reports whether the device can decode s in real time, and why not
// when it cannot. The reason is written for the user, not the log.
func (s *Stream) Playable() (bool, string) {
	if s == nil {
		return false, "No stream found"
	}
	if s.Kind != Progressive {
		return false, fmt.Sprintf("%s streams are not supported", s.Kind)
	}
	if s.Codec != H264 {
		return false, fmt.Sprintf("%s video is not supported", s.Codec)
	}
	// A user supplied URL carries no description, so there is nothing to
	// check. Play it and let the device be the judge.
	if s.Unverified {
		return true, ""
	}
	if s.Height == 0 || s.Bitrate == 0 {
		return false, "Video size is unknown, so it cannot be checked"
	}
	if s.Height > MaxHeight {
		return false, fmt.Sprintf("Video is %dp; this device tops out at %dp", s.Height, MaxHeight)
	}
	if s.Bitrate > MaxBitrate {
		return false, fmt.Sprintf("Bitrate is %d kbps; limit is %d kbps",
			s.Bitrate/1000, MaxBitrate/1000)
	}
	return true, ""
}
