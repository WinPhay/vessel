package sandbox

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
