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
		var in struct {
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
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.Pattern == "" {
			return tool.Errorf("pattern is required"), nil
		}
		expr := in.Pattern
		if in.FixedStrings {
			expr = regexp.QuoteMeta(expr)
		}
		if in.CaseInsensitive {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return tool.Errorf("invalid pattern: %v", err), nil
		}
		ctxLines := in.Context
		if ctxLines < 0 {
			ctxLines = 0
		}
		if ctxLines > maxGrepContext {
			ctxLines = maxGrepContext
		}
		limit := in.MaxResults
		if limit <= 0 {
			limit = maxGrepMatches
		}
		if limit > maxGrepLimit {
			limit = maxGrepLimit
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

		var stats walkStats
		var out strings.Builder
		total, filesMatched, skipped := 0, 0, 0
		truncated := false
		opt := walkOptions{NoIgnore: in.NoIgnore, Hidden: in.Hidden, stats: &stats}
		err = walk(ctx, root, opt, func(e walkEntry) error {
			if e.IsDir {
				return nil
			}
			if globBase != "" && !globMatch(globBase, e.Rel) {
				return nil
			}
			if e.Size > maxGrepFileBytes {
				skipped++
				return nil
			}
			text, long, longCount, ok := loadGrepLines(e.Path)
			if !ok {
				skipped++
				return nil
			}
			skipped += longCount
			matches := matchGrepLines(re, text, long)
			if len(matches) == 0 {
				return nil
			}
			used, more := renderGrepFile(&out, e.Rel, text, matches, ctxLines, limit-total)
			total += used
			if used > 0 {
				filesMatched++
			}
			if more || total >= limit {
				truncated = true
				return errStopWalk
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStopWalk) {
			return tool.Errorf("%v", err), nil
		}
		details, _ := json.Marshal(grepDetails{Matches: total, Files: filesMatched, Truncated: truncated, Skipped: skipped})
		if total == 0 {
			content := fmt.Sprintf("no matches for %s", in.Pattern)
			if stats.unreadable > 0 {
				content += fmt.Sprintf("\n(%d directories could not be read)", stats.unreadable)
			}
			return tool.Result{Content: content, Details: details}, nil
		}
		content := out.String()
		if truncated {
			content += "... (more matches omitted; narrow the search)\n"
		}
		if stats.unreadable > 0 {
			content += fmt.Sprintf("(%d directories could not be read)\n", stats.unreadable)
		}
		return tool.Result{Content: content, Truncated: truncated, Details: details}, nil
	})
}

// loadGrepLines reads a file and returns its lines, a parallel slice marking
// lines that were too long to match, and how many were skipped. ok is false
// when the file was skipped: unreadable, binary, or too large.
func loadGrepLines(path string) (text []string, long []bool, skipped int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGrepFileBytes+1))
	if err != nil || len(data) > maxGrepFileBytes {
		return nil, nil, 0, false
	}
	if tool.LooksBinary(data) {
		return nil, nil, 0, false
	}
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		raw := data
		if i >= 0 {
			raw, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
		if len(raw) > maxGrepLineBytes {
			text, long, skipped = append(text, ""), append(long, true), skipped+1
			continue
		}
		text, long = append(text, string(raw)), append(long, false)
	}
	return text, long, skipped, true
}

// matchGrepLines returns the 1-based numbers of lines matching re. Lines marked
// long are skipped.
func matchGrepLines(re *regexp.Regexp, text []string, long []bool) []int {
	var out []int
	for i, line := range text {
		if long[i] {
			continue
		}
		if re.MatchString(line) {
			out = append(out, i+1)
		}
	}
	return out
}

// renderGrepFile writes one file's matches and their context, stopping after
// remaining matches. It reports whether more matches remained.
func renderGrepFile(out *strings.Builder, rel string, text []string, matches []int, context, remaining int) (used int, truncated bool) {
	matchSet := make(map[int]bool, len(matches))
	for _, m := range matches {
		matchSet[m] = true
	}
	lastEnd := 0
	for _, m := range matches {
		if used >= remaining {
			return used, true
		}
		start, end := m-context, m+context
		if start < 1 {
			start = 1
		}
		if end > len(text) {
			end = len(text)
		}
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

// cutGrepLine replaces invalid UTF-8 and cuts a long line to a displayable
// length. Matching happened on the raw bytes; this is only for output.
func cutGrepLine(line string) string {
	line = strings.ToValidUTF8(line, "\uFFFD")
	runes := []rune(line)
	if len(runes) > maxGrepLineChars {
		return string(runes[:maxGrepLineChars]) + "…"
	}
	return line
}
