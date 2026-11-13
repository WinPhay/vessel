package sandbox

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrEmptyPatch       = errors.New("patch content cannot be empty")
	ErrNoHunksFound     = errors.New("no valid diff hunks found in patch")
	ErrHunkMatchFailure = errors.New("patch hunk context failed to match target file content")
	ErrNoTargetFile     = errors.New("unable to determine target file path from patch header")
)

var hunkHeaderRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// PatchResult holds the outcome of applying a unified diff patch.
type PatchResult struct {
	File         string `json:"file"`
	HunksApplied int    `json:"hunks_applied"`
	LinesAdded   int    `json:"lines_added"`
	LinesDeleted int    `json:"lines_deleted"`
	DryRun       bool   `json:"dry_run"`
	Status       string `json:"status"`
}

type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []string // Raw hunk lines with prefix ' ', '+', '-'
}

// ApplyPatch applies a unified diff to a target file in workspaceRoot.
func ApplyPatch(workspaceRoot, patchContent string, dryRun bool) (*PatchResult, error) {
	patchContent = strings.TrimSpace(patchContent)
	if patchContent == "" {
		return nil, ErrEmptyPatch
	}

	targetPath, hunks, err := parseUnifiedDiff(patchContent)
	if err != nil {
		return nil, err
	}
	if len(hunks) == 0 {
		return nil, ErrNoHunksFound
	}

	// Validate target path is jailed in workspace
	resolvedTarget, err := ResolveSecurePath(workspaceRoot, targetPath)
	if err != nil {
		return nil, err
	}

	// Read existing file content
	contentBytes, err := os.ReadFile(resolvedTarget)
	if err != nil {
		if os.IsNotExist(err) {
			// If file doesn't exist, all hunks must be entirely additions
			contentBytes = []byte{}
		} else {
			return nil, fmt.Errorf("failed reading target file %s: %w", targetPath, err)
		}
	}

	rawText := string(contentBytes)
	var originalLines []string
	if len(rawText) > 0 {
		// Normalize CRLF to LF
		normalized := strings.ReplaceAll(rawText, "\r\n", "\n")
		originalLines = strings.Split(normalized, "\n")
		// Trim trailing empty line from split if file ended with newline
		if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
			originalLines = originalLines[:len(originalLines)-1]
		}
	}

	currentLines := make([]string, len(originalLines))
	copy(currentLines, originalLines)

	linesAdded := 0
	linesDeleted := 0
	lineOffset := 0

	for hunkIdx, hunk := range hunks {
		// Extract expected context and deletions
		var expectedContext []string
		var replacement []string

		for _, hl := range hunk.lines {
			if len(hl) == 0 {
				expectedContext = append(expectedContext, "")
				replacement = append(replacement, "")
				continue
			}
			prefix := hl[0]
			text := hl[1:]
			switch prefix {
			case ' ':
				expectedContext = append(expectedContext, text)
				replacement = append(replacement, text)
			case '-':
				expectedContext = append(expectedContext, text)
				linesDeleted++
			case '+':
				replacement = append(replacement, text)
				linesAdded++
			}
		}

		// Find where expectedContext matches currentLines
		matchIdx := findHunkMatch(currentLines, expectedContext, hunk.oldStart-1+lineOffset)
		if matchIdx == -1 {
			return nil, fmt.Errorf("%w: hunk #%d at expected line %d", ErrHunkMatchFailure, hunkIdx+1, hunk.oldStart)
		}

		// Replace matched region with replacement lines
		endIdx := matchIdx + len(expectedContext)
		newCurrent := make([]string, 0, len(currentLines)-len(expectedContext)+len(replacement))
		newCurrent = append(newCurrent, currentLines[:matchIdx]...)
		newCurrent = append(newCurrent, replacement...)
		if endIdx < len(currentLines) {
			newCurrent = append(newCurrent, currentLines[endIdx:]...)
		}

		lineOffset += len(replacement) - len(expectedContext)
		currentLines = newCurrent
	}

	finalContent := strings.Join(currentLines, "\n")
	if len(currentLines) > 0 {
		finalContent += "\n"
	}

	relPath, _ := filepath.Rel(workspaceRoot, resolvedTarget)
	if relPath == "" {
		relPath = targetPath
	}

	if dryRun {
		return &PatchResult{
			File:         filepath.ToSlash(relPath),
			HunksApplied: len(hunks),
			LinesAdded:   linesAdded,
			LinesDeleted: linesDeleted,
			DryRun:       true,
			Status:       "verified (dry-run)",
		}, nil
	}

	_, err = WriteFile(workspaceRoot, relPath, finalContent, true)
	if err != nil {
		return nil, fmt.Errorf("failed writing patched file: %w", err)
	}

	return &PatchResult{
		File:         filepath.ToSlash(relPath),
		HunksApplied: len(hunks),
		LinesAdded:   linesAdded,
		LinesDeleted: linesDeleted,
		DryRun:       false,
		Status:       "applied",
	}, nil
}

func parseUnifiedDiff(patch string) (string, []diffHunk, error) {
	scanner := bufio.NewScanner(strings.NewReader(patch))
	var targetFile string
	var hunks []diffHunk
	var currentHunk *diffHunk

	for scanner.Scan() {
		line := scanner.Text()

		// Extract target file path
		if strings.HasPrefix(line, "+++ ") {
			rawPath := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			rawPath = strings.TrimPrefix(rawPath, "b/")
			rawPath = strings.TrimPrefix(rawPath, "a/")
			// Remove timestamp comments if present
			if idx := strings.IndexAny(rawPath, "\t "); idx != -1 {
				rawPath = rawPath[:idx]
			}
			targetFile = strings.Trim(rawPath, `"'`)
			continue
		} else if strings.HasPrefix(line, "--- ") && targetFile == "" {
			rawPath := strings.TrimSpace(strings.TrimPrefix(line, "--- "))
			rawPath = strings.TrimPrefix(rawPath, "a/")
			rawPath = strings.TrimPrefix(rawPath, "b/")
			if idx := strings.IndexAny(rawPath, "\t "); idx != -1 {
				rawPath = rawPath[:idx]
			}
			targetFile = strings.Trim(rawPath, `"'`)
			continue
		}

		// Detect hunk header
		if strings.HasPrefix(line, "@@ ") {
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
			}
			matches := hunkHeaderRe.FindStringSubmatch(line)
			if len(matches) < 4 {
				continue
			}
			oldStart, _ := strconv.Atoi(matches[1])
			oldCount := 1
			if matches[2] != "" {
				oldCount, _ = strconv.Atoi(matches[2])
			}
			newStart, _ := strconv.Atoi(matches[3])
			newCount := 1
			if matches[4] != "" {
				newCount, _ = strconv.Atoi(matches[4])
			}

			currentHunk = &diffHunk{
				oldStart: oldStart,
				oldCount: oldCount,
				newStart: newStart,
				newCount: newCount,
				lines:    make([]string, 0),
			}
			continue
		}

		if currentHunk != nil {
			if len(line) > 0 && (line[0] == ' ' || line[0] == '+' || line[0] == '-') {
				currentHunk.lines = append(currentHunk.lines, line)
			} else if line == "" {
				currentHunk.lines = append(currentHunk.lines, " ")
			}
		}
	}

	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	if targetFile == "" {
		return "", nil, ErrNoTargetFile
	}

	return filepath.Clean(targetFile), hunks, scanner.Err()
}

func findHunkMatch(lines, expected []string, hintIdx int) int {
	if len(expected) == 0 {
		if hintIdx < 0 {
			return 0
		}
		if hintIdx > len(lines) {
			return len(lines)
		}
		return hintIdx
	}

	// 1. Try exact hint position first
	if hintIdx >= 0 && hintIdx+len(expected) <= len(lines) {
		if matchRegion(lines[hintIdx:hintIdx+len(expected)], expected) {
			return hintIdx
		}
	}

	// 2. Search outward with fuzz window up to full file
	maxOffset := len(lines)
	for offset := 1; offset <= maxOffset; offset++ {
		// check hintIdx - offset
		pos1 := hintIdx - offset
		if pos1 >= 0 && pos1+len(expected) <= len(lines) {
			if matchRegion(lines[pos1:pos1+len(expected)], expected) {
				return pos1
			}
		}
		// check hintIdx + offset
		pos2 := hintIdx + offset
		if pos2 >= 0 && pos2+len(expected) <= len(lines) {
			if matchRegion(lines[pos2:pos2+len(expected)], expected) {
				return pos2
			}
		}
	}

	return -1
}

func matchRegion(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if strings.TrimRight(actual[i], "\r") != strings.TrimRight(expected[i], "\r") {
			return false
		}
	}
	return true
}
