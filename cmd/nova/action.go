package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/mode/action"
	"github.com/AutumnsGrove/nova/permission"
)

// actionCommand builds the `nova action` command tree.
func actionCommand(stdout, stderr io.Writer, exit *int) *cobra.Command {
	cmd := &cobra.Command{Use: "action", Short: "Run Nova in CI"}
	cmd.AddCommand(actionReview(stdout, stderr, exit))
	return cmd
}

// actionReview builds `nova action review`.
func actionReview(stdout, stderr io.Writer, exit *int) *cobra.Command {
	var (
		model           string
		maxComments     int
		maxCost         float64
		dryRun, jsonOut bool
		token, apiURL   string
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review a pull request and post one review",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			store := auth.NewFileStore(filepath.Join(config.UserConfigDir(home), "auth.json"))
			key, _, _ := auth.Resolve("", os.Getenv, store)
			state := action.NewState(maxComments)
			opt := action.Options{
				Model: model, MaxComments: maxComments, MaxCostUSD: maxCost,
				DryRun: dryRun, JSON: jsonOut, Token: token, APIURL: apiURL,
				HasKey: key != "", Out: stdout, State: state,
			}
			if key == "" {
				*exit = action.Run(cmd.Context(), nil, opt)
				return nil
			}
			s, err := buildActionServer(state, model)
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
	f.Float64Var(&maxCost, "max-cost", 1.00, "maximum cost in USD (0 means unlimited)")
	f.BoolVar(&dryRun, "dry-run", false, "do not post the review")
	f.BoolVar(&jsonOut, "json", false, "print the report as JSON")
	f.StringVar(&token, "token", "", "GitHub token (or GITHUB_TOKEN)")
	f.StringVar(&apiURL, "api-url", "", "GitHub API URL")
	return cmd
}

// buildActionServer builds the read-only review server with the review tools.
func buildActionServer(state *action.State, model string) (*mode.Server, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	trust := config.NewTrustStore(filepath.Join(config.UserConfigDir(home), "trust.json"))
	cfg, _, err := config.Load(config.LoadOptions{CWD: cwd, Home: home, Trusted: trust.IsTrusted})
	if err != nil {
		return nil, err
	}
	if model != "" {
		cfg.Model = model
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 30
	}
	cfg.Permissions = permission.Policy{Default: permission.Deny, Headless: permission.Deny}
	return mode.New(mode.Options{
		Config: cfg, CWD: cwd, Home: home, Version: version, Mode: "action",
		ExtraTools: state.Tools(), SystemPrompt: action.Prompt,
		Tools: []string{"read", "list", "glob", "grep", "review_comment", "review_summary"},
	})
}
