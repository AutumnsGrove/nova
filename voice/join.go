package voice

import (
	"errors"
	"fmt"

	"github.com/AutumnsGrove/nova/provider"
)

// Join concatenates clips from one voice into one clip, which is what makes a
// long answer possible without a model that accepts a long input. Every clip
// must share the format, and a PCM clip must share its sample layout; otherwise
// Join refuses rather than producing a file that plays at the wrong speed.
//
// MP3 frames and PCM samples are both self-contained, so a join is a byte
// concatenation; the caller wraps PCM with WAV once at the end. Usage is summed,
// the first clip's metadata is kept, and GenerationID is dropped because one
// joined clip has many generations.
func Join(clips ...Clip) (Clip, error) {
	if len(clips) == 0 {
		return Clip{}, errors.New("voice: join needs at least one clip")
	}
	out := clips[0]
	// Copy, so a caller cannot mutate the first clip through the joined one.
	out.Bytes = append([]byte(nil), clips[0].Bytes...)
	out.GenerationID = ""
	out.Usage = provider.Usage{}
	for i, clip := range clips {
		if clip.Format != out.Format {
			return Clip{}, fmt.Errorf("voice: join needs one format; clip %d is %q, clip 0 is %q", i, clip.Format, out.Format)
		}
		if out.Format == FormatPCM && !sameLayout(out, clip) {
			return Clip{}, fmt.Errorf("voice: join needs one pcm layout; clip %d is %d Hz/%d ch/%d bit, clip 0 is %d Hz/%d ch/%d bit",
				i, clip.SampleRate, clip.Channels, clip.BitsPerSample, out.SampleRate, out.Channels, out.BitsPerSample)
		}
		if i > 0 {
			out.Bytes = append(out.Bytes, clip.Bytes...)
		}
		out.Usage.Add(clip.Usage)
	}
	return out, nil
}

// sameLayout reports whether two PCM clips describe the same sample format.
func sameLayout(a, b Clip) bool {
	return a.SampleRate == b.SampleRate && a.Channels == b.Channels && a.BitsPerSample == b.BitsPerSample
}
