package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/voice"
)

// TestVoice_A26_ToolReachesTheVoiceSeams: Config.Speak and Config.Transcribe
// reach a tool as tool.Context fields, and a tool's AddUsage counts them in the
// run's totals, exactly as Complete, Decide and Embed do.
func TestVoice_A26_ToolReachesTheVoiceSeams(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, namedToolTurn("speak_and_transcribe"), textTurn("done"))
	var spoke *voice.Clip
	var transcribed *voice.Transcript
	cfg := Config{
		Provider: p,
		Model:    "m",
		Clock:    clock,
		Speak: func(_ context.Context, req voice.SpeechRequest) (*voice.Clip, error) {
			return &voice.Clip{
				Bytes: []byte{1}, Format: voice.FormatMP3, Voice: req.Voice,
				Usage: provider.Usage{CostUSD: 0.002, CostSource: "provider"},
			}, nil
		},
		Transcribe: func(context.Context, voice.TranscribeRequest) (*voice.Transcript, error) {
			return &voice.Transcript{Text: "hello", Usage: provider.Usage{CostUSD: 0.001, CostSource: "provider"}}, nil
		},
		Tools: registry(toolOf("speak_and_transcribe", false, func(ctx context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
			if tctx.Speak == nil || tctx.Transcribe == nil {
				return tool.Errorf("no voice seam"), nil
			}
			clip, err := tctx.Speak(ctx, voice.SpeechRequest{Model: "vendor/tts", Voice: "v", Input: "hi"})
			if err != nil {
				return tool.Errorf("speak: %v", err), nil
			}
			spoke = clip
			tctx.AddUsage(clip.Usage)
			transcript, err := tctx.Transcribe(ctx, voice.TranscribeRequest{Model: "vendor/stt", Audio: []byte{1}, Format: voice.FormatMP3})
			if err != nil {
				return tool.Errorf("transcribe: %v", err), nil
			}
			transcribed = transcript
			tctx.AddUsage(transcript.Usage)
			return tool.Text("%s", transcript.Text), nil
		})),
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Reason != "done" {
		t.Fatalf("reason = %q, want done", res.Reason)
	}
	if spoke == nil || spoke.Voice != "v" {
		t.Fatalf("tool got clip %+v, want the scripted one", spoke)
	}
	if transcribed == nil || transcribed.Text != "hello" {
		t.Fatalf("tool got transcript %+v, want the scripted one", transcribed)
	}
	if res.Usage.CostUSD != 0.003 {
		t.Errorf("run usage = %+v, want both voice calls counted", res.Usage)
	}
}
