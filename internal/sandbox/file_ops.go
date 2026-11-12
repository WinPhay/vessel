package sandbox

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

var (
	ErrPathEscapesJail = errors.New("path escapes workspace sandbox")
	ErrFileExists      = errors.New("file already exists and overwrite is false")
	ErrIsDirectory     = errors.New("target path is a directory")
)

// ReadFileResult holds the contents and metadata of a read file.
type ReadFileResult struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	TotalLines    int    `json:"total_lines"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	TokenEstimate int    `json:"token_estimate"`
}

// WriteFileResult holds the result of writing a file.
type WriteFileResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
	Success      bool   `json:"success"`
}

// DirEntry represents a single item in a directory listing.
type DirEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	SizeBytes int64  `json:"size_bytes"`
}

// ListDirResult holds the directory listing.
type ListDirResult struct {
	Path    string     `json:"path"`
	Entries []DirEntry `json:"entries"`
	Count   int        `json:"count"`
}

// ResolveSecurePath validates that userPath stays strictly within workspaceRoot.
func ResolveSecurePath(workspaceRoot, userPath string) (string, error) {
	cleanRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root: %w", err)
	}

	cleanPath := filepath.Clean(userPath)
	// Strip container /workspace prefix if present
	cleanPath = strings.TrimPrefix(cleanPath, "/workspace")
	cleanPath = strings.TrimPrefix(cleanPath, "workspace")
	cleanPath = strings.TrimPrefix(cleanPath, string(filepath.Separator))

	resolved := filepath.Join(cleanRoot, cleanPath)
	resolved = filepath.Clean(resolved)

	rel, err := filepath.Rel(cleanRoot, resolved)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", ErrPathEscapesJail
	}

	// Resolve real path of cleanRoot in case root itself is a symlink (e.g. /tmp -> /private/tmp on macOS)
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		realRoot = cleanRoot
	}

	// Verify symlinks don't escape sandbox by resolving the deepest existing ancestor
	curr := resolved
	for {
		if _, err := os.Lstat(curr); err == nil {
			realCurr, err := filepath.EvalSymlinks(curr)
			if err == nil {
				realRel, err := filepath.Rel(realRoot, realCurr)
				if err != nil || strings.HasPrefix(realRel, "..") || realRel == ".." {
					return "", ErrPathEscapesJail
				}
			}
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || len(parent) < len(cleanRoot) {
			break
		}
		curr = parent
	}

	return resolved, nil
}

// ReadFile securely reads a file within workspaceRoot with optional line pagination.
func ReadFile(workspaceRoot, userPath string, startLine, endLine int) (*ReadFileResult, error) {
	targetPath, err := ResolveSecurePath(workspaceRoot, userPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, ErrIsDirectory
	}

	file, err := os.Open(targetPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var allLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading file: %w", err)
	}

	total := len(allLines)
	if total == 0 {
		return &ReadFileResult{
			Path:          userPath,
			Content:       "",
			TotalLines:    0,
			StartLine:     0,
			EndLine:       0,
			TokenEstimate: 0,
		}, nil
	}

	start := startLine
	if start <= 0 {
		start = 1
	}
	end := endLine
	if end <= 0 || end > total {
		end = total
	}
	if start > total {
		start = total
	}
	if end < start {
		end = start
	}

	selected := allLines[start-1 : end]
	content := strings.Join(selected, "\n")
	tokenEst := tokenfilter.EstimateTokens(content)

	return &ReadFileResult{
		Path:          userPath,
		Content:       content,
		TotalLines:    total,
		StartLine:     start,
		EndLine:       end,
		TokenEstimate: tokenEst,
	}, nil
}

// WriteFile securely writes content to a file inside workspaceRoot using atomic replace.
func WriteFile(workspaceRoot, userPath, content string, overwrite bool) (*WriteFileResult, error) {
	targetPath, err := ResolveSecurePath(workspaceRoot, userPath)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(targetPath); err == nil {
		if !overwrite {
			return nil, ErrFileExists
		}
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed creating parent directories: %w", err)
	}

	// Atomic write via temporary file
	tmpFile, err := os.CreateTemp(dir, ".vessel-write-*")
	if err != nil {
		return nil, fmt.Errorf("failed creating temp file for atomic write: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	n, err := tmpFile.WriteString(content)
	if err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("failed writing to temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed closing temp file: %w", err)
	}

	if err := os.Chmod(tmpName, 0644); err != nil {
		return nil, err
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return nil, fmt.Errorf("failed atomic rename to %s: %w", targetPath, err)
	}

	return &WriteFileResult{
		Path:         userPath,
		BytesWritten: n,
		Success:      true,
	}, nil
}

// ListDir securely lists directory contents inside workspaceRoot.
func ListDir(workspaceRoot, userPath string, recursive bool, maxDepth int) (*ListDirResult, error) {
	targetPath, err := ResolveSecurePath(workspaceRoot, userPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("target path is not a directory: %s", userPath)
	}

	if maxDepth <= 0 {
		if recursive {
			maxDepth = 3
		} else {
			maxDepth = 1
		}
	}

	var entries []DirEntry

	err = filepath.WalkDir(targetPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if path == targetPath {
			return nil
		}

		name := d.Name()
		if name == ".vessel" || name == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relToTarget, _ := filepath.Rel(targetPath, path)
		depth := len(strings.Split(filepath.ToSlash(relToTarget), "/"))
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		cleanRoot, _ := filepath.Abs(workspaceRoot)
		relToRoot, _ := filepath.Rel(cleanRoot, path)

		size := int64(0)
		if !d.IsDir() {
			if fInfo, err := d.Info(); err == nil {
				size = fInfo.Size()
			}
		}

		entries = append(entries, DirEntry{
			Name:      name,
			Path:      filepath.ToSlash(relToRoot),
			IsDir:     d.IsDir(),
			SizeBytes: size,
		})

		if !recursive && d.IsDir() {
			return filepath.SkipDir
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed listing directory: %w", err)
	}

	return &ListDirResult{
		Path:    userPath,
		Entries: entries,
		Count:   len(entries),
	}, nil
}
