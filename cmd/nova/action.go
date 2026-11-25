package main

import (
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/mode/action"
)

// actionReview builds `nova action review`.
func actionReview(stdout io.Writer, exit *int) *cobra.Command {
	var model, token string
	var maxComments, pr int
	var maxCost float64
	var wallClock time.Duration
	var dryRun, jsonOut bool
	cmd := &cobra.Command{
		Use:   "action review",
		Short: "Review a pull request and post one review",
		RunE: func(cmd *cobra.Command, _ []string) error {
			state := action.NewState(maxComments)
			opt := action.Options{
				Model: model, MaxComments: maxComments, MaxCostUSD: maxCost,
				WallClock: wallClock, PR: pr,
				DryRun: dryRun, JSON: jsonOut, Token: token,
				Out: stdout, State: state,
			}
			// The key is settled on the loaded config, before a server exists: the
			// config settles the model and the config key, the store settles the
			// rest, and building a server first would start the extension host and
			// every configured MCP server for a run that only prints a notice.
			build := mode.BuildOptions{
				Mode: "action", Version: version, Model: model, DenyAll: true,
				ExtraTools: state.Tools(), SystemPrompt: action.Prompt,
				Tools: []string{"read", "list", "glob", "grep", "review_comment", "review_summary"},
			}
			cfg, home, err := mode.LoadConfig(build)
			if err != nil {
				return err
			}
			opt.HasKey = mode.HasKeyForModel(cfg, home)
			if !opt.HasKey {
				*exit = action.Run(cmd.Context(), nil, opt)
				return nil
			}
			// Build reads the config again, deliberately: it is the server's own
			// load, and its reload, rather than a second opinion about this one.
			s, err := mode.Build(build)
			if err != nil {
				return err
			}
			defer s.Close()
			*exit = action.Run(cmd.Context(), s, opt)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&model, "model", "", "model id")
	f.IntVar(&maxComments, "max-comments", 15, "maximum inline comments")
	f.IntVar(&pr, "pr", 0, "pull request number (0 reads it from the event)")
	f.Float64Var(&maxCost, "max-cost", 0, "maximum cost in USD (0 means unlimited)")
	f.DurationVar(&wallClock, "wall-clock", 15*time.Minute, "wall-clock limit (0 means no limit)")
	f.BoolVar(&dryRun, "dry-run", false, "do not post the review")
	f.BoolVar(&jsonOut, "json", false, "print the report as JSON")
	f.StringVar(&token, "token", "", "GitHub token (or GITHUB_TOKEN)")
	return cmd
}
