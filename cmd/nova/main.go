// Command nova is the main entry point: it parses flags, loads configuration,
// and picks a mode (print, JSON, or RPC today; the TUI in C15).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/update"
)

// version is stamped at build time with -ldflags; see version.go.

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, stdinIsTerminal()))
}

// stdinIsTerminal reports whether the process stdin is a character device.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// run is the testable entry point. It installs the process-wide crash recorder
// and recovers any panic that escapes a command, so a crash is never silent.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) (code int) {
	rec := newRecorder()
	diag.SetDefault(rec)
	defer func() {
		_ = rec.Close()
		diag.SetDefault(nil)
	}()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "\nnova: internal error: %v\n", r)
			if path, err := diag.Report(r, "command"); err == nil && path != "" {
				fmt.Fprintf(stderr, "nova: crash report: %s\n", path)
			}
			code = 1
		}
	}()
	exit := 0
	root := newRoot(stdin, stdout, stderr, tty, &exit)
	root.SetArgs(args)
	if err := execute(root, stderr); err != nil {
		return 2
	}
	return exit
}

// newRecorder builds the crash and debug recorder for this process. NOVA_DEBUG
// turns routine tracing on; --debug does the same from the command line.
func newRecorder() *diag.Recorder {
	opt := diag.Options{Version: version, Debug: debugRequested(os.Getenv("NOVA_DEBUG"))}
	if home, err := os.UserHomeDir(); err == nil {
		opt.Dir = config.UserConfigDir(home)
	}
	return diag.New(opt)
}

// debugRequested reports whether an environment value asks for debug tracing.
// Empty, "0", "false", "no", and "off" are off.
func debugRequested(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// newRoot builds the cobra command tree.
func newRoot(stdin io.Reader, stdout, stderr io.Writer, tty bool, exit *int) *cobra.Command {
	var (
		promptFlag   string
		modeFlag     string
		modelFlag    string
		thinkingFlag string
		sessionFlag  string
		noSession    bool
		continueFlag bool
		resumeFlag   bool
		toolsFlag    string
		noTools      bool
		cwdFlag      string
		maxTurns     int
		contextLimit int
		yes          bool
		debugFlag    bool
	)
	root := &cobra.Command{
		Use:           "nova",
		Short:         "Nova is a coding agent for the terminal",
		Long:          helpLong(),
		Example:       helpExamples,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if debugFlag {
				diag.SetDebug(true)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			code, err := runRoot(cmd.Context(), rootOptions{
				prompt: promptFlag, mode: modeFlag, model: modelFlag, thinking: thinkingFlag,
				session: sessionFlag, noSession: noSession, continueRun: continueFlag, resume: resumeFlag,
				tools: toolsFlag, noTools: noTools, cwd: cwdFlag, maxTurns: maxTurns,
				contextLimit: contextLimit, yes: yes, stdin: stdin, stdout: stdout, stderr: stderr, tty: tty,
				changed: cmd.Flags().Changed,
			})
			if err != nil {
				return err
			}
			*exit = code
			return nil
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.HiddenDefaultCmd = true
	flags := root.Flags()
	flags.StringVarP(&promptFlag, "prompt", "p", "", "run one prompt and exit (-p - reads stdin)")
	flags.StringVar(&modeFlag, "mode", "", "print | json | rpc | tui")
	flags.StringVar(&modelFlag, "model", "", "model id")
	flags.StringVar(&thinkingFlag, "thinking", "", "off | low | medium | high")
	flags.StringVar(&sessionFlag, "session", "", "session file path")
	flags.BoolVar(&noSession, "no-session", false, "do not write a session file")
	flags.BoolVar(&continueFlag, "continue", false, "resume the most recent session for the cwd")
	flags.BoolVar(&resumeFlag, "resume", false, "resume a session (TUI picker)")
	flags.StringVar(&toolsFlag, "tools", "", "comma-separated tools to enable")
	flags.BoolVar(&noTools, "no-tools", false, "enable no tools")
	flags.StringVar(&cwdFlag, "cwd", "", "working directory")
	flags.IntVar(&maxTurns, "max-turns", 0, "optional maximum agent turns (0 means unlimited)")
	flags.IntVar(&contextLimit, "context-limit", 0, "context token limit")
	flags.BoolVar(&yes, "yes", false, "auto-approve permission prompts for this run")
	root.PersistentFlags().BoolVar(&debugFlag, "debug", false, "write routine tracing to the debug log")

	root.AddCommand(
		versionCommand(stdout),
		modelsCommand(stdout),
		sessionsCommand(stdout, stderr),
		loginCommand(stdout, stderr),
		logoutCommand(stdout, stderr),
		docsCommand(stdout),
		logCommand(stdout),
		extCommand(stdin, stdout),
		serveCommand(),
		actionReview(stdout, exit),
		doctorCommand(stdout, exit),
		updateCommand(stdout, stderr, exit, update.Run),
	)
	applyGroups(root)
	return root
}

// rootOptions carries the parsed flags into runRoot.
type rootOptions struct {
	prompt, mode, model, thinking, session string
	noSession, continueRun, resume         bool
	tools, cwd                             string
	noTools, yes                           bool
	maxTurns, contextLimit                 int
	stdin                                  io.Reader
	stdout, stderr                         io.Writer
	tty                                    bool
	changed                                func(string) bool
}

// runRoot executes a normal (non-subcommand) invocation.
func runRoot(ctx context.Context, o rootOptions) (int, error) {
	if o.prompt != "" && (o.mode == "rpc" || o.mode == "tui") {
		return 0, fmt.Errorf("nova: -p cannot be combined with --mode %s", o.mode)
	}
	cwd, err := resolveCWD(o.cwd)
	if err != nil {
		return 0, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("nova: home directory: %w", err)
	}
	flags := map[string]any{}
	if o.changed("model") {
		flags["model"] = o.model
	}
	if o.changed("thinking") {
		flags["thinking"] = o.thinking
	}
	if o.changed("max-turns") {
		flags["max_turns"] = o.maxTurns
	}
	if o.changed("context-limit") {
		flags["context_limit"] = o.contextLimit
	}
	var tools []string
	if o.tools != "" {
		tools = splitList(o.tools)
	}
	sessionPath := o.session
	if o.continueRun {
		if sessionPath, err = newestSession(cwd); err != nil {
			return 0, err
		}
	}
	if o.resume && o.mode != "tui" {
		return 0, fmt.Errorf("nova: --resume needs the TUI (not built yet)")
	}
	tuiMode := shouldStartTUI(o.mode, o.tty, writerIsTerminal(o.stdout))
	diag.SetMode(modeLabel(o.mode, tuiMode))
	diag.Tracef("nova %s mode=%s", version, modeLabel(o.mode, tuiMode))
	if tuiMode {
		ok, err := ensureTrust(cwd, home, o.stdin, o.stdout)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}
		if o.tty && writerIsTerminal(o.stdout) && !mode.HasProviderKey(home) {
			if err := maybeFirstRun(ctx, home, o.stdin, o.stdout); err != nil {
				return 0, err
			}
		}
	} else if requiresKey(o.mode) && !mode.HasProviderKey(home) {
		fmt.Fprintln(o.stderr, "no API key: run `nova login` or set OPENROUTER_API_KEY")
		return 1, nil
	}
	s, err := mode.Build(mode.BuildOptions{
		CWD: cwd, Home: home, Mode: o.mode, Version: version, Flags: flags,
		Yes: o.yes, Interactive: tuiMode, NoSession: o.noSession, Session: sessionPath,
		NoTools: o.noTools, Tools: tools, Stderr: o.stderr,
	})
	if err != nil {
		return 0, err
	}
	defer s.Close()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case o.mode == "rpc":
		if err := mode.ServeRPC(sigCtx, s, o.stdin, o.stdout); err != nil {
			return 1, err
		}
		return 0, nil
	case o.mode == "json":
		return mode.RunJSON(sigCtx, s, o.prompt, o.stdout), nil
	case o.mode == "tui":
		return 0, startTUI(sigCtx, s)
	case o.prompt != "":
		text, err := buildPrompt(o.prompt, o.stdin, o.tty)
		if err != nil {
			return 0, err
		}
		if strings.TrimSpace(text) == "" {
			return 0, fmt.Errorf("nova: empty prompt")
		}
		if !mode.HasProviderKey(home) {
			fmt.Fprintln(o.stderr, "no API key: run `nova login` or set OPENROUTER_API_KEY")
			return 1, nil
		}
		return mode.RunPrint(sigCtx, s, text, o.stdout, o.stderr), nil
	default:
		if shouldStartTUI(o.mode, o.tty, writerIsTerminal(o.stdout)) {
			return 0, startTUI(sigCtx, s)
		}
		return 0, fmt.Errorf("nova: no mode selected; use -p, --mode, or a subcommand")
	}
}

// modeLabel names the run for a crash report. A bare invocation that opened
// the TUI is "tui"; an explicit --mode keeps its value; anything else is print.
func modeLabel(mode string, tui bool) string {
	switch {
	case tui:
		return "tui"
	case mode != "":
		return mode
	default:
		return "print"
	}
}

// writerIsTerminal reports whether a writer is a character device.
func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// buildPrompt reads the prompt, honouring "-" and piped stdin.
func buildPrompt(flag string, stdin io.Reader, tty bool) (string, error) {
	if flag == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("nova: read stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if !tty {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("nova: read stdin: %w", err)
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return flag + "\n\n```\n" + text + "\n```", nil
		}
	}
	return flag, nil
}

// requiresKey reports whether a non-TUI mode needs a credential before it can
// do anything: print, json, rpc, and serve all talk to the provider. The
// default print mode (an empty --mode with a prompt) is checked after the
// prompt is read, so an empty prompt is still a usage error.
func requiresKey(mode string) bool {
	switch mode {
	case "print", "json", "rpc", "serve":
		return true
	}
	return false
}

// resolveCWD validates an explicit --cwd.
func resolveCWD(flag string) (string, error) {
	if flag == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("nova: working directory: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(flag)
	if err != nil {
		return "", fmt.Errorf("nova: --cwd: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("nova: --cwd %s is not a directory", flag)
	}
	return abs, nil
}

// newestSession returns the most recent session path for cwd.
func newestSession(cwd string) (string, error) {
	infos, err := session.List(session.DefaultDir(cwd))
	if err != nil || len(infos) == 0 {
		return "", fmt.Errorf("nova: no session to continue in %s", cwd)
	}
	return infos[0].Path, nil
}

// splitList splits a comma-separated flag value.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func modelsCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List the model catalog",
		RunE: func(*cobra.Command, []string) error {
			for _, m := range provider.Models() {
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%d\n", m.ID, m.Name, m.Provider, m.ContextWindow)
			}
			return nil
		},
	}
}

func sessionsCommand(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List sessions for the working directory",
		RunE: func(*cobra.Command, []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			infos, err := session.List(session.DefaultDir(cwd))
			if err != nil {
				return err
			}
			for _, info := range infos {
				fmt.Fprintf(stdout, "%s\t%s\t%s\n", info.ID, info.Name, info.Modified.Format("2006-01-02 15:04"))
			}
			return nil
		},
	}
}

func loginCommand(stdout, stderr io.Writer) *cobra.Command {
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login [provider]",
		Short: "Log in to OpenRouter (default) or opencode-go",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			provider := "openrouter"
			if len(args) > 0 {
				provider = args[0]
			}
			switch provider {
			case "opencode-go", "opencodego":
				cfg := opencode.Config{Out: stderr}
				if v := os.Getenv("OPENCODE_CONSOLE_SERVER"); v != "" {
					cfg.Server = v
				}
				if !noBrowser {
					cfg.OpenBrowser = openBrowser
				}
				key, err := opencode.Login(cmd.Context(), cfg)
				if err != nil {
					return err
				}
				if err := opencode.SaveKey(opencode.DefaultKeyPath(home), key); err != nil {
					return err
				}
				fmt.Fprintln(stdout, "logged in to OpenCode Go")
				return nil
			case "openrouter", "":
			default:
				return fmt.Errorf("nova: unknown provider %q", provider)
			}
			opts := auth.LoginOptions{Out: stderr}
			if v := os.Getenv("NOVA_AUTH_URL"); v != "" {
				opts.AuthURL = v
			}
			if v := os.Getenv("NOVA_EXCHANGE_URL"); v != "" {
				opts.ExchangeURL = v
			}
			if !noBrowser {
				opts.OpenBrowser = openBrowser
			}
			key, err := auth.Login(cmd.Context(), opts)
			if err != nil {
				return err
			}
			store := auth.NewFileStore(filepath.Join(config.UserConfigDir(home), "auth.json"))
			if err := store.Save(key); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "logged in")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	return cmd
}

func logoutCommand(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of OpenRouter",
		RunE: func(*cobra.Command, []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			store := auth.NewFileStore(filepath.Join(config.UserConfigDir(home), "auth.json"))
			if err := store.Delete(); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "logged out")
			return nil
		},
	}
}

// openBrowser opens url in the platform's browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
