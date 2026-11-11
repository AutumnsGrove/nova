package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/testkit"
)

// voiceEnv points the CLI at one scripted server, with a throwaway home and a
// key in the environment.
func voiceEnv(t *testing.T, srv *testkit.VoiceServer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "sk-test-value-123")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	// Costing is off by default here: these tests script one turn per
	// synthesis, and the catalog read the estimate makes would consume one.
	writeConfig(t, home, "voice:\n  cost_lookup: false\n")
}

// writeConfig writes a user config under home.
func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "nova")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeVoiceConfig writes a user config with one speech and one transcription
// role, so a test can check that flags win over roles.
func writeVoiceConfig(t *testing.T, home string) {
	t.Helper()
	body := `voice:
  cost_lookup: false
  speech:
    roles:
      narrate:
        model: vendor/role-tts
        voice: role-voice
        format: mp3
  transcribe:
    roles:
      default:
        model: vendor/role-stt
`
	writeConfig(t, home, body)
}

// runSpeak runs one speak command with args and returns its output.
func runSpeak(t *testing.T, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	cmd := speakCommand(strings.NewReader(""), stdout, stderr)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd.Execute()
}

func TestSpeak_A27_WritesAFile(t *testing.T) {
	audio := []byte{0xff, 0xf3, 0x01, 0x02}
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: audio, ContentType: "audio/mpeg"})
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")

	var stdout, stderr bytes.Buffer
	err := runSpeak(t, &stdout, &stderr, "hello", "--model", "hexgrad/kokoro-82m", "--voice", "af_heart", "-o", out)
	if err != nil {
		t.Fatalf("speak: %v", err)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if !bytes.Equal(written, audio) {
		t.Errorf("file = %v, want %v", written, audio)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/audio/speech" {
		t.Fatalf("requests = %+v", reqs)
	}
	var body struct {
		Model string `json:"model"`
		Voice string `json:"voice"`
		Input string `json:"input"`
	}
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.Model != "hexgrad/kokoro-82m" || body.Voice != "af_heart" || body.Input != "hello" {
		t.Errorf("body = %+v", body)
	}
	if !strings.Contains(stderr.String(), "wrote") {
		t.Errorf("stderr = %q, want a note about the file", stderr.String())
	}
}

func TestSpeak_A27_FlagsWinOverTheRole(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	home := t.TempDir()
	voiceEnv(t, srv)
	writeVoiceConfig(t, home)
	t.Setenv("HOME", home)
	out := filepath.Join(t.TempDir(), "out.mp3")

	var stdout, stderr bytes.Buffer
	if err := runSpeak(t, &stdout, &stderr, "hi", "--role", "narrate", "--model", "other/tts", "--voice", "other", "-o", out); err != nil {
		t.Fatalf("speak: %v", err)
	}
	var body struct {
		Model string `json:"model"`
		Voice string `json:"voice"`
	}
	if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.Model != "other/tts" || body.Voice != "other" {
		t.Errorf("body = %+v, want the flags to win over the role", body)
	}
}

func TestSpeak_A27_RoleFromConfig(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	home := t.TempDir()
	voiceEnv(t, srv)
	writeVoiceConfig(t, home)
	t.Setenv("HOME", home)

	var stdout, stderr bytes.Buffer
	if err := runSpeak(t, &stdout, &stderr, "hi", "--role", "narrate", "-o", filepath.Join(t.TempDir(), "a.mp3")); err != nil {
		t.Fatalf("speak: %v", err)
	}
	var body struct {
		Model string `json:"model"`
		Voice string `json:"voice"`
	}
	if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.Model != "vendor/role-tts" || body.Voice != "role-voice" {
		t.Errorf("body = %+v, want the configured role", body)
	}
}

func TestSpeak_A27_NoModelExplainsTheRoles(t *testing.T) {
	srv := testkit.NewVoiceServer(t)
	home := t.TempDir()
	voiceEnv(t, srv)
	writeVoiceConfig(t, home)
	t.Setenv("HOME", home)

	var stdout, stderr bytes.Buffer
	err := runSpeak(t, &stdout, &stderr, "hi", "-o", filepath.Join(t.TempDir(), "a.mp3"))
	if err == nil {
		t.Fatal("speak with no model = nil, want an error naming the roles")
	}
	if !strings.Contains(err.Error(), "narrate") {
		t.Errorf("error = %q, want it to name the configured role", err)
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestSpeak_A28_ChunksAndJoins(t *testing.T) {
	srv := testkit.NewVoiceServer(t,
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Audio: []byte{2}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Audio: []byte{3}, ContentType: "audio/mpeg"},
	)
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")

	var stdout, stderr bytes.Buffer
	if err := runSpeak(t, &stdout, &stderr, "One. Two. Three.", "--model", "vendor/tts", "--voice", "v", "--chunk-size", "6", "-o", out); err != nil {
		t.Fatalf("speak: %v", err)
	}
	if got := len(srv.Requests()); got != 3 {
		t.Fatalf("requests = %d, want one per chunk", got)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if !bytes.Equal(written, []byte{1, 2, 3}) {
		t.Errorf("file = %v, want the joined clips", written)
	}
	if !strings.Contains(stderr.String(), "3 chunk") {
		t.Errorf("stderr = %q, want the chunk count", stderr.String())
	}
}

func TestSpeak_A28_NoFileWhenAChunkFails(t *testing.T) {
	srv := testkit.NewVoiceServer(t,
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Status: 500, Body: `{"error":{"message":"boom"}}`},
		testkit.VoiceServerTurn{Audio: []byte{3}, ContentType: "audio/mpeg"},
	)
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")

	var stdout, stderr bytes.Buffer
	err := runSpeak(t, &stdout, &stderr, "One. Two. Three.", "--model", "vendor/tts", "--voice", "v", "--chunk-size", "6", "-o", out)
	if err == nil {
		t.Fatal("speak with a failing chunk = nil, want an error")
	}
	if !strings.Contains(err.Error(), "chunk 2 of 3") {
		t.Errorf("error = %q, want it to name the chunk", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Error("a partial chunk failure still wrote a file")
	}
}

func TestSpeak_A27_RefusesACharDevice(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	voiceEnv(t, srv)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	cmd := speakCommand(strings.NewReader(""), devNull, &bytes.Buffer{})
	cmd.SetArgs([]string{"hi", "--model", "vendor/tts", "--voice", "v"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("speak to a char device = %v, want a refusal", err)
	}
}

func TestSpeak_A27_JSONSummary(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{
		Audio: []byte{1, 2}, ContentType: "audio/mpeg",
		Headers: map[string]string{"X-Generation-Id": "gen-1"},
	})
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")

	var stdout, stderr bytes.Buffer
	if err := runSpeak(t, &stdout, &stderr, "hi", "--model", "vendor/tts", "--voice", "v", "-o", out, "--json"); err != nil {
		t.Fatalf("speak: %v", err)
	}
	var summary struct {
		File   string `json:"file"`
		Bytes  int    `json:"bytes"`
		Chunks int    `json:"chunks"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, stdout.String())
	}
	if summary.File != out || summary.Bytes != 2 || summary.Chunks != 1 || summary.Format != "mp3" {
		t.Errorf("summary = %+v", summary)
	}
}

func TestSpeak_A27_JSONAndStdoutConflict(t *testing.T) {
	srv := testkit.NewVoiceServer(t)
	voiceEnv(t, srv)
	if err := runSpeak(t, &bytes.Buffer{}, &bytes.Buffer{}, "hi", "--model", "vendor/tts", "--voice", "v", "--json"); err == nil {
		t.Fatal("speak --json with -o - = nil, want a conflict error")
	}
}

func TestSpeak_A27_Stdin(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")

	var stdout, stderr bytes.Buffer
	cmd := speakCommand(strings.NewReader("from stdin"), &stdout, &stderr)
	cmd.SetArgs([]string{"--model", "vendor/tts", "--voice", "v", "-o", out})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("speak: %v", err)
	}
	var body struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.Input != "from stdin" {
		t.Errorf("input = %q, want the piped text", body.Input)
	}
}

// runTranscribe runs one transcribe command with args.
func runTranscribe(t *testing.T, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	cmd := transcribeCommand(strings.NewReader(""), stdout, stderr)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd.Execute()
}

func TestTranscribe_A29_PrintsTheText(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"  hello there  "}`})
	voiceEnv(t, srv)
	path := filepath.Join(t.TempDir(), "memo.webm")
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runTranscribe(t, &stdout, &stderr, path, "--model", "vendor/stt"); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hello there" {
		t.Errorf("stdout = %q, want the transcript", got)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/audio/transcriptions" {
		t.Fatalf("requests = %+v", reqs)
	}
	if !strings.Contains(string(reqs[0].Body), "clip.webm") {
		t.Errorf("the file part does not name clip.webm: %q", reqs[0].Body)
	}
}

func TestTranscribe_A29_JSON(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"hi","usage":{"seconds":1,"cost":0.0001}}`})
	voiceEnv(t, srv)
	path := filepath.Join(t.TempDir(), "memo.mp3")
	if err := os.WriteFile(path, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runTranscribe(t, &stdout, &stderr, path, "--model", "vendor/stt", "--json"); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	var transcript struct {
		Text  string `json:"text"`
		Usage struct {
			CostUSD float64 `json:"cost_usd"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &transcript); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, stdout.String())
	}
	if transcript.Text != "hi" || transcript.Usage.CostUSD != 0.0001 {
		t.Errorf("transcript = %+v", transcript)
	}
}

func TestTranscribe_A29_StdinNeedsAFormat(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"hi"}`})
	voiceEnv(t, srv)

	var stdout, stderr bytes.Buffer
	cmd := transcribeCommand(strings.NewReader("audio bytes"), &stdout, &stderr)
	cmd.SetArgs([]string{"-", "--model", "vendor/stt"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--format") {
		t.Fatalf("transcribe - without a format = %v, want an error naming --format", err)
	}
}

func TestTranscribe_A29_Stdin(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"hi"}`})
	voiceEnv(t, srv)

	var stdout, stderr bytes.Buffer
	cmd := transcribeCommand(strings.NewReader("audio bytes"), &stdout, &stderr)
	cmd.SetArgs([]string{"-", "--model", "vendor/stt", "--format", "mp3"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hi" {
		t.Errorf("stdout = %q", got)
	}
}

// TestSpeak_A28_LongInputChunks: the guard is per request, not per command, so
// an input longer than voice.MaxSpeechRunes is fine when it is chunked.
func TestSpeak_A28_LongInputChunks(t *testing.T) {
	srv := testkit.NewVoiceServer(t,
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Audio: []byte{2}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Audio: []byte{3}, ContentType: "audio/mpeg"},
	)
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")
	text := strings.Repeat("a", 2500)

	var stdout, stderr bytes.Buffer
	if err := runSpeak(t, &stdout, &stderr, text, "--model", "vendor/tts", "--voice", "v", "--chunk-size", "1000", "-o", out); err != nil {
		t.Fatalf("speak: %v", err)
	}
	if got := len(srv.Requests()); got != 3 {
		t.Fatalf("requests = %d, want 3 chunks", got)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if !bytes.Equal(written, []byte{1, 2, 3}) {
		t.Errorf("file = %v", written)
	}
}

// TestSpeak_A28_NoChunkRefusesOverTheGuard: --no-chunk leaves the whole input
// in one request, so the library's guard refuses it before the wire.
func TestSpeak_A28_NoChunkRefusesOverTheGuard(t *testing.T) {
	srv := testkit.NewVoiceServer(t)
	voiceEnv(t, srv)
	out := filepath.Join(t.TempDir(), "out.mp3")
	text := strings.Repeat("a", 32_001)

	var stdout, stderr bytes.Buffer
	err := runSpeak(t, &stdout, &stderr, text, "--model", "vendor/tts", "--voice", "v", "--no-chunk", "-o", out)
	if err == nil {
		t.Fatal("speak --no-chunk over the guard = nil, want an error")
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}
