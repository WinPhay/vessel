package sandbox

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

var (
	ErrInvalidCheckpointTag = errors.New("invalid checkpoint tag")
	reValidTag              = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ValidateCheckpointTag ensures tag is a safe single-path-component tag without directory traversal.
func ValidateCheckpointTag(tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return fmt.Errorf("%w: tag cannot be empty", ErrInvalidCheckpointTag)
	}
	if len(tag) > 64 {
		return fmt.Errorf("%w: tag length cannot exceed 64 characters", ErrInvalidCheckpointTag)
	}
	if tag == "." || tag == ".." {
		return fmt.Errorf("%w: tag cannot be '.' or '..'", ErrInvalidCheckpointTag)
	}
	if strings.ContainsAny(tag, `/\`) {
		return fmt.Errorf("%w: tag cannot contain path separators", ErrInvalidCheckpointTag)
	}
	if !reValidTag.MatchString(tag) {
		return fmt.Errorf("%w: tag %q must contain only alphanumeric characters, '.', '_', and '-'", ErrInvalidCheckpointTag, tag)
	}
	return nil
}

// GetDefaultSnapshotDir returns an isolated snapshot storage directory outside the workspace mount.
func GetDefaultSnapshotDir(workspacePath string) (string, error) {
	if custom := os.Getenv("VESSEL_SNAPSHOT_DIR"); custom != "" {
		return custom, nil
	}

	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256([]byte(absPath))
	hashHex := hex.EncodeToString(hash[:8]) // 16-char unique identifier

	var baseDir string
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		baseDir = filepath.Join(dataHome, "vessel", "snapshots", hashHex)
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		baseDir = filepath.Join(home, ".local", "share", "vessel", "snapshots", hashHex)
	} else {
		baseDir = filepath.Join(os.TempDir(), "vessel-snapshots", hashHex)
	}

	return baseDir, nil
}

// SnapshotManager manages filesystem-level snapshots of a workspace directory.
type SnapshotManager struct {
	mu              sync.RWMutex
	workspacePath   string
	snapshotBaseDir string
	ignoredDirs     []string
}

// NewSnapshotManager initializes a SnapshotManager for the given workspace.
// Snapshots are stored outside the workspace tree to prevent the sandboxed container from tampering with them.
func NewSnapshotManager(workspacePath string) (*SnapshotManager, error) {
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute workspace path: %w", err)
	}

	snapDir, err := GetDefaultSnapshotDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to determine snapshot directory: %w", err)
	}

	if err := os.MkdirAll(snapDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	sm := &SnapshotManager{
		workspacePath:   absPath,
		snapshotBaseDir: snapDir,
		ignoredDirs: []string{
			".vessel", ".git", "node_modules", ".tmp", "dist",
			"build", ".next", ".cache", "target", "vendor",
		},
	}
	sm.loadVesselIgnore()

	return sm, nil
}

// loadVesselIgnore reads custom ignore patterns from .vesselignore if present in workspace root.
func (sm *SnapshotManager) loadVesselIgnore() {
	ignoreFile := filepath.Join(sm.workspacePath, ".vesselignore")
	f, err := os.Open(ignoreFile)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sm.ignoredDirs = append(sm.ignoredDirs, line)
	}
}

// isIgnored checks whether a path relative to the workspace root should be skipped.
func (sm *SnapshotManager) isIgnored(relPath string) bool {
	if relPath == "." || relPath == "" {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, part := range parts {
		for _, ignored := range sm.ignoredDirs {
			if part == ignored {
				return true
			}
		}
	}
	return false
}

// CreateSnapshot creates a fast hardlinked (or copied) snapshot of the workspace under the given tag.
func (sm *SnapshotManager) CreateSnapshot(tag string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := ValidateCheckpointTag(tag); err != nil {
		return err
	}

	tagDir := filepath.Join(sm.snapshotBaseDir, tag)
	if err := os.RemoveAll(tagDir); err != nil {
		return fmt.Errorf("failed to clean existing snapshot tag dir: %w", err)
	}

	if err := os.MkdirAll(tagDir, 0700); err != nil {
		return fmt.Errorf("failed to create tag directory: %w", err)
	}

	return filepath.Walk(sm.workspacePath, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(sm.workspacePath, srcPath)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		if sm.isIgnored(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		destPath := filepath.Join(tagDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %w", srcPath, err)
			}
			return os.Symlink(linkTarget, destPath)
		}

		// Fast kernel-level file copy (delegates to copy_file_range / sendfile in Go runtime)
		return copyFile(srcPath, destPath, info.Mode())
	})
}

// RestoreSnapshot restores the workspace directory to the exact state saved in the snapshot tag.
// Files added after the checkpoint are removed, and modified/deleted files are restored.
func (sm *SnapshotManager) RestoreSnapshot(tag string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := ValidateCheckpointTag(tag); err != nil {
		return err
	}

	tagDir := filepath.Join(sm.snapshotBaseDir, tag)
	if _, err := os.Stat(tagDir); os.IsNotExist(err) {
		return ErrCheckpointNotFound
	}

	// Step 1: Prune files/directories in workspace that do not exist in the snapshot
	err := filepath.Walk(sm.workspacePath, func(wPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(sm.workspacePath, wPath)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		if sm.isIgnored(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		snapPath := filepath.Join(tagDir, relPath)
		if _, err := os.Lstat(snapPath); os.IsNotExist(err) {
			// File or dir was created after the snapshot, delete it
			if err := os.RemoveAll(wPath); err != nil {
				return fmt.Errorf("failed to remove untracked path %s: %w", wPath, err)
			}
			if info.IsDir() {
				return filepath.SkipDir
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to prune untracked files during rollback: %w", err)
	}

	// Step 2: Copy all files and directories from the snapshot back into the workspace
	return filepath.Walk(tagDir, func(snapPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(tagDir, snapPath)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		destPath := filepath.Join(sm.workspacePath, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(snapPath)
			if err != nil {
				return err
			}
			_ = os.Remove(destPath)
			return os.Symlink(linkTarget, destPath)
		}

		_ = os.Remove(destPath)
		return copyFile(snapPath, destPath, info.Mode())
	})
}

// ListSnapshots returns all stored snapshot tags.
func (sm *SnapshotManager) ListSnapshots() ([]string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entries, err := os.ReadDir(sm.snapshotBaseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	var tags []string
	for _, entry := range entries {
		if entry.IsDir() {
			tags = append(tags, entry.Name())
		}
	}
	return tags, nil
}

// DeleteSnapshot removes a snapshot tag directory.
func (sm *SnapshotManager) DeleteSnapshot(tag string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := ValidateCheckpointTag(tag); err != nil {
		return err
	}

	tagDir := filepath.Join(sm.snapshotBaseDir, tag)
	if _, err := os.Stat(tagDir); os.IsNotExist(err) {
		return ErrCheckpointNotFound
	}
	return os.RemoveAll(tagDir)
}

func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return nil
}

// CheckpointFileDiff records changes to an individual file between checkpoints.
type CheckpointFileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "modified", "deleted"
}

// CheckpointDiffResult contains the overall delta and unified diff between checkpoints.
type CheckpointDiffResult struct {
	TagA          string               `json:"tag_a"`
	TagB          string               `json:"tag_b"`
	FilesChanged  int                  `json:"files_changed"`
	Additions     int                  `json:"additions"`
	Deletions     int                  `json:"deletions"`
	Files         []CheckpointFileDiff `json:"files"`
	Diff          string               `json:"diff"`
	TokenEstimate int                  `json:"token_estimate"`
	Truncated     bool                 `json:"truncated"`
}

func isBinary(data []byte) bool {
	limit := len(data)
	if limit > 8000 {
		limit = 8000
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func computeLCS(a, b []string) [][]int {
	n := len(a)
	m := len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				if dp[i-1][j] >= dp[i][j-1] {
					dp[i][j] = dp[i-1][j]
				} else {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}
	return dp
}

type diffOp struct {
	op   rune
	line string
}

func generateUnifiedDiff(filePath string, oldData, newData []byte) (string, int, int) {
	if bytes.Equal(oldData, newData) {
		return "", 0, 0
	}

	if isBinary(oldData) || isBinary(newData) {
		return fmt.Sprintf("Binary files a/%s and b/%s differ\n", filePath, filePath), 0, 0
	}

	oldLines := splitLines(string(oldData))
	newLines := splitLines(string(newData))

	if len(oldLines) == 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("--- /dev/null\n+++ b/%s\n", filePath))
		sb.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(newLines)))
		for _, l := range newLines {
			sb.WriteString("+" + l + "\n")
		}
		return sb.String(), len(newLines), 0
	}

	if len(newLines) == 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("--- a/%s\n+++ /dev/null\n", filePath))
		sb.WriteString(fmt.Sprintf("@@ -1,%d +0,0 @@\n", len(oldLines)))
		for _, l := range oldLines {
			sb.WriteString("-" + l + "\n")
		}
		return sb.String(), 0, len(oldLines)
	}

	dp := computeLCS(oldLines, newLines)
	var ops []diffOp
	i, j := len(oldLines), len(newLines)
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			ops = append(ops, diffOp{op: ' ', line: oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, diffOp{op: '+', line: newLines[j-1]})
			j--
		} else if i > 0 && (j == 0 || dp[i][j-1] < dp[i-1][j]) {
			ops = append(ops, diffOp{op: '-', line: oldLines[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	additions := 0
	deletions := 0
	for _, op := range ops {
		if op.op == '+' {
			additions++
		} else if op.op == '-' {
			deletions++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", filePath, filePath))
	sb.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines)))
	for _, op := range ops {
		sb.WriteRune(op.op)
		sb.WriteString(op.line)
		sb.WriteByte('\n')
	}

	return sb.String(), additions, deletions
}

// scanFiles scans a directory recursively and returns all non-ignored relative file paths.
func (sm *SnapshotManager) scanFiles(baseDir string) (map[string]os.FileInfo, error) {
	files := make(map[string]os.FileInfo)
	err := filepath.Walk(baseDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if sm.isIgnored(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.IsDir() {
			files[filepath.ToSlash(rel)] = info
		}
		return nil
	})
	return files, err
}

// DiffSnapshot compares two snapshots or compares a snapshot against the current live workspace.
func (sm *SnapshotManager) DiffSnapshot(tagA, tagB string, maxBytes int) (*CheckpointDiffResult, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if err := ValidateCheckpointTag(tagA); err != nil {
		return nil, fmt.Errorf("invalid tagA: %w", err)
	}

	dirA := filepath.Join(sm.snapshotBaseDir, tagA)
	if _, err := os.Stat(dirA); os.IsNotExist(err) {
		return nil, ErrCheckpointNotFound
	}

	tagBName := tagB
	var dirB string
	if tagB == "" || tagB == "live" || tagB == "current" {
		tagBName = "live"
		dirB = sm.workspacePath
	} else {
		if err := ValidateCheckpointTag(tagB); err != nil {
			return nil, fmt.Errorf("invalid tagB: %w", err)
		}
		dirB = filepath.Join(sm.snapshotBaseDir, tagB)
		if _, err := os.Stat(dirB); os.IsNotExist(err) {
			return nil, ErrCheckpointNotFound
		}
	}

	filesA, err := sm.scanFiles(dirA)
	if err != nil {
		return nil, fmt.Errorf("failed to scan files for tagA %s: %w", tagA, err)
	}

	filesB, err := sm.scanFiles(dirB)
	if err != nil {
		return nil, fmt.Errorf("failed to scan files for tagB %s: %w", tagBName, err)
	}

	allPathsMap := make(map[string]bool)
	for p := range filesA {
		allPathsMap[p] = true
	}
	for p := range filesB {
		allPathsMap[p] = true
	}

	var allPaths []string
	for p := range allPathsMap {
		allPaths = append(allPaths, p)
	}
	sort.Strings(allPaths)

	var (
		fileDiffs   []CheckpointFileDiff
		diffBuilder strings.Builder
		additions   int
		deletions   int
	)

	for _, relPath := range allPaths {
		_, inA := filesA[relPath]
		_, inB := filesB[relPath]

		pathA := filepath.Join(dirA, filepath.FromSlash(relPath))
		pathB := filepath.Join(dirB, filepath.FromSlash(relPath))

		if !inA && inB {
			// Added in B
			dataB, err := os.ReadFile(pathB)
			if err != nil {
				continue
			}
			fileDiff, adds, dels := generateUnifiedDiff(relPath, nil, dataB)
			diffBuilder.WriteString(fileDiff)
			additions += adds
			deletions += dels
			fileDiffs = append(fileDiffs, CheckpointFileDiff{Path: relPath, Status: "added"})
		} else if inA && !inB {
			// Deleted in B
			dataA, err := os.ReadFile(pathA)
			if err != nil {
				continue
			}
			fileDiff, adds, dels := generateUnifiedDiff(relPath, dataA, nil)
			diffBuilder.WriteString(fileDiff)
			additions += adds
			deletions += dels
			fileDiffs = append(fileDiffs, CheckpointFileDiff{Path: relPath, Status: "deleted"})
		} else {
			// In both: check if modified
			dataA, err := os.ReadFile(pathA)
			if err != nil {
				continue
			}
			dataB, err := os.ReadFile(pathB)
			if err != nil {
				continue
			}
			if bytes.Equal(dataA, dataB) {
				continue
			}
			fileDiff, adds, dels := generateUnifiedDiff(relPath, dataA, dataB)
			diffBuilder.WriteString(fileDiff)
			additions += adds
			deletions += dels
			fileDiffs = append(fileDiffs, CheckpointFileDiff{Path: relPath, Status: "modified"})
		}
	}

	rawDiff := diffBuilder.String()
	cleaned := tokenfilter.CleanOutput(rawDiff)
	truncated := false
	if maxBytes > 0 && len(cleaned) > maxBytes {
		cleaned, truncated = tokenfilter.TruncateOutput(cleaned, maxBytes)
	}

	return &CheckpointDiffResult{
		TagA:          tagA,
		TagB:          tagBName,
		FilesChanged:  len(fileDiffs),
		Additions:     additions,
		Deletions:     deletions,
		Files:         fileDiffs,
		Diff:          cleaned,
		TokenEstimate: tokenfilter.EstimateTokens(cleaned),
		Truncated:     truncated,
	}, nil
}
