package config

import (
	"bytes"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// Defaults for context-file discovery.
const (
	defaultMaxFileBytes  = 64 << 10  // 64 KiB
	defaultMaxTotalBytes = 256 << 10 // 256 KiB
	binarySniffBytes     = 8 << 10
	truncatedMarker      = "\n\n... (file truncated) ...\n"
)

// defaultContextNames are the files searched for instructions, in order.
var defaultContextNames = []string{"AGENTS.md", "CLAUDE.md"}

// ContextFile is one project-instruction file that will be placed in the
// system prompt.
type ContextFile struct {
	Path      string
	Content   string
	Truncated bool
}

// ContextOptions controls LoadContext. Zero values are filled with defaults.
type ContextOptions struct {
	Names         []string
	MaxFileBytes  int
	MaxTotalBytes int
	ReadFile      func(string) ([]byte, error)
	// Trusted reports whether a project tree may contribute instructions. A nil
	// function means the project is not trusted, so only the global file loads.
	Trusted func(dir string) bool
}

// LoadContext reads AGENTS.md and CLAUDE.md from the user config directory and
// then from the project tree, outermost first. An untrusted project contributes
// only the global file. Problems are warnings, never errors.
func LoadContext(cwd, home string, opt ContextOptions) ([]ContextFile, []Warning) {
	names := opt.Names
	if len(names) == 0 {
		names = defaultContextNames
	}
	maxFile := opt.MaxFileBytes
	if maxFile <= 0 {
		maxFile = defaultMaxFileBytes
	}
	maxTotal := opt.MaxTotalBytes
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalBytes
	}
	readFile := opt.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	var warnings []Warning
	trusted := opt.Trusted != nil && opt.Trusted(cwd)

	var global []string
	for _, name := range names {
		global = append(global, filepath.Join(UserConfigDir(home), name))
	}
	var project []string
	for _, dir := range ancestorDirs(cwd, home) {
		for _, name := range names {
			project = append(project, filepath.Join(dir, name))
		}
	}

	candidates := global
	if trusted {
		candidates = append(append([]string{}, global...), project...)
	} else if anyExists(project) {
		warnings = append(warnings, Warning{
			Source:  cwd,
			Message: "project is not trusted; skipping project instructions",
		})
	}

	seen := map[string]bool{}
	var files []ContextFile
	globalCount := 0
	for i, path := range candidates {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		if seen[real] {
			continue
		}
		file, ok := loadContextFile(readFile, path, maxFile, &warnings)
		if !ok {
			continue
		}
		seen[real] = true
		files = append(files, file)
		if i < len(global) {
			globalCount++
		}
	}

	files, warnings = capTotal(files, globalCount, maxTotal, warnings)
	return files, warnings
}

// loadContextFile reads and normalises one candidate. It reports whether the
// file should be included; any problem is recorded as a warning.
func loadContextFile(readFile func(string) ([]byte, error), path string, maxFile int, warnings *[]Warning) (ContextFile, bool) {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return ContextFile{}, false
		}
		if err := checkReadable(info, maxReadBytes); err != nil {
			*warnings = append(*warnings, Warning{Source: path, Message: "skipping context file: " + err.Error()})
			return ContextFile{}, false
		}
	}
	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ContextFile{}, false
		}
		*warnings = append(*warnings, Warning{Source: path, Message: "cannot read context file: " + err.Error()})
		return ContextFile{}, false
	}
	data = stripBOM(data)
	if len(data) == 0 {
		return ContextFile{}, false
	}
	if hasNUL(data) {
		*warnings = append(*warnings, Warning{Source: path, Message: "skipping binary context file"})
		return ContextFile{}, false
	}
	truncated := false
	if len(data) > maxFile {
		data = cutAtRune(data, maxFile)
		truncated = true
	}
	content := string(data)
	if truncated {
		content += truncatedMarker
	}
	return ContextFile{Path: path, Content: content, Truncated: truncated}, true
}

// capTotal drops the outermost project files until the total fits, warning for
// each. globalCount is how many leading entries are the untouchable global ones.
func capTotal(files []ContextFile, globalCount, maxTotal int, warnings []Warning) ([]ContextFile, []Warning) {
	total := 0
	for _, file := range files {
		total += len(file.Content)
	}
	for total > maxTotal && len(files) > globalCount {
		dropped := files[globalCount]
		total -= len(dropped.Content)
		files = append(files[:globalCount], files[globalCount+1:]...)
		warnings = append(warnings, Warning{Source: dropped.Path, Message: "dropped context file to stay under the total size cap"})
	}
	return files, warnings
}

// anyExists reports whether any candidate is a file. It stats instead of
// reading: this runs for an untrusted project, whose files must not be opened.
func anyExists(paths []string) bool {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// ancestorDirs lists the project directories from the outermost boundary down
// to cwd. The boundary is home's parent when cwd is below home, else the root.
func ancestorDirs(cwd, home string) []string {
	var inner []string
	dir := filepath.Clean(cwd)
	for {
		inner = append(inner, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	boundary := len(inner)
	homeParent := filepath.Dir(filepath.Clean(home))
	for i, d := range inner {
		if d == homeParent {
			boundary = i + 1
			break
		}
	}
	selected := inner[:boundary]
	out := make([]string, len(selected))
	for i, d := range selected {
		out[len(selected)-1-i] = d
	}
	return out
}

// stripBOM removes a leading UTF-8 byte-order mark.
func stripBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// hasNUL reports a NUL byte in the sniff window.
func hasNUL(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0
}

// cutAtRune returns at most limit leading bytes of data without splitting a
// UTF-8 sequence, which would put invalid text into the system prompt.
func cutAtRune(data []byte, limit int) []byte {
	for limit > 0 && limit < len(data) && !utf8.RuneStart(data[limit]) {
		limit--
	}
	return data[:limit]
}
