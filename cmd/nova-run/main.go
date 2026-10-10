// Command nova-run is a minimal headless frontend for the backend library: it wires the
// OpenRouter provider and the core tools into an agent and runs either one
// prompt or an interactive REPL.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/tools/bash"
	"github.com/AutumnsGrove/nova/tools/fs"
)

const defaultSystem = `You are a capable coding assistant running in a terminal.
You have tools to read, write, edit, and list files and to run bash commands.
Prefer the filesystem tools over shell equivalents when possible, keep changes minimal, and verify your work before finishing.`

func main() {
	var (
		model      = flag.String("model", "openai/gpt-4o-mini", "model id (OpenRouter slug)")
		prompt     = flag.String("p", "", "run a single prompt and exit")
		dir        = flag.String("dir", ".", "working directory")
		maxTurns   = flag.Int("max-turns", 25, "maximum agent turns")
		system     = flag.String("system", defaultSystem, "system prompt")
		sequential = flag.Bool("sequential", false, "run tool calls sequentially instead of in parallel")
		showCost   = flag.Bool("cost", true, "print token/cost totals after each run")
	)
	flag.Parse()

	workDir, err := filepath.Abs(*dir)
	if err != nil {
		fatal("resolve working directory: %v", err)
	}
	if _, err := os.Stat(workDir); err != nil {
		fatal("working directory: %v", err)
	}

	if os.Getenv("OPENROUTER_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "warning: OPENROUTER_API_KEY is not set")
	}

	registry := tool.NewRegistry()
	registry.MustRegister(fs.Read())
	registry.MustRegister(fs.Write())
	registry.MustRegister(fs.Edit())
	registry.MustRegister(fs.List())
	registry.MustRegister(bash.Bash())

	mode := agent.Parallel
	if *sequential {
		mode = agent.Sequential
	}

	cfg := agent.Config{
		Provider:      openrouter.New(openrouter.Config{APIKey: os.Getenv("OPENROUTER_API_KEY")}),
		Model:         *model,
		System:        *system,
		Tools:         registry,
		WorkDir:       workDir,
		MaxTurns:      *maxTurns,
		ToolExecution: mode,
		OnTextDelta: func(s string) {
			fmt.Print(s)
		},
		OnEvent: func(e agent.Event) {
			switch e.Type {
			case agent.EventToolResult:
				status := "ok"
				if e.IsError {
					status = "error"
				}
				fmt.Fprintf(os.Stderr, "\n[tool] %s (%s)\n", e.ToolName, status)
			case agent.EventError:
				fmt.Fprintf(os.Stderr, "\n[error] %v\n", e.Err)
			}
		},
	}

	if *prompt != "" {
		history := []message.Message{message.User(*prompt)}
		runOnce(cfg, history, *showCost)
		return
	}

	repl(cfg, *showCost)
}

func runOnce(cfg agent.Config, history []message.Message, showCost bool) {
	res, err := agent.Run(context.Background(), cfg, history)
	if err != nil {
		fmt.Fprintln(os.Stderr)
		fatal("agent: %v", err)
	}
	fmt.Println()
	if showCost {
		printUsage(res)
	}
}

func repl(cfg agent.Config, showCost bool) {
	fmt.Printf("nova — model %s, dir %s\n", cfg.Model, cfg.WorkDir)
	fmt.Println("Type a prompt, or /exit to quit. Ctrl-D also exits.")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var history []message.Message

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return
		}

		history = append(history, message.User(line))
		res, err := agent.Run(context.Background(), cfg, history)
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			continue
		}
		history = stripSystem(res.Messages)
		if showCost {
			printUsage(res)
		}
	}
}

func stripSystem(messages []message.Message) []message.Message {
	if len(messages) > 0 && messages[0].Role == message.RoleSystem {
		return messages[1:]
	}
	return messages
}

func printUsage(res *agent.Result) {
	u := res.Usage
	fmt.Fprintf(os.Stderr,
		"[usage] turns=%d prompt=%d completion=%d cached=%d cost=$%.6f\n",
		res.Turns, u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.CostUSD)
	if res.Exhausted {
		fmt.Fprintln(os.Stderr, "[warn] turn budget exhausted without a final answer")
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
