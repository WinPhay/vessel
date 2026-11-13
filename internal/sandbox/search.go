package sandbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

var (
	ErrPatternEmpty = errors.New("search pattern cannot be empty")
)

const (
	DefaultMaxGrepMatches  = 100
	AbsoluteMaxGrepMatches = 1000
	MaxSearchFileSize      = 2 * 1024 * 1024 // 2 MB
)

// GrepMatch represents a single matching line in a file.
type GrepMatch struct {
	File       string `json:"file"`
	LineNumber int    `json:"line_number"`
	LineText   string `json:"line_text"`
}

// GrepResult holds aggregated search results across the workspace.
type GrepResult struct {
	Pattern       string      `json:"pattern"`
	TotalMatches  int         `json:"total_matches"`
	FilesSearched int         `json:"files_searched"`
	Matches       []GrepMatch `json:"matches"`
	TokenEstimate int         `json:"token_estimate"`
	Truncated     bool        `json:"truncated"`
}

// GrepOptions configures workspace search behavior.
type GrepOptions struct {
	Pattern       string
	Path          string // Path relative to workspace
	FilePattern   string // Glob filter (e.g. "*.go", "*.ts")
	MaxMatches    int    // Maximum matches to return
	CaseSensitive bool
}

// GrepWorkspace searches for a pattern across files within the workspace sandbox jail.
func GrepWorkspace(workspaceRoot string, opts GrepOptions) (*GrepResult, error) {
	if strings.TrimSpace(opts.Pattern) == "" {
		return nil, ErrPatternEmpty
	}

	searchDir := "."
	if opts.Path != "" {
		searchDir = opts.Path
	}

	resolvedDir, err := ResolveSecurePath(workspaceRoot, searchDir)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(resolvedDir)
	if err != nil {
		return nil, fmt.Errorf("search path error: %w", err)
	}

	maxMatches := opts.MaxMatches
	if maxMatches <= 0 {
		maxMatches = DefaultMaxGrepMatches
	} else if maxMatches > AbsoluteMaxGrepMatches {
		maxMatches = AbsoluteMaxGrepMatches
	}

	regexPattern := opts.Pattern
	if !opts.CaseSensitive && !strings.HasPrefix(regexPattern, "(?i)") {
		regexPattern = "(?i)" + regexPattern
	}

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %w", opts.Pattern, err)
	}

	result := &GrepResult{
		Pattern: opts.Pattern,
		Matches: make([]GrepMatch, 0),
	}

	// Single file search if targeted directly
	if !stat.IsDir() {
		result.FilesSearched = 1
		matches, err := searchFile(workspaceRoot, resolvedDir, re, maxMatches)
		if err != nil {
			return nil, err
		}
		result.Matches = matches
		result.TotalMatches = len(matches)
		result.TokenEstimate = calculateGrepTokens(result.Matches)
		return result, nil
	}

	// Recursive directory walk
	err = filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip unreadable entries
		}

		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}

		// Apply file pattern glob if specified
		if opts.FilePattern != "" {
			matched, globErr := filepath.Match(opts.FilePattern, name)
			if globErr != nil || !matched {
				return nil
			}
		}

		// Skip files larger than 2 MB
		info, err := d.Info()
		if err != nil || info.Size() > MaxSearchFileSize {
			return nil
		}

		result.FilesSearched++
		remaining := maxMatches - len(result.Matches)
		if remaining <= 0 {
			result.Truncated = true
			return fs.SkipAll
		}

		fileMatches, err := searchFile(workspaceRoot, path, re, remaining)
		if err == nil && len(fileMatches) > 0 {
			result.Matches = append(result.Matches, fileMatches...)
			if len(result.Matches) >= maxMatches {
				result.Truncated = true
				return fs.SkipAll
			}
		}

		return nil
	})

	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	result.TotalMatches = len(result.Matches)
	result.TokenEstimate = calculateGrepTokens(result.Matches)
	return result, nil
}

func searchFile(workspaceRoot, filePath string, re *regexp.Regexp, maxMatches int) ([]GrepMatch, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Detect binary content by inspecting first 512 bytes
	header := make([]byte, 512)
	n, _ := f.Read(header)
	if n > 0 && bytes.Contains(header[:n], []byte{0}) {
		return nil, nil // Binary file, skip
	}
	_, _ = f.Seek(0, io.SeekStart)

	relPath, err := filepath.Rel(workspaceRoot, filePath)
	if err != nil {
		relPath = filePath
	}

	var matches []GrepMatch
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			cleanLine := tokenfilter.CleanOutput(line)
			matches = append(matches, GrepMatch{
				File:       filepath.ToSlash(relPath),
				LineNumber: lineNum,
				LineText:   strings.TrimRight(cleanLine, "\r\n"),
			})
			if len(matches) >= maxMatches {
				break
			}
		}
	}

	return matches, scanner.Err()
}

func calculateGrepTokens(matches []GrepMatch) int {
	totalLen := 0
	for _, m := range matches {
		totalLen += len(m.File) + len(m.LineText) + 16
	}
	return tokenfilter.EstimateTokens(strings.Repeat("a", totalLen))
}
