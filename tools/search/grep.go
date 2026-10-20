package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/tool"
)

const (
	maxGrepMatches   = 200     // default match cap
	maxGrepLimit     = 1000    // hard match cap
	maxGrepContext   = 10      // hard context cap
	maxGrepFileBytes = 2 << 20 // files larger than this are skipped
	maxGrepLineChars = 500     // displayed chars before a line is cut
	maxGrepLineBytes = 1 << 20 // lines longer than this are not matched
)

// errStopWalk stops the walk once the match cap is reached.
var errStopWalk = errors.New("stop walk")

// grepDetails is the structured data grep attaches to Result.Details.
type grepDetails struct {
	Matches   int  `json:"matches"`
	Files     int  `json:"files"`
	Truncated bool `json:"truncated"`
	Skipped   int  `json:"skipped"`
}

// grepInput is the arguments of one grep call.
type grepInput struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	Glob            string `json:"glob"`
	CaseInsensitive bool   `json:"case_insensitive"`
	FixedStrings    bool   `json:"fixed_strings"`
	Context         int    `json:"context"`
	MaxResults      int    `json:"max_results"`
	Hidden          bool   `json:"hidden"`
	NoIgnore        bool   `json:"no_ignore"`
}

// compile builds the matcher from the pattern and its flags.
func (in grepInput) compile() (*regexp.Regexp, error) {
	expr := in.Pattern
	if in.FixedStrings {
		expr = regexp.QuoteMeta(expr)
	}
	if in.CaseInsensitive {
		expr = "(?i)" + expr
	}
	return regexp.Compile(expr)
}

// Grep returns the grep tool. It searches file contents with a RE2 regular
// expression and prints matching lines with optional context.
func Grep() tool.Tool {
	return tool.New(tool.Info{
		Name:        "grep",
		Description: "Search file contents with a regular expression. Returns matching lines as path:line:text, with optional context lines. Respects .gitignore and skips binary files.",
		ReadOnly:    true,
		Parallel:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":          tool.StringProp("RE2 regular expression"),
				"path":             tool.StringProp("File or directory to search (default \".\")"),
				"glob":             tool.StringProp("Only search files whose path matches this glob"),
				"case_insensitive": tool.BoolProp("Case-insensitive matching"),
				"fixed_strings":    tool.BoolProp("Treat pattern as a literal string"),
				"context":          tool.IntProp("Context lines before and after each match (0-10)"),
				"max_results":      tool.IntProp("Maximum matching lines (default 200, max 1000)"),
				"hidden":           tool.BoolProp("Include dot-files and dot-directories"),
				"no_ignore":        tool.BoolProp("Ignore .gitignore files"),
			},
			"required": []string{"pattern"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in grepInput
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.Pattern == "" {
			return tool.Errorf("pattern is required"), nil
		}
		re, err := in.compile()
		if err != nil {
			return tool.Errorf("invalid pattern: %v", err), nil
		}
		globBase := strings.TrimSuffix(in.Glob, "/")
		if globBase != "" && !permission.ValidPath(globBase) {
			return tool.Errorf("invalid glob: %s", in.Glob), nil
		}
		if in.Path == "" {
			in.Path = "."
		}
		root, err := tool.ResolvePath(tctx, in.Path)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		info, err := os.Stat(root)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return tool.Errorf("%s is not a file or directory", in.Path), nil
		}
		g := &grepRun{
			re:       re,
			glob:     globBase,
			ctxLines: min(max(in.Context, 0), maxGrepContext),
			limit:    maxGrepMatches,
		}
		if in.MaxResults > 0 {
			g.limit = min(in.MaxResults, maxGrepLimit)
		}
		var stats walkStats
		opt := walkOptions{NoIgnore: in.NoIgnore, Hidden: in.Hidden, stats: &stats}
		if err := walk(ctx, root, opt, g.visit); err != nil && !errors.Is(err, errStopWalk) {
			return tool.Errorf("%v", err), nil
		}
		return g.result(in.Pattern, stats.unreadable), nil
	})
}

// grepRun is the state of one search: what to match, how much to show, and
// what has been found so far.
type grepRun struct {
	re       *regexp.Regexp
	glob     string // empty means every file
	ctxLines int
	limit    int

	out       strings.Builder
	total     int  // matching lines shown
	files     int  // files with at least one match (D50)
	skipped   int  // files not searched, plus lines too long to match
	truncated bool // the match cap was hit
}

// visit searches one walked file. It returns errStopWalk once the cap is hit.
func (g *grepRun) visit(e walkEntry) error {
	if e.IsDir || (g.glob != "" && !globMatch(g.glob, e.Rel)) {
		return nil
	}
	// Only regular files are opened: a named pipe with no writer would block the
	// open forever, and a device has no end.
	if !e.Regular || e.Size > maxGrepFileBytes {
		g.skipped++
		return nil
	}
	text, long, tooLong, ok := loadGrepLines(e.Path)
	if !ok {
		g.skipped++
		return nil
	}
	g.skipped += tooLong
	matches := matchGrepLines(g.re, text, long)
	if len(matches) == 0 {
		return nil
	}
	used, more := renderGrepFile(&g.out, e.Rel, text, matches, g.ctxLines, g.limit-g.total)
	g.total += used
	if used > 0 {
		g.files++
	}
	if more || g.total >= g.limit {
		g.truncated = true
		return errStopWalk
	}
	return nil
}

// result shapes the findings into the tool result. unreadable is how many
// directories the walk could not read, which the model should know about.
func (g *grepRun) result(pattern string, unreadable int) tool.Result {
	details, _ := json.Marshal(grepDetails{Matches: g.total, Files: g.files, Truncated: g.truncated, Skipped: g.skipped})
	note := fmt.Sprintf("(%d directories could not be read)", unreadable)
	if g.total == 0 {
		content := fmt.Sprintf("no matches for %s", pattern)
		if unreadable > 0 {
			content += "\n" + note
		}
		return tool.Result{Content: content, Details: details}
	}
	content := g.out.String()
	if g.truncated {
		content += "... (more matches omitted; narrow the search)\n"
	}
	if unreadable > 0 {
		content += note + "\n"
	}
	return tool.Result{Content: content, Truncated: g.truncated, Details: details}
}

// loadGrepLines reads a file and returns its lines, a parallel slice marking
// lines that were too long to match, and how many of those there were. ok is
// false when the file was skipped: unreadable, binary, or too large.
func loadGrepLines(path string) (text []string, long []bool, tooLong int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGrepFileBytes+1))
	if err != nil || len(data) > maxGrepFileBytes || tool.LooksBinary(data) {
		return nil, nil, 0, false
	}
	for len(data) > 0 {
		raw, rest, _ := bytes.Cut(data, []byte{'\n'})
		data = rest
		raw = bytes.TrimSuffix(raw, []byte{'\r'})
		if len(raw) > maxGrepLineBytes {
			text, long, tooLong = append(text, ""), append(long, true), tooLong+1
			continue
		}
		text, long = append(text, string(raw)), append(long, false)
	}
	return text, long, tooLong, true
}

// matchGrepLines returns the 1-based numbers of lines matching re. Lines marked
// long are skipped.
func matchGrepLines(re *regexp.Regexp, text []string, long []bool) []int {
	var out []int
	for i, line := range text {
		if !long[i] && re.MatchString(line) {
			out = append(out, i+1)
		}
	}
	return out
}

// renderGrepFile writes one file's matches and their context, stopping after
// remaining matches. It reports how many matches it wrote and whether more
// remained.
func renderGrepFile(out *strings.Builder, rel string, text []string, matches []int, ctxLines, remaining int) (used int, truncated bool) {
	matchSet := make(map[int]bool, len(matches))
	for _, m := range matches {
		matchSet[m] = true
	}
	lastEnd := 0
	for _, m := range matches {
		if used >= remaining {
			return used, true
		}
		start, end := max(m-ctxLines, 1), min(m+ctxLines, len(text))
		if lastEnd > 0 {
			switch {
			case start > lastEnd+1:
				out.WriteString("--\n")
			case start <= lastEnd:
				start = lastEnd + 1
			}
		}
		for ln := start; ln <= end; ln++ {
			sep := "-"
			if matchSet[ln] {
				sep = ":"
			}
			fmt.Fprintf(out, "%s%s%d%s%s\n", rel, sep, ln, sep, cutGrepLine(text[ln-1]))
		}
		used++
		lastEnd = end
	}
	return used, false
}

// cutGrepLine cuts a long line to a displayable length and replaces invalid
// UTF-8. Matching happened on the raw bytes; this is only for output. It cuts
// first, without converting the line to runes, so a megabyte-long line costs
// nothing beyond the 500 characters shown.
func cutGrepLine(line string) string {
	suffix := ""
	n := 0
	for i := range line {
		if n == maxGrepLineChars {
			line, suffix = line[:i], "…"
			break
		}
		n++
	}
	return strings.ToValidUTF8(line, "�") + suffix
}
