package voice

import (
	"context"
	"errors"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/roles"
)

// VoiceRole maps an application role to a model and the voice it speaks with.
//
// The voice belongs to the model: "alloy" is an OpenAI voice and returns 400 on
// Voxtral or Kokoro. A fallback therefore restates its own model and voice,
// because it cannot inherit the primary's. This is the audio analogue of
// embed's dimension check: the thing that cannot survive a fallback is what the
// fallback must declare.
type VoiceRole struct {
	Model string
	Voice string
	// Format is "mp3" or "pcm"; empty leaves it to the request or the adapter.
	Format Format
	// Speed is a playback multiplier; nil leaves it to the request.
	Speed        *float64
	Instructions string
	// Timeout is per attempt; zero means only the caller's context bounds it.
	Timeout  time.Duration
	Fallback *VoiceAttempt
}

// VoiceAttempt is one fallback: a model with its own voice and bounds.
type VoiceAttempt struct {
	Model  string
	Voice  string
	Format Format
	Speed  *float64
}

// TranscribeRole maps an application role to a transcription model and its
// bounds. A transcription model has no voice, so the shared resolver's shape
// fits it exactly.
type TranscribeRole struct {
	Model    string
	Fallback string
	// Timeout is per attempt; zero means only the caller's context bounds it.
	Timeout time.Duration
}

// RolesConfig builds a Roles decorator. A caller that only synthesizes may leave
// Transcriber nil, and the other way round; the unused direction reports it when
// it is called.
type RolesConfig struct {
	Speaker     Speaker
	Transcriber Transcriber
	// Speech maps an application role name to its model, voice and bounds.
	Speech map[string]VoiceRole
	// Transcribe maps an application role name to its model and bounds.
	Transcribe map[string]TranscribeRole
	// Clock drives a role's timeout; nil means the wall clock.
	Clock roles.Clock
	// AllowAliases permits ids that end in "-latest" or start with "~".
	AllowAliases bool
}

// Roles resolves a role name to a model, fills the voice the role declares, and
// runs one attempt per model under the role's timeout. A request whose Model
// contains a slash is a direct catalog id and passes through.
type Roles struct {
	speaker         Speaker
	transcriber     Transcriber
	speech          map[string]VoiceRole
	speechTable     *roles.Table
	transcribeTable *roles.Table
}

var (
	_ Speaker     = (*Roles)(nil)
	_ Transcriber = (*Roles)(nil)
)

// NewRoles builds the decorator. It does not validate the table, because a bad
// role is a configuration error the caller should see when it is used.
func NewRoles(cfg RolesConfig) *Roles {
	speech := make(map[string]VoiceRole, len(cfg.Speech))
	speechRoles := make(map[string]roles.Role, len(cfg.Speech))
	for name, role := range cfg.Speech {
		speech[name] = role
		entry := roles.Role{Model: role.Model, Timeout: role.Timeout}
		if role.Fallback != nil {
			entry.Fallback = role.Fallback.Model
		}
		speechRoles[name] = entry
	}
	transcribeRoles := make(map[string]roles.Role, len(cfg.Transcribe))
	for name, role := range cfg.Transcribe {
		transcribeRoles[name] = roles.Role{Model: role.Model, Fallback: role.Fallback, Timeout: role.Timeout}
	}
	return &Roles{
		speaker:     cfg.Speaker,
		transcriber: cfg.Transcriber,
		speech:      speech,
		speechTable: roles.New(roles.Config{
			Roles: speechRoles, Clock: cfg.Clock, AllowAliases: cfg.AllowAliases, Subsystem: "voice",
		}),
		transcribeTable: roles.New(roles.Config{
			Roles: transcribeRoles, Clock: cfg.Clock, AllowAliases: cfg.AllowAliases, Subsystem: "voice",
		}),
	}
}

// Speak resolves req.Model and calls the speaker with the role's voice and
// bounds. Every attempt is validated before the first request, so a bad fallback
// fails fast instead of after the primary has already been paid for.
func (r *Roles) Speak(ctx context.Context, req SpeechRequest) (*Clip, error) {
	if r.speaker == nil {
		return nil, errors.New("voice: no speaker configured")
	}
	attempts, timeout, err := r.speechAttempts(req)
	if err != nil {
		return nil, err
	}
	var answer *Clip
	var firstErr error
	var usage provider.Usage
	for _, attempt := range attempts {
		request := attempt
		u, err := r.speechTable.Attempt(ctx, request.Model, timeout, func(ctx context.Context, _ string) (provider.Usage, error) {
			clip, err := r.speaker.Speak(ctx, request)
			if clip == nil {
				if err == nil {
					err = errors.New("voice: speaker returned no clip")
				}
				return provider.Usage{}, err
			}
			answer = clip
			return clip.Usage, err
		})
		usage.Add(u)
		if err == nil {
			out := *answer
			out.Usage = usage
			return &out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		// A cancelled caller is not an availability problem; do not spend a
		// second request on it.
		if ctx.Err() != nil {
			break
		}
	}
	if firstErr == nil {
		firstErr = errors.New("voice: no model resolved")
	}
	return &Clip{Usage: usage}, firstErr
}

// speechAttempts resolves the request's model chain and builds one validated
// request per attempt, filling the role's voice, format, speed and instructions.
func (r *Roles) speechAttempts(req SpeechRequest) ([]SpeechRequest, time.Duration, error) {
	chain, err := r.speechTable.Resolve(req.Model)
	if err != nil {
		return nil, 0, err
	}
	role, named := r.speech[req.Model]
	attempts := make([]SpeechRequest, 0, len(chain.Models))
	for i, model := range chain.Models {
		attempt := req
		attempt.Model = model
		switch {
		case i == 0 && named:
			attempt.Voice = firstNonEmpty(attempt.Voice, role.Voice)
			attempt.Format = firstFormat(attempt.Format, role.Format)
			attempt.Speed = firstSpeed(attempt.Speed, role.Speed)
			attempt.Instructions = firstNonEmpty(attempt.Instructions, role.Instructions)
		case i > 0 && named && role.Fallback != nil:
			attempt.Voice = role.Fallback.Voice
			attempt.Format = firstFormat(role.Fallback.Format, attempt.Format)
			attempt.Speed = firstSpeed(role.Fallback.Speed, attempt.Speed)
		}
		if err := attempt.Validate(); err != nil {
			return nil, 0, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, chain.Timeout, nil
}

// Transcribe resolves req.Model and calls the transcriber with the role's
// bounds, summing the usage of every attempt.
func (r *Roles) Transcribe(ctx context.Context, req TranscribeRequest) (*Transcript, error) {
	if r.transcriber == nil {
		return nil, errors.New("voice: no transcriber configured")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	chain, err := r.transcribeTable.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	var answer *Transcript
	var firstErr error
	var usage provider.Usage
	for _, model := range chain.Models {
		attempt := req
		attempt.Model = model
		u, err := r.transcribeTable.Attempt(ctx, model, chain.Timeout, func(ctx context.Context, _ string) (provider.Usage, error) {
			transcript, err := r.transcriber.Transcribe(ctx, attempt)
			if transcript == nil {
				if err == nil {
					err = errors.New("voice: transcriber returned no transcript")
				}
				return provider.Usage{}, err
			}
			answer = transcript
			return transcript.Usage, err
		})
		usage.Add(u)
		if err == nil {
			out := *answer
			out.Usage = usage
			return &out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	if firstErr == nil {
		firstErr = errors.New("voice: no model resolved")
	}
	return &Transcript{Usage: usage}, firstErr
}

// firstNonEmpty returns a when it is set, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// firstFormat returns a when it is set, otherwise b.
func firstFormat(a, b Format) Format {
	if a != "" {
		return a
	}
	return b
}

// firstSpeed returns a when it is set, otherwise b.
func firstSpeed(a, b *float64) *float64 {
	if a != nil {
		return a
	}
	return b
}
