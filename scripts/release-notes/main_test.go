package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixture is a small repository with two tags, a merge commit and a
// changelog whose sections are adjacent, so every extraction is checked against
// a neighbour rather than against an empty file.
const (
	changelogV2 = `# Changelog

## [Unreleased]

## [0.2.0] - 2026-10-01

### Added

- Speech and transcription (#85).
`

	changelogV3 = `# Changelog

## [Unreleased]

## [0.3.0] - 2026-11-01

Upgrade notes for library consumers (#90):

- The line cap rises (#90).

### Added

- Release notes (#90).

## [0.2.0] - 2026-10-01

### Added

- Speech and transcription (#85).
`

	// sectionV3 is what the fallback must produce: the 0.3.0 content without its
	// heading, and without the 0.2.0 section that follows it.
	sectionV3 = `Upgrade notes for library consumers (#90):

- The line cap rises (#90).

### Added

- Release notes (#90).
`
)

// repo builds the fixture repository and returns its root. Commit dates are
// fixed so the log order never depends on the clock.
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	gitRun(t, dir, "config", "tag.gpgsign", "false")

	write(t, dir, "CHANGELOG.md", changelogV2)
	write(t, dir, "a.go", "package a\n")
	gitRun(t, dir, "add", "-A")
	commit(t, dir, "feat: add speech (#85)", "Adds voice/openrouter.")
	gitRun(t, dir, "tag", "-a", "v0.2.0", "-m", "v0.2.0")

	write(t, dir, "CHANGELOG.md", changelogV3)
	write(t, dir, "b.go", "package b\n")
	gitRun(t, dir, "add", "-A")
	commit(t, dir, "feat: write release notes (#90)", "Summarises the tag range.\n\nSecond paragraph.")
	gitRun(t, dir, "checkout", "-q", "-b", "feat/side")
	write(t, dir, "c.go", "package c\n")
	gitRun(t, dir, "add", "-A")
	commit(t, dir, "test: cover the new path", "")
	gitRun(t, dir, "checkout", "-q", "main")
	merge(t, dir, "feat/side", "Merge pull request #91 from example/feat/side")
	gitRun(t, dir, "tag", "-a", "v0.3.0", "-m", "v0.3.0")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func commit(t *testing.T, dir, subject, body string) {
	t.Helper()
	date := "2026-10-01T00:00:00+00:00"
	t.Setenv("GIT_AUTHOR_DATE", date)
	t.Setenv("GIT_COMMITTER_DATE", date)
	args := []string{"commit", "-q", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	gitRun(t, dir, args...)
}

func merge(t *testing.T, dir, branch, subject string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_DATE", "2026-10-01T00:00:00+00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2026-10-01T00:00:00+00:00")
	gitRun(t, dir, "merge", "-q", "--no-ff", branch, "-m", subject)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// stubPi writes a fake pi first on PATH. It records the prompt on stdin and the
// argv, then prints $PI_FAKE_OUT and exits with $PI_FAKE_CODE; $PI_FAKE_SLEEP
// blocks instead, for the timeout case.
func stubPi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	script := `#!/bin/sh
cat > "` + record + `.prompt"
printf '%s' "$*" > "` + record + `.argv"
if [ -n "${PI_FAKE_SLEEP:-}" ]; then sleep "$PI_FAKE_SLEEP"; fi
printf '%s' "${PI_FAKE_OUT:-}"
exit "${PI_FAKE_CODE:-0}"
`
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub pi: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

// notes runs the command against the fixture repository.
func notes(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(append([]string{"--repo", dir}, args...), &out, &errOut, nil)
	return out.String(), errOut.String(), code
}

func TestReleaseNotes_RN01_PromptCarriesBothEvidenceBlocks(t *testing.T) {
	dir := repo(t)
	record := stubPi(t)
	out, errOut, code := notes(t, dir, "--prompt-only", "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", code, errOut)
	}
	if _, err := os.Stat(record + ".prompt"); err == nil {
		t.Fatal("--prompt-only called the agent")
	}
	// The changelog section, the subjects and the diffstat all reach the model:
	// the two blocks are equal evidence, so both must be there.
	for _, want := range []string{
		"Upgrade notes for library consumers (#90)",
		"feat: write release notes (#90)",
		"Summarises the tag range",
		"Second paragraph",
		"b.go",
		"v0.2.0..v0.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt is missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "equal evidence") {
		t.Errorf("prompt does not say the two blocks are equal evidence\n%s", out)
	}
}

func TestReleaseNotes_RN02_BodyIsWrittenToStdout(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "### Added\n\n- Release notes (#90).\n")
	out, errOut, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", code, errOut)
	}
	if want := "### Added\n\n- Release notes (#90).\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if strings.Contains(errOut, "falling back") {
		t.Errorf("a good body still warned about falling back:\n%s", errOut)
	}
}

func TestReleaseNotes_RN03_BodyInAFenceIsUnwrapped(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "```markdown\n### Added\n\n- Release notes (#90).\n```\n")
	out, _, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if want := "### Added\n\n- Release notes (#90).\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestReleaseNotes_RN04_AgentFailureFallsBackToTheChangelog(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_CODE", "1")
	out, errOut, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran, without failing the release)", code)
	}
	if out != sectionV3 {
		t.Errorf("stdout = %q, want the 0.3.0 section %q", out, sectionV3)
	}
	if !strings.Contains(errOut, "falling back") {
		t.Errorf("no warning on stderr:\n%s", errOut)
	}
}

func TestReleaseNotes_RN05_EmptyOutputFallsBack(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "   \n\n")
	out, errOut, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != sectionV3 {
		t.Errorf("stdout = %q, want the 0.3.0 section", out)
	}
	if !strings.Contains(errOut, "empty") {
		t.Errorf("the warning does not name the empty body:\n%s", errOut)
	}
}

func TestReleaseNotes_RN06_OversizeOutputFallsBack(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "### Added\n\n- "+strings.Repeat("x", 200)+"\n")
	out, errOut, code := notes(t, dir, "--max-bytes", "100", "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != sectionV3 {
		t.Errorf("stdout = %q, want the 0.3.0 section", out)
	}
	if !strings.Contains(errOut, "100") {
		t.Errorf("the warning does not name the cap:\n%s", errOut)
	}
}

func TestReleaseNotes_RN07_UnstructuredOutputFallsBack(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "Sure! Here are the release notes you asked for.\n")
	out, errOut, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != sectionV3 {
		t.Errorf("stdout = %q, want the 0.3.0 section", out)
	}
	if !strings.Contains(errOut, "falling back") {
		t.Errorf("no warning on stderr:\n%s", errOut)
	}
}

func TestReleaseNotes_RN08_NoChangelogAndNoAgentWritesNothing(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_CODE", "1")
	// A tag the changelog says nothing about: the fallback has nothing to fall
	// back to and the release is published with no body, as it is today.
	gitRun(t, dir, "tag", "-a", "v9.9.9", "-m", "v9.9.9")
	out, errOut, code := notes(t, dir, "v0.2.0", "v9.9.9")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "no changelog section") {
		t.Errorf("the warning does not say the changelog had no section:\n%s", errOut)
	}
}

func TestReleaseNotes_RN09_OutWritesTheFile(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "### Added\n\n- Release notes (#90).\n")
	path := filepath.Join(t.TempDir(), "body.md")
	out, _, code := notes(t, dir, "--out", path, "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing when --out is set", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if want := "### Added\n\n- Release notes (#90).\n"; string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
}

func TestReleaseNotes_RN10_AgentFlagsArePinned(t *testing.T) {
	dir := repo(t)
	record := stubPi(t)
	t.Setenv("PI_FAKE_OUT", "### Added\n\n- Release notes (#90).\n")
	if _, _, code := notes(t, dir, "v0.2.0", "v0.3.0"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	argv, err := os.ReadFile(record + ".argv")
	if err != nil {
		t.Fatalf("the agent was not called: %v", err)
	}
	// A release that is reproducible needs the provider, the model, the thinking
	// level and the tool list pinned in the command, not in a user's config.
	for _, want := range []string{
		"-p", "--mode text", "--no-session", "--offline", "--no-mcp",
		"--no-themes", "-ne", "-ns", "-np", "-na", "-nc",
		"--provider opencode-go", "--model deepseek-v4.1-flash", "--thinking high",
		"--tools read,grep,find,ls,bash",
	} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("argv is missing %q:\n%s", want, argv)
		}
	}
	prompt, err := os.ReadFile(record + ".prompt")
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if !strings.Contains(string(prompt), "Upgrade notes for library consumers") {
		t.Errorf("the prompt did not reach the agent on stdin:\n%s", prompt)
	}
}

func TestReleaseNotes_RN11_ChangelogSectionStopsAtTheNextVersion(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_CODE", "1")
	out, _, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if strings.Contains(out, "## [0.3.0]") {
		t.Errorf("the fallback kept the version heading:\n%s", out)
	}
	if strings.Contains(out, "Speech and transcription") {
		t.Errorf("the fallback ran into the 0.2.0 section:\n%s", out)
	}
}

func TestReleaseNotes_RN12_FirstReleaseHasNoRange(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	out, errOut, code := notes(t, dir, "--prompt-only", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", code, errOut)
	}
	if !strings.Contains(out, "first release") {
		t.Errorf("the prompt does not tell the model this is a first release:\n%s", out)
	}
	if !strings.Contains(out, "feat: add speech (#85)") {
		t.Errorf("the prompt does not carry the whole history:\n%s", out)
	}
}

func TestReleaseNotes_RN13_UsageErrors(t *testing.T) {
	dir := repo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no ref", nil},
		{"unknown flag", []string{"--nope", "v0.3.0"}},
		{"too many refs", []string{"v0.2.0", "v0.3.0", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, code := notes(t, dir, tc.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
		})
	}
	t.Run("not a repository", func(t *testing.T) {
		_, _, code := notes(t, t.TempDir(), "v0.2.0", "v0.3.0")
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}
	})
	t.Run("unknown ref", func(t *testing.T) {
		_, _, code := notes(t, dir, "v0.0.1", "v0.3.0")
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}
	})
}

func TestReleaseNotes_RN14_AnsiIsStripped(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_OUT", "\x1b[1m### Added\x1b[0m\n\n- \x1b[32mRelease notes\x1b[0m (#90).\n")
	out, _, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if want := "### Added\n\n- Release notes (#90).\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestReleaseNotes_RN15_TimeoutFallsBackToTheChangelog(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	t.Setenv("PI_FAKE_SLEEP", "5")
	t.Setenv("PI_FAKE_OUT", "### Added\n\n- Too late.\n")
	// The deadline is a millisecond, so the fake never finishes and the script
	// must not wait for it. No test sleeps on real time.
	out, errOut, code := notes(t, dir, "--timeout", "1ms", "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != sectionV3 {
		t.Errorf("stdout = %q, want the 0.3.0 section", out)
	}
	if !strings.Contains(errOut, "1ms") {
		t.Errorf("the warning does not name the timeout:\n%s", errOut)
	}
}

func TestReleaseNotes_RN17_FlagsComeBeforeTheRefs(t *testing.T) {
	// Go's flag package stops at the first ref, so a flag after the range is a
	// usage error rather than a silently ignored one. The usage text says where
	// the flags go, and release.yml follows it.
	dir := repo(t)
	stubPi(t)
	_, errOut, code := notes(t, dir, "v0.2.0", "v0.3.0", "--out", filepath.Join(t.TempDir(), "b.md"))
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "[flags]") {
		t.Errorf("the usage text does not say where the flags go:\n%s", errOut)
	}
}

func TestReleaseNotes_RN18_DefaultCapFitsALargeRelease(t *testing.T) {
	// A release with a lot of backend work behind it is still one body. The
	// largest real release wanted 13 KiB, so the default has to accept that and
	// still refuse an answer that has become the commit dump.
	dir := repo(t)
	stubPi(t)
	large := "### Added\n\n" + strings.Repeat("- a change (#90) with enough words to reach twelve kilobytes.\n", 200)
	if len(large) < 12<<10 {
		t.Fatalf("fixture is only %d bytes", len(large))
	}
	t.Setenv("PI_FAKE_OUT", large)
	out, errOut, code := notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out == sectionV3 {
		t.Fatalf("a %d-byte body fell back under the default cap:\n%s", len(large), errOut)
	}
	t.Setenv("PI_FAKE_OUT", large+strings.Repeat("- one more change (#90) that pushes the body past the cap.\n", 120))
	out, _, code = notes(t, dir, "v0.2.0", "v0.3.0")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != sectionV3 {
		t.Errorf("an oversized body was published instead of falling back")
	}
}

func TestReleaseNotes_RN19_TimeoutKillsTheWholeGroup(t *testing.T) {
	// pi runs tools, so a child can outlive it holding the output pipe. Killing
	// only pi leaves cmd.Wait blocked on that pipe past the deadline, which in
	// CI would run into the step's own timeout and publish no body. The whole
	// group has to die for the deadline to bound wall clock.
	dir := repo(t)
	record := stubPi(t)
	script := "#!/bin/sh\nsleep 5 &\nsleep 5\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(record), "pi"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	// The deadline has to outlast the fork, or the shell dies before it spawns
	// the child that holds the pipe and the case cannot reproduce.
	start := time.Now()
	out, errOut, code := notes(t, dir, "--timeout", "300ms", "v0.2.0", "v0.3.0")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the deadline did not bound wall clock: it took %s", elapsed)
	}
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the changelog fallback ran)", code)
	}
	if out != sectionV3 {
		t.Errorf("stdout = %q, want the 0.3.0 section", out)
	}
	if !strings.Contains(errOut, "300ms") {
		t.Errorf("the warning does not name the timeout:\n%s", errOut)
	}
}

func TestReleaseNotes_RN16_MergeCommitsAreExcluded(t *testing.T) {
	dir := repo(t)
	stubPi(t)
	out, _, code := notes(t, dir, "--prompt-only", "v0.2.0", "v0.3.0")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(out, "Merge pull request") {
		t.Errorf("a merge commit reached the prompt:\n%s", out)
	}
	if !strings.Contains(out, "test: cover the new path") {
		t.Errorf("an ordinary commit is missing from the prompt:\n%s", out)
	}
}
