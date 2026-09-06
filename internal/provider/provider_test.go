package provider

import "testing"

func TestStreamPlayable(t *testing.T) {
	tests := []struct {
		name   string
		stream *Stream
		want   bool
	}{
		{
			name:   "nil stream is not playable",
			stream: nil,
			want:   false,
		},
		{
			name:   "typical Internet Archive derivative",
			stream: &Stream{Kind: Progressive, Codec: H264, Height: 480, Bitrate: 830_000},
			want:   true,
		},
		{
			name:   "at both limits exactly",
			stream: &Stream{Kind: Progressive, Codec: H264, Height: MaxHeight, Bitrate: MaxBitrate},
			want:   true,
		},
		{
			name:   "1080p original is too tall",
			stream: &Stream{Kind: Progressive, Codec: H264, Height: 1080, Bitrate: 900_000},
			want:   false,
		},
		{
			name:   "4.2 Mbps original is too heavy",
			stream: &Stream{Kind: Progressive, Codec: H264, Height: 480, Bitrate: 4_200_000},
			want:   false,
		},
		{
			name:   "HLS is rejected regardless of size",
			stream: &Stream{Kind: HLS, Codec: H264, Height: 360, Bitrate: 500_000},
			want:   false,
		},
		{
			name:   "Theora derivative is rejected even though it fits",
			stream: &Stream{Kind: Progressive, Codec: Theora, Height: 300, Bitrate: 590_000},
			want:   false,
		},
		{
			name:   "unrecognised codec is rejected",
			stream: &Stream{Kind: Progressive, Codec: CodecUnknown, Height: 240, Bitrate: 400_000},
			want:   false,
		},
		{
			name:   "unknown dimensions cannot be cleared",
			stream: &Stream{Kind: Progressive, Codec: H264, Height: 0, Bitrate: 0},
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := tc.stream.Playable()
			if got != tc.want {
				t.Fatalf("Playable() = %v, want %v (reason %q)", got, tc.want, reason)
			}
			if !got && reason == "" {
				t.Fatal("unplayable stream must explain why")
			}
			if got && reason != "" {
				t.Fatalf("playable stream should have no reason, got %q", reason)
			}
		})
	}
}
