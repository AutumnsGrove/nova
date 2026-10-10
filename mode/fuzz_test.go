package mode

import (
	"bytes"
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

func FuzzRPCLoop(f *testing.F) {
	f.Add([]byte(`{"type":"get_state","id":"1"}` + "\n"))
	f.Add([]byte(`{"type":"set_model","id":"1","model":"openai/gpt-6-luna"}` + "\n"))
	f.Add([]byte("not json\n"))
	f.Add([]byte(`{"type":"prompt","id":"1","text":"hi"}` + "\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg := config.Defaults()
		cfg.Tools.Active = []string{"read"}
		s, err := New(Options{
			Config:   &cfg,
			CWD:      t.TempDir(),
			Home:     t.TempDir(),
			Session:  t.TempDir() + "/s.jsonl",
			Provider: testkit.NewProvider(testkit.RealClock(), testkit.Turn{Text: "ok"}),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer s.Close()

		var out bytes.Buffer
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = ServeRPC(context.Background(), s, bytes.NewReader(data), &out)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("ServeRPC hung on arbitrary input")
		}
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			if line == "" {
				continue
			}
			if _, err := proto.Decode([]byte(line)); err != nil {
				t.Fatalf("stdout line is not a frame: %v (%q)", err, line)
			}
		}
	})
}

func TestProperty_SRV_EventOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		s, _ := testServer(t, testkit.Turn{Text: "ok"})
		events, cancel := s.Subscribe(4096)
		for _, cmd := range randomCommands(rng) {
			s.Handle(context.Background(), cmd)
		}
		if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"}); !resp.OK {
			t.Fatalf("iteration %d: prompt = %+v", i, resp)
		}
		got := collect(t, events)
		cancel()
		starts, ends := 0, 0
		for j, ev := range got {
			if j > 0 && ev.Seq <= got[j-1].Seq {
				t.Fatalf("iteration %d: seq not strictly increasing at %d: %d then %d", i, j, got[j-1].Seq, ev.Seq)
			}
			switch ev.Type {
			case proto.EventRunStart:
				starts++
			case proto.EventRunEnd:
				ends++
			}
		}
		if starts != 1 || ends != 1 {
			t.Fatalf("iteration %d: %d run_start vs %d run_end", i, starts, ends)
		}
	}
}

// randomCommands builds a random sequence of non-run commands.
func randomCommands(rng *rand.Rand) []proto.Command {
	n := rng.Intn(6) + 1
	out := make([]proto.Command, 0, n)
	for i := 0; i < n; i++ {
		switch rng.Intn(8) {
		case 0:
			out = append(out, proto.Command{Type: proto.CmdGetState})
		case 1:
			out = append(out, proto.Command{Type: proto.CmdSetThinking, Level: "high"})
		case 2:
			out = append(out, proto.Command{Type: proto.CmdSetContextLimit, Tokens: intPtr(1000)})
		case 3:
			out = append(out, proto.Command{Type: proto.CmdSessionName, Name: "n"})
		case 4:
			out = append(out, proto.Command{Type: proto.CmdGetMessages})
		case 5:
			out = append(out, proto.Command{Type: proto.CmdGetCommands})
		case 6:
			out = append(out, proto.Command{Type: proto.CmdListModels})
		default:
			out = append(out, proto.Command{Type: proto.CmdSteer, Text: "steer"})
		}
	}
	return out
}
