package main

import "github.com/spf13/cobra"

// The help text is assembled here so the full and notui builds print the same
// modes, flags, subcommands, examples, and links; only the styling differs.

// helpDescription is the one-line summary above the usage block.
const helpDescription = "Nova is a coding agent for the terminal: one binary that is an\n" +
	"interactive agent, a headless tool, a protocol server, and a GitHub Action."

// helpModes documents the four run modes.
const helpModes = "Modes:\n" +
	"  tui     the interactive terminal UI (default in a terminal)\n" +
	"  print   one prompt, plain text (-p)\n" +
	"  json    one prompt, newline-delimited JSON events\n" +
	"  rpc     the JSON-RPC protocol on stdin and stdout"

// helpSessionFlags groups the flags that shape a session.
const helpSessionFlags = "Session flags:\n" +
	"  --session <path>  use a specific session file\n" +
	"  --continue        resume the most recent session for this directory\n" +
	"  --resume          pick a session to resume (TUI)\n" +
	"  --no-session      do not write a session file"

// helpLinks points at the documentation.
const helpLinks = "Docs: github.com/AutumnsGrove/nova"

// helpExamples is the Example block, rendered by fang and by cobra alike.
const helpExamples = "  nova                         start the TUI in this directory\n" +
	"  nova -p \"explain this repo\"   one prompt, print the answer\n" +
	"  nova --mode rpc              speak the JSON-RPC protocol\n" +
	"  nova doctor                  check the installation\n" +
	"  nova update                  refresh from a checkout or a release\n" +
	"  nova docs extensions         read the extension author's guide\n" +
	"\n" +
	"  # " + helpLinks

// helpLong is the description fang prints above the usage block.
func helpLong() string {
	return helpDescription + "\n\n" + helpModes + "\n\n" + helpSessionFlags
}

// applyGroups registers the subcommand groups and assigns each command to one.
func applyGroups(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: "run", Title: "Run:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "extend", Title: "Extend:"},
	)
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "models", "sessions", "docs":
			cmd.GroupID = "run"
		case "login", "logout", "doctor", "version", "update":
			cmd.GroupID = "setup"
		case "ext", "serve":
			cmd.GroupID = "extend"
		}
	}
}
