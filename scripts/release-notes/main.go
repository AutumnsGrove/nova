// Command release-notes writes the body of a GitHub release from the tag range.
//
// It exists because every release Nova published before it had an empty body:
// the changelog is the record, but it is not where a reader lands. The agent
// summarises the changelog section and the commit log together, and the release
// never blocks on it: when the agent fails, times out, or returns something
// unusable, the changelog section becomes the body instead, and if there is no
// section either, nothing does.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed prompt.md
var rules string

const (
	// Pinned so a release is reproducible: the provider, model and thinking
	// level are the ones pi-review already runs in CI.
	defaultProvider = "opencode-go"
	defaultModel    = "deepseek-v4.1-flash"
	defaultThinking = "high"
	// The body is a summary, not a changelog. Sixteen kilobytes is about forty
	// bullets with room for upgrade notes, which covers a release with a lot of
	// backend work behind it (the largest real one wanted 13 KiB) while still
	// catching an answer that has started dumping the commit log.
	defaultMaxBytes = 16 << 10
	defaultTimeout  = 5 * time.Minute
	// emptyTree is git's hash of an empty tree. It is the diff base for a first
	// release, which has no previous tag to compare against.
	emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	// Read-only inspection so the agent can ground a claim in the tree. bash is
	// there for the same reason and runs in a throwaway runner.
	agentTools = "read,grep,find,ls,bash"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

// options is the parsed command line.
type options struct {
	from, to   string
	repo       string
	changelog  string
	out        string
	provider   string
	model      string
	thinking   string
	maxBytes   int
	timeout    time.Duration
	promptOnly bool
}

// run is the testable entry point: 0 the agent's answer is the body, 3 the body
// came from the fallback (or there is none), 2 a usage or setup error. CI turns
// 3 into a warning, so a silent fallback cannot hide a body that was never
// written by the agent.
func run(args []string, stdout, stderr io.Writer, env []string) int {
	o, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "release-notes: %v\n", err)
		return 2
	}
	ev, err := collect(context.Background(), o, env)
	if err != nil {
		fmt.Fprintf(stderr, "release-notes: %v\n", err)
		return 2
	}
	prompt := buildPrompt(ev)
	if o.promptOnly {
		fmt.Fprint(stdout, prompt)
		return 0
	}

	body, why := generate(o, prompt, env)
	if why != "" {
		fmt.Fprintf(stderr, "release-notes: %s; falling back to the changelog\n", why)
	}
	if body == "" {
		if body = clean(ev.changelog); body == "" {
			fmt.Fprintf(stderr, "release-notes: no changelog section for %s either; publishing no body\n", ev.version)
		}
	}
	if err := emit(o, stdout, stderr, body); err != nil {
		fmt.Fprintf(stderr, "release-notes: %v\n", err)
		return 2
	}
	if why != "" {
		return 3
	}
	return 0
}

// parse reads the command line: one ref is a first release, two are a range.
func parse(args []string) (options, error) {
	o := options{repo: ".", changelog: "CHANGELOG.md", maxBytes: defaultMaxBytes,
		timeout: defaultTimeout, provider: defaultProvider, model: defaultModel, thinking: defaultThinking}
	fs := flag.NewFlagSet("release-notes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.repo, "repo", o.repo, "repository to read")
	fs.StringVar(&o.changelog, "changelog", o.changelog, "changelog path, relative to the repository")
	fs.StringVar(&o.out, "out", "", "write the body to this file instead of stdout")
	fs.StringVar(&o.provider, "provider", o.provider, "agent provider")
	fs.StringVar(&o.model, "model", o.model, "agent model")
	fs.StringVar(&o.thinking, "thinking", o.thinking, "agent thinking level")
	fs.IntVar(&o.maxBytes, "max-bytes", o.maxBytes, "reject a body larger than this")
	fs.DurationVar(&o.timeout, "timeout", o.timeout, "time limit for the agent")
	fs.BoolVar(&o.promptOnly, "prompt-only", false, "print the prompt and exit without calling the agent")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	switch fs.NArg() {
	case 1:
		o.to = fs.Arg(0)
	case 2:
		o.from, o.to = fs.Arg(0), fs.Arg(1)
	default:
		return o, errors.New("usage: release-notes [flags] [previous-tag] <tag>")
	}
	if o.from == o.to {
		return o, fmt.Errorf("range %s..%s is empty", o.from, o.to)
	}
	return o, nil
}

// evidence is everything the agent is given. The changelog section and the
// commit log are both inputs and neither outranks the other.
type evidence struct {
	from, to  string
	version   string
	commits   string
	count     int
	diffstat  string
	changelog string
}

// collect gathers the tag range from git and the release's changelog section.
func collect(ctx context.Context, o options, env []string) (evidence, error) {
	ev := evidence{from: o.from, to: o.to, version: strings.TrimPrefix(o.to, "v")}
	if _, err := os.Stat(o.repo); err != nil {
		return ev, fmt.Errorf("repository: %w", err)
	}
	rangeSpec := ev.to
	if ev.from != "" {
		rangeSpec = ev.from + ".." + ev.to
	}
	count, err := git(ctx, o.repo, env, "rev-list", "--no-merges", "--count", rangeSpec)
	if err != nil {
		return ev, err
	}
	if ev.count, err = atoi(strings.TrimSpace(count)); err != nil {
		return ev, err
	}
	// Merges are excluded because a pull request's commits are the change and
	// its merge commit is the plumbing; %b keeps the body, which carries the why.
	if ev.commits, err = git(ctx, o.repo, env, "log", "--no-merges", "--reverse", "--format=%s%n%b", rangeSpec); err != nil {
		return ev, err
	}
	base := ev.from
	if base == "" {
		base = emptyTree
	}
	if ev.diffstat, err = git(ctx, o.repo, env, "diff", "--stat", "--no-color", base, ev.to); err != nil {
		return ev, err
	}
	if ev.changelog, err = changelogSection(o.repo, o.changelog, ev.version); err != nil {
		return ev, err
	}
	return ev, nil
}

// git runs one git command in the repository. A missing ref is the common
// failure, so its stderr is the message rather than a bare exit status.
func git(ctx context.Context, repo string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	if len(env) > 0 {
		cmd.Env = env
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

func atoi(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("read commit count %q: %w", s, err)
	}
	return n, nil
}

// changelogSection returns a version's content without its heading, stopping at
// the next version. A missing file or section is not an error: the range is
// still worth summarising, and the caller reports the empty fallback.
func changelogSection(repo, name, version string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repo, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	lines := strings.Split(string(data), "\n")
	head := "## [" + version + "]"
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), head) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start+1:end], "\n")), nil
}

// buildPrompt assembles the rules and the evidence into one request.
func buildPrompt(ev evidence) string {
	var b strings.Builder
	b.WriteString(rules)
	b.WriteString("\n# Evidence\n\n")

	fmt.Fprintf(&b, "## The changelog section for %s\n\n", ev.to)
	if ev.changelog == "" {
		fmt.Fprintf(&b, "None: the changelog has no section for %s, so the commit log is the only evidence.\n\n", ev.version)
	} else {
		b.WriteString(ev.changelog)
		b.WriteString("\n\n")
	}

	if ev.from == "" {
		fmt.Fprintf(&b, "This is the first release: there is no previous tag, so the commits and the diffstat cover the whole history up to %s.\n\n", ev.to)
	}
	rangeLabel := ev.to
	if ev.from != "" {
		rangeLabel = ev.from + ".." + ev.to
	}
	fmt.Fprintf(&b, "## %d commits in %s, merges excluded, oldest first\n\n", ev.count, rangeLabel)
	b.WriteString(strings.TrimSpace(ev.commits))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## Diffstat for %s\n\n```\n%s\n```\n", rangeLabel, strings.TrimSpace(ev.diffstat))
	return b.String()
}

// generate runs the agent and vets its answer. A non-empty why means the answer
// is unusable and the caller must fall back.
func generate(o options, prompt string, env []string) (body, why string) {
	if _, err := exec.LookPath("pi"); err != nil {
		return "", "pi is not installed"
	}
	args := []string{
		"-p", "--mode", "text", "--no-session", "-na", "-nc", "--offline", "-ne", "-ns", "-np",
		"--no-themes", "--no-mcp", "--tools", agentTools,
		"--provider", o.provider, "--model", o.model, "--thinking", o.thinking,
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pi", args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	cmd.Stdin = strings.NewReader(prompt)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Sprintf("the agent did not finish within %s", o.timeout)
		}
		msg := strings.TrimSpace(errOut.String())
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", "the agent failed: " + msg
	}
	body = clean(out.String())
	switch {
	case body == "":
		return "", "the agent returned an empty body"
	case len(body) > o.maxBytes:
		return "", fmt.Sprintf("the agent returned %d bytes, over the %d-byte cap", len(body), o.maxBytes)
	case !hasStructure(body):
		return "", "the agent returned no headings or bullets"
	}
	return body, ""
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// clean makes an answer safe to publish: no terminal escapes, no fence around
// the whole body, no trailing spaces, at most one blank line between blocks.
func clean(s string) string {
	s = ansi.ReplaceAllString(s, "")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	// Drop blank edge lines first, so the fence check sees the real first and
	// last line rather than the empty tail after the final newline.
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 1 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") &&
		strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[1 : len(lines)-1]
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	if out = strings.TrimSpace(out); out == "" {
		return ""
	}
	return out + "\n"
}

// hasStructure rejects prose and preamble: a release body is headings and
// bullets, so an answer with neither is the model talking about the task.
func hasStructure(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"## ", "### ", "- ", "* "} {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

// emit writes the body where the caller asked for it.
func emit(o options, stdout, stderr io.Writer, body string) error {
	if o.out == "" {
		_, err := io.WriteString(stdout, body)
		return err
	}
	if err := os.WriteFile(o.out, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", o.out, err)
	}
	fmt.Fprintf(stderr, "release-notes: wrote %d bytes to %s\n", len(body), o.out)
	return nil
}
