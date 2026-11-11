package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_VoiceRoles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
voice:
  allow_aliases: true
  cost_lookup: false
  speech:
    roles:
      narrate:
        model: openai/gpt-4o-mini-tts-2025-12-15
        voice: alloy
        format: mp3
        speed: 1.1
        instructions: calm
        timeout_ms: 60000
        fallback:
          model: mistralai/voxtral-mini-tts-2603
          voice: en_paul_neutral
          format: mp3
  transcribe:
    roles:
      default:
        model: openai/whisper-1
        fallback: mistralai/voxtral-small-transcribe-3-0
        timeout_ms: 60000
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if !cfg.Voice.AllowAliases {
		t.Error("allow_aliases was not read")
	}
	if cfg.Voice.CostLookupEnabled() {
		t.Error("cost_lookup: false was not read")
	}
	speech, ok := cfg.Voice.Speech.Roles["narrate"]
	if !ok {
		t.Fatalf("speech roles = %v, want narrate", cfg.Voice.Speech.Roles)
	}
	if speech.Model != "openai/gpt-4o-mini-tts-2025-12-15" || speech.Voice != "alloy" || speech.Format != "mp3" || speech.Instructions != "calm" || speech.TimeoutMS != 60000 {
		t.Errorf("speech role = %+v", speech)
	}
	if speech.Speed == nil || *speech.Speed != 1.1 {
		t.Errorf("speed = %v, want 1.1", speech.Speed)
	}
	if speech.Fallback == nil || speech.Fallback.Model != "mistralai/voxtral-mini-tts-2603" || speech.Fallback.Voice != "en_paul_neutral" {
		t.Errorf("fallback = %+v, want its own model and voice", speech.Fallback)
	}
	transcribe, ok := cfg.Voice.Transcribe.Roles["default"]
	if !ok {
		t.Fatalf("transcribe roles = %v, want default", cfg.Voice.Transcribe.Roles)
	}
	if transcribe.Model != "openai/whisper-1" || transcribe.Fallback != "mistralai/voxtral-small-transcribe-3-0" || transcribe.TimeoutMS != 60000 {
		t.Errorf("transcribe role = %+v", transcribe)
	}
}

func TestLoad_VoiceRoleProvider(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
voice:
  speech:
    roles:
      narrate:
        model: hexgrad/kokoro-82m
        voice: af_heart
        provider:
          only: [Together]
        fallback:
          model: fallback/tts
          voice: two
          provider:
            only: [DeepInfra]
      plain:
        model: hexgrad/kokoro-82m
        voice: af_heart
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	role := cfg.Voice.Speech.Roles["narrate"]
	if only := role.Provider["only"]; only == nil {
		t.Errorf("provider = %v, want only: [Together] verbatim", role.Provider)
	}
	if role.Fallback == nil || role.Fallback.Provider["only"] == nil {
		t.Errorf("fallback = %+v, want its own provider", role.Fallback)
	}
	if role := cfg.Voice.Speech.Roles["plain"]; role.Provider != nil {
		t.Errorf("provider = %v, want nil for a role without one", role.Provider)
	}
}

func TestLoad_VoiceCostLookupDefaultsOn(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if !cfg.Voice.CostLookupEnabled() {
		t.Error("an unset cost_lookup should default to on for the front doors")
	}
}

func TestLoad_VoiceWarnsOnBadRoles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
voice:
  speech:
    roles:
      nomodel:
        voice: alloy
        timeout_ms: -5
        fallback:
          voice: other
  transcribe:
    roles:
      quiet:
        timeout_ms: -1
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if got := cfg.Voice.Speech.Roles["nomodel"]; got.TimeoutMS != 0 {
		t.Errorf("timeout_ms = %d, want a negative value clamped to 0", got.TimeoutMS)
	}
	if got := cfg.Voice.Transcribe.Roles["quiet"]; got.TimeoutMS != 0 {
		t.Errorf("transcribe timeout_ms = %d, want 0", got.TimeoutMS)
	}
	text := warnText(warnings)
	for _, want := range []string{
		"negative voice.speech.roles.nomodel.timeout_ms",
		"negative voice.transcribe.roles.quiet.timeout_ms",
		`voice speech role "nomodel" has no model`,
		`voice speech role "nomodel" fallback has no model`,
		`voice transcription role "quiet" has no model`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings = %s, want %q", text, want)
		}
	}
}

func TestDefaults_VoiceExtensionCaps(t *testing.T) {
	cfg := Defaults().Extensions
	if cfg.SpeechMaxPerRun != 20 || cfg.TranscribeMaxPerRun != 20 {
		t.Errorf("default voice caps = %d/%d, want 20/20", cfg.SpeechMaxPerRun, cfg.TranscribeMaxPerRun)
	}
}

func TestLoad_VoiceExtensionCaps(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "extensions:\n  speech_max_per_run: -1\n  transcribe_max_per_run: 5\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if got := cfg.Extensions.SpeechMaxPerRun; got != 0 {
		t.Errorf("speech_max_per_run = %d, want 0", got)
	}
	if got := cfg.Extensions.TranscribeMaxPerRun; got != 5 {
		t.Errorf("transcribe_max_per_run = %d, want 5", got)
	}
	if text := warnText(warnings); !strings.Contains(text, "extensions.speech_max_per_run") {
		t.Errorf("warnings = %s, want a negative-cap warning", text)
	}
}
