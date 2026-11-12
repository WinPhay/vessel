package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SnapshotManager manages filesystem-level snapshots of a workspace directory.
type SnapshotManager struct {
	mu              sync.RWMutex
	workspacePath   string
	snapshotBaseDir string
	ignoredDirs     []string
}

// NewSnapshotManager initializes a SnapshotManager for the given workspace.
func NewSnapshotManager(workspacePath string) (*SnapshotManager, error) {
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute workspace path: %w", err)
	}

	snapDir := filepath.Join(absPath, ".vessel", "snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	return &SnapshotManager{
		workspacePath:   absPath,
		snapshotBaseDir: snapDir,
		ignoredDirs:     []string{".vessel", ".git"},
	}, nil
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

	if tag == "" {
		return fmt.Errorf("checkpoint tag cannot be empty")
	}

	tagDir := filepath.Join(sm.snapshotBaseDir, tag)
	if err := os.RemoveAll(tagDir); err != nil {
		return fmt.Errorf("failed to clean existing snapshot tag dir: %w", err)
	}

	if err := os.MkdirAll(tagDir, 0755); err != nil {
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
		// Ensures complete isolation even on filesystems without CoW (e.g. ext4) where in-place
		// truncation would corrupt hardlinked inodes.
		return copyFile(srcPath, destPath, info.Mode())
	})
}

// RestoreSnapshot restores the workspace directory to the exact state saved in the snapshot tag.
// Files added after the checkpoint are removed, and modified/deleted files are restored.
func (sm *SnapshotManager) RestoreSnapshot(tag string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

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

		// Clean copy to break any remaining hardlinks and guarantee independent workspace files
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
