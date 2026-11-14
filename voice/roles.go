package voice

import (
	"context"
	"errors"
	"fmt"
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
	// Provider pins the endpoint for this attempt. It is not inherited by the
	// fallback for the same reason the voice is not: it names the endpoints of
	// one model, and the fallback is another model.
	Provider map[string]any
	// Endpoint names the client that serves this role. Empty means the default
	// client, which is what every config without a voice endpoint gets.
	Endpoint string
	// Timeout is per attempt; zero means only the caller's context bounds it.
	Timeout  time.Duration
	Fallback *VoiceAttempt
}

// VoiceAttempt is one fallback: a model with its own voice and bounds.
type VoiceAttempt struct {
	Model    string
	Voice    string
	Format   Format
	Speed    *float64
	Provider map[string]any
	// Endpoint is restated for the same reason the voice is: it names the
	// client of one model, and the fallback is another model that may live on
	// another engine entirely.
	Endpoint string
}

// TranscribeRole maps an application role to a transcription model and its
// bounds. A transcription model has no voice, so the shared resolver's shape
// fits it exactly.
type TranscribeRole struct {
	Model    string
	Fallback string
	// Endpoint names the client that serves this role, as for speech.
	Endpoint string
	// Timeout is per attempt; zero means only the caller's context bounds it.
	Timeout time.Duration
}

// RolesConfig builds a Roles decorator. A caller that only synthesizes may leave
// Transcriber nil, and the other way round; the unused direction reports it when
// it is called.
type RolesConfig struct {
	Speaker     Speaker
	Transcriber Transcriber
	// Speakers names an endpoint per client. A role whose Endpoint is empty
	// uses Speaker, so a config that names none behaves as it always did.
	Speakers map[string]Speaker
	// Transcribers names an endpoint per client, as Speakers does.
	Transcribers map[string]Transcriber
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
	speakers        map[string]Speaker
	transcribers    map[string]Transcriber
	speech          map[string]VoiceRole
	transcribe      map[string]TranscribeRole
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
	transcribe := make(map[string]TranscribeRole, len(cfg.Transcribe))
	transcribeRoles := make(map[string]roles.Role, len(cfg.Transcribe))
	for name, role := range cfg.Transcribe {
		transcribe[name] = role
		transcribeRoles[name] = roles.Role{Model: role.Model, Fallback: role.Fallback, Timeout: role.Timeout}
	}
	return &Roles{
		speaker:      cfg.Speaker,
		transcriber:  cfg.Transcriber,
		speakers:     cfg.Speakers,
		transcribers: cfg.Transcribers,
		speech:       speech,
		transcribe:   transcribe,
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
	if r.speaker == nil && len(r.speakers) == 0 {
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
		request, speaker := attempt.request, attempt.speaker
		u, err := r.speechTable.Attempt(ctx, request.Model, timeout, func(ctx context.Context, _ string) (provider.Usage, error) {
			clip, err := speaker.Speak(ctx, request)
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

// speechAttempt is one model in a role's chain with the client that serves it.
// The endpoint is a property of the attempt rather than the role, because a
// fallback may live on another engine: a local summariser falling back to a
// hosted one is the case that makes it worth carrying.
type speechAttempt struct {
	request SpeechRequest
	speaker Speaker
}

// speechAttempts resolves the request's model chain and builds one validated
// request per attempt, filling the role's voice, format, speed and instructions.
// It resolves every attempt's client here, so an unknown endpoint name is
// refused before any request is made.
func (r *Roles) speechAttempts(req SpeechRequest) ([]speechAttempt, time.Duration, error) {
	chain, err := r.speechTable.Resolve(req.Model)
	if err != nil {
		return nil, 0, err
	}
	role, named := r.speech[req.Model]
	attempts := make([]speechAttempt, 0, len(chain.Models))
	for i, model := range chain.Models {
		attempt := req
		attempt.Model = model
		endpoint := ""
		switch {
		case i == 0 && named:
			attempt.Voice = firstNonEmpty(attempt.Voice, role.Voice)
			attempt.Format = firstFormat(attempt.Format, role.Format)
			attempt.Speed = firstSpeed(attempt.Speed, role.Speed)
			attempt.Instructions = firstNonEmpty(attempt.Instructions, role.Instructions)
			attempt.Provider = firstProvider(attempt.Provider, role.Provider)
			endpoint = role.Endpoint
		case i > 0 && named && role.Fallback != nil:
			attempt.Voice = role.Fallback.Voice
			attempt.Format = firstFormat(role.Fallback.Format, attempt.Format)
			attempt.Speed = firstSpeed(role.Fallback.Speed, attempt.Speed)
			// Replace rather than merge: a pin names one model's endpoints, so a
			// primary's pin that leaked into a fallback would be routed against a
			// model it never named. A fallback without a pin means no pin.
			attempt.Provider = role.Fallback.Provider
			endpoint = role.Fallback.Endpoint
		}
		if err := attempt.Validate(); err != nil {
			return nil, 0, err
		}
		speaker, err := r.speakerFor(endpoint)
		if err != nil {
			return nil, 0, err
		}
		attempts = append(attempts, speechAttempt{request: attempt, speaker: speaker})
	}
	return attempts, chain.Timeout, nil
}

// speakerFor returns the client a role's attempt names. An empty name is the
// default client, which is what every config without a voice endpoint uses.
func (r *Roles) speakerFor(endpoint string) (Speaker, error) {
	if endpoint == "" {
		if r.speaker == nil {
			return nil, errors.New("voice: no speaker configured")
		}
		return r.speaker, nil
	}
	if speaker, ok := r.speakers[endpoint]; ok && speaker != nil {
		return speaker, nil
	}
	return nil, fmt.Errorf("voice: endpoint %q is not configured", endpoint)
}

// transcriberFor is speakerFor for the transcription direction.
func (r *Roles) transcriberFor(endpoint string) (Transcriber, error) {
	if endpoint == "" {
		if r.transcriber == nil {
			return nil, errors.New("voice: no transcriber configured")
		}
		return r.transcriber, nil
	}
	if transcriber, ok := r.transcribers[endpoint]; ok && transcriber != nil {
		return transcriber, nil
	}
	return nil, fmt.Errorf("voice: endpoint %q is not configured", endpoint)
}

// Transcribe resolves req.Model and calls the transcriber with the role's
// bounds, summing the usage of every attempt.
func (r *Roles) Transcribe(ctx context.Context, req TranscribeRequest) (*Transcript, error) {
	if r.transcriber == nil && len(r.transcribers) == 0 {
		return nil, errors.New("voice: no transcriber configured")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	chain, err := r.transcribeTable.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	// A transcription fallback is another model, not another role, so the
	// endpoint is the role's and covers both attempts.
	transcriber, err := r.transcriberFor(r.transcribe[req.Model].Endpoint)
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
			transcript, err := transcriber.Transcribe(ctx, attempt)
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

// firstProvider returns a when it is set, otherwise b.
func firstProvider(a, b map[string]any) map[string]any {
	if len(a) > 0 {
		return a
	}
	return b
}
