package mode

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/mode/providers"
	"github.com/AutumnsGrove/nova/voice"
)

// Voice is the voice client set a one-shot command needs, built from the config
// without an agent server: no session, no tools, no extension host. `nova speak`
// and `nova transcribe` use it so they start instantly and still read the
// configured roles and the shared credential.
type Voice struct {
	speaker     voice.Speaker
	transcriber voice.Transcriber
	speech      []string
	transcribe  []string
}

// NewVoice loads the config for o and builds only the voice clients.
func NewVoice(o BuildOptions) (*Voice, error) {
	cwd, home := o.CWD, o.Home
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	cfg, warnings, err := loadConfig(o, cwd, home)
	if err != nil {
		return nil, err
	}
	if o.Stderr != nil {
		for _, w := range warnings {
			fmt.Fprintf(o.Stderr, "warning: %s\n", w)
		}
	}
	opt := providers.Options{
		Config: cfg,
		Home:   home,
		Getenv: os.Getenv,
		Store:  auth.NewFileStore(keyPath(home)),
	}
	return &Voice{
		speaker:     providers.BuildSpeaker(opt),
		transcriber: providers.BuildTranscriber(opt),
		speech:      slices.Sorted(maps.Keys(cfg.Voice.Speech.Roles)),
		transcribe:  slices.Sorted(maps.Keys(cfg.Voice.Transcribe.Roles)),
	}, nil
}

// Speak synthesizes one clip through the configured speech roles.
func (v *Voice) Speak(ctx context.Context, req voice.SpeechRequest) (*voice.Clip, error) {
	return v.speaker.Speak(ctx, req)
}

// Transcribe transcribes one clip through the configured transcription roles.
func (v *Voice) Transcribe(ctx context.Context, req voice.TranscribeRequest) (*voice.Transcript, error) {
	return v.transcriber.Transcribe(ctx, req)
}

// SpeechRoles lists the configured text-to-speech role names.
func (v *Voice) SpeechRoles() []string { return v.speech }

// TranscribeRoles lists the configured speech-to-text role names.
func (v *Voice) TranscribeRoles() []string { return v.transcribe }
