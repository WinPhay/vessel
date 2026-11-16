package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotManager_CreateAndRestore(t *testing.T) {
	// Create temporary workspace
	tempDir, err := os.MkdirTemp("", "vessel-snap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create initial workspace files
	fileA := filepath.Join(tempDir, "file_a.txt")
	fileB := filepath.Join(tempDir, "subdir", "file_b.txt")
	ignoredFile := filepath.Join(tempDir, ".git", "config")
	nodeModulesFile := filepath.Join(tempDir, "node_modules", "pkg", "index.js")

	if err := os.WriteFile(fileA, []byte("version 1 of A"), 0644); err != nil {
		t.Fatalf("failed to write fileA: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fileB), 0755); err != nil {
		t.Fatalf("failed to mkdir for fileB: %v", err)
	}
	if err := os.WriteFile(fileB, []byte("version 1 of B"), 0644); err != nil {
		t.Fatalf("failed to write fileB: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(ignoredFile), 0755); err != nil {
		t.Fatalf("failed to mkdir for git: %v", err)
	}
	if err := os.WriteFile(ignoredFile, []byte("git config contents"), 0644); err != nil {
		t.Fatalf("failed to write git file: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(nodeModulesFile), 0755); err != nil {
		t.Fatalf("failed to mkdir for node_modules: %v", err)
	}
	if err := os.WriteFile(nodeModulesFile, []byte("console.log('hi');"), 0644); err != nil {
		t.Fatalf("failed to write node_modules file: %v", err)
	}

	sm, err := NewSnapshotManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create SnapshotManager: %v", err)
	}
	defer os.RemoveAll(sm.snapshotBaseDir)

	// Verify snapshots live outside the workspace tree
	if strings.HasPrefix(sm.snapshotBaseDir, tempDir) {
		t.Errorf("snapshots must be stored outside workspace tree, got: %s", sm.snapshotBaseDir)
	}

	// 1. Create snapshot "v1"
	if err := sm.CreateSnapshot("v1"); err != nil {
		t.Fatalf("failed to create snapshot v1: %v", err)
	}

	// Verify ignored file was NOT copied to snapshot
	snapGitPath := filepath.Join(sm.snapshotBaseDir, "v1", ".git")
	if _, err := os.Stat(snapGitPath); !os.IsNotExist(err) {
		t.Errorf("expected .git to be ignored in snapshot, but found it at %s", snapGitPath)
	}

	// Verify node_modules was NOT copied to snapshot
	snapNodePath := filepath.Join(sm.snapshotBaseDir, "v1", "node_modules")
	if _, err := os.Stat(snapNodePath); !os.IsNotExist(err) {
		t.Errorf("expected node_modules to be ignored in snapshot, but found it at %s", snapNodePath)
	}

	// 2. Mutate workspace (modify fileA, delete fileB, add fileC)
	if err := os.WriteFile(fileA, []byte("corrupted file A!"), 0644); err != nil {
		t.Fatalf("failed to corrupt fileA: %v", err)
	}
	if err := os.Remove(fileB); err != nil {
		t.Fatalf("failed to remove fileB: %v", err)
	}
	fileC := filepath.Join(tempDir, "untracked_file_c.txt")
	if err := os.WriteFile(fileC, []byte("untracked file C"), 0644); err != nil {
		t.Fatalf("failed to create fileC: %v", err)
	}

	// 3. Rollback to "v1"
	if err := sm.RestoreSnapshot("v1"); err != nil {
		t.Fatalf("failed to restore snapshot v1: %v", err)
	}

	// 4. Verify fileA restored
	contentA, err := os.ReadFile(fileA)
	if err != nil {
		t.Fatalf("failed to read fileA after restore: %v", err)
	}
	if string(contentA) != "version 1 of A" {
		t.Errorf("fileA mismatch: expected 'version 1 of A', got '%s'", string(contentA))
	}

	// 5. Verify fileB resurrected
	contentB, err := os.ReadFile(fileB)
	if err != nil {
		t.Fatalf("failed to read fileB after restore: %v", err)
	}
	if string(contentB) != "version 1 of B" {
		t.Errorf("fileB mismatch: expected 'version 1 of B', got '%s'", string(contentB))
	}

	// 6. Verify untracked fileC was pruned
	if _, err := os.Stat(fileC); !os.IsNotExist(err) {
		t.Errorf("expected untracked fileC to be deleted on rollback, but it still exists")
	}

	// 7. Verify .git was preserved untouched
	gitContent, err := os.ReadFile(ignoredFile)
	if err != nil || string(gitContent) != "git config contents" {
		t.Errorf(".git contents damaged during rollback: %v", err)
	}
}

func TestSnapshotManager_NotFoundAndList(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-snap-test-meta-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sm, err := NewSnapshotManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create SnapshotManager: %v", err)
	}

	// Rollback nonexistent tag
	if err := sm.RestoreSnapshot("nonexistent"); err != ErrCheckpointNotFound {
		t.Errorf("expected ErrCheckpointNotFound, got %v", err)
	}

	// Create two snapshots
	if err := sm.CreateSnapshot("snap-1"); err != nil {
		t.Fatalf("failed to create snap-1: %v", err)
	}
	if err := sm.CreateSnapshot("snap-2"); err != nil {
		t.Fatalf("failed to create snap-2: %v", err)
	}

	tags, err := sm.ListSnapshots()
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(tags))
	}

	// Delete snap-1
	if err := sm.DeleteSnapshot("snap-1"); err != nil {
		t.Fatalf("failed to delete snap-1: %v", err)
	}

	tags, _ = sm.ListSnapshots()
	if len(tags) != 1 || tags[0] != "snap-2" {
		t.Errorf("expected only snap-2 remaining, got %v", tags)
	}
}

func TestValidateCheckpointTag(t *testing.T) {
	valid := []string{"v1", "v0.1.0", "snap-2026", "my_checkpoint", "A1.B2"}
	for _, tag := range valid {
		if err := ValidateCheckpointTag(tag); err != nil {
			t.Errorf("ValidateCheckpointTag(%q) unexpected error: %v", tag, err)
		}
	}

	invalid := []string{
		"",
		"   ",
		".",
		"..",
		"../../../victim",
		"tag/slash",
		"tag\\backslash",
		"tag with spaces",
		"tag$dollar",
		"tag;semi",
		"tag`backtick`",
		strings.Repeat("a", 65), // >64 chars
	}
	for _, tag := range invalid {
		if err := ValidateCheckpointTag(tag); err == nil {
			t.Errorf("ValidateCheckpointTag(%q) expected error, got nil", tag)
		}
	}
}

func TestSnapshotManager_DiffSnapshot(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-snap-diff-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fileA := filepath.Join(tempDir, "file_a.txt")
	fileB := filepath.Join(tempDir, "subdir", "file_b.txt")
	fileUnchanged := filepath.Join(tempDir, "same.txt")

	if err := os.WriteFile(fileA, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatalf("failed to write fileA: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fileB), 0755); err != nil {
		t.Fatalf("failed to mkdir for fileB: %v", err)
	}
	if err := os.WriteFile(fileB, []byte("original B"), 0644); err != nil {
		t.Fatalf("failed to write fileB: %v", err)
	}
	if err := os.WriteFile(fileUnchanged, []byte("constant content"), 0644); err != nil {
		t.Fatalf("failed to write fileUnchanged: %v", err)
	}

	sm, err := NewSnapshotManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create SnapshotManager: %v", err)
	}
	defer os.RemoveAll(sm.snapshotBaseDir)

	// Create checkpoint "v1"
	if err := sm.CreateSnapshot("v1"); err != nil {
		t.Fatalf("failed to create checkpoint v1: %v", err)
	}

	// Identical diff before mutations
	diffSame, err := sm.DiffSnapshot("v1", "live", 0)
	if err != nil {
		t.Fatalf("DiffSnapshot identical failed: %v", err)
	}
	if diffSame.FilesChanged != 0 || diffSame.Diff != "" {
		t.Errorf("expected 0 files changed on identical diff, got %d", diffSame.FilesChanged)
	}

	// Mutate workspace: modify fileA, delete fileB, create fileC
	if err := os.WriteFile(fileA, []byte("line1\nline2 modified\nline3\nline4\n"), 0644); err != nil {
		t.Fatalf("failed to modify fileA: %v", err)
	}
	if err := os.Remove(fileB); err != nil {
		t.Fatalf("failed to remove fileB: %v", err)
	}
	fileC := filepath.Join(tempDir, "file_c.txt")
	if err := os.WriteFile(fileC, []byte("new file content\n"), 0644); err != nil {
		t.Fatalf("failed to write fileC: %v", err)
	}

	// Diff v1 against live workspace
	res, err := sm.DiffSnapshot("v1", "live", 0)
	if err != nil {
		t.Fatalf("DiffSnapshot v1 vs live failed: %v", err)
	}

	if res.TagA != "v1" || res.TagB != "live" {
		t.Errorf("expected tags v1->live, got %s->%s", res.TagA, res.TagB)
	}
	if res.FilesChanged != 3 {
		t.Errorf("expected 3 files changed, got %d", res.FilesChanged)
	}
	if res.Additions == 0 || res.Deletions == 0 {
		t.Errorf("expected non-zero additions/deletions, got +%d -%d", res.Additions, res.Deletions)
	}
	if res.TokenEstimate <= 0 {
		t.Errorf("expected positive token estimate, got %d", res.TokenEstimate)
	}

	statusMap := make(map[string]string)
	for _, f := range res.Files {
		statusMap[f.Path] = f.Status
	}
	if statusMap["file_a.txt"] != "modified" {
		t.Errorf("expected file_a.txt to be modified, got %s", statusMap["file_a.txt"])
	}
	if statusMap["subdir/file_b.txt"] != "deleted" {
		t.Errorf("expected subdir/file_b.txt to be deleted, got %s", statusMap["subdir/file_b.txt"])
	}
	if statusMap["file_c.txt"] != "added" {
		t.Errorf("expected file_c.txt to be added, got %s", statusMap["file_c.txt"])
	}

	if !strings.Contains(res.Diff, "--- a/file_a.txt") || !strings.Contains(res.Diff, "+++ b/file_a.txt") {
		t.Errorf("expected unified diff header for file_a.txt, got:\n%s", res.Diff)
	}
	if !strings.Contains(res.Diff, "--- a/subdir/file_b.txt") || !strings.Contains(res.Diff, "+++ /dev/null") {
		t.Errorf("expected unified diff header for subdir/file_b.txt, got:\n%s", res.Diff)
	}
	if !strings.Contains(res.Diff, "--- /dev/null") || !strings.Contains(res.Diff, "+++ b/file_c.txt") {
		t.Errorf("expected unified diff header for file_c.txt, got:\n%s", res.Diff)
	}

	// Create checkpoint "v2"
	if err := sm.CreateSnapshot("v2"); err != nil {
		t.Fatalf("failed to create checkpoint v2: %v", err)
	}

	// Diff v1 against v2
	resV1V2, err := sm.DiffSnapshot("v1", "v2", 0)
	if err != nil {
		t.Fatalf("DiffSnapshot v1 vs v2 failed: %v", err)
	}
	if resV1V2.FilesChanged != 3 {
		t.Errorf("expected 3 files changed between v1 and v2, got %d", resV1V2.FilesChanged)
	}

	// Test maxBytes truncation
	resTrunc, err := sm.DiffSnapshot("v1", "v2", 50)
	if err != nil {
		t.Fatalf("DiffSnapshot with maxBytes failed: %v", err)
	}
	if !resTrunc.Truncated {
		t.Errorf("expected Truncated=true with maxBytes=50")
	}
	if !strings.Contains(resTrunc.Diff, "TRUNCATED") {
		t.Errorf("expected truncation notice in diff, got: %s", resTrunc.Diff)
	}

	// Test errors
	if _, err := sm.DiffSnapshot("nonexistent", "live", 0); err != ErrCheckpointNotFound {
		t.Errorf("expected ErrCheckpointNotFound for nonexistent tagA, got %v", err)
	}
	if _, err := sm.DiffSnapshot("v1", "nonexistent", 0); err != ErrCheckpointNotFound {
		t.Errorf("expected ErrCheckpointNotFound for nonexistent tagB, got %v", err)
	}
	if _, err := sm.DiffSnapshot("invalid/tag", "live", 0); err == nil {
		t.Errorf("expected error for invalid tagA name")
	}
	if _, err := sm.DiffSnapshot("v1", "invalid/tag", 0); err == nil {
		t.Errorf("expected error for invalid tagB name")
	}
}

func BenchmarkSnapshotManager_CreateAndRestore(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "vessel-snap-bench-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create 200 files in various subdirectories
	for i := 0; i < 200; i++ {
		sub := filepath.Join(tempDir, fmt.Sprintf("pkg_%d", i%10))
		_ = os.MkdirAll(sub, 0755)
		filePath := filepath.Join(sub, fmt.Sprintf("source_%d.go", i))
		_ = os.WriteFile(filePath, []byte("package main\nfunc Test() int { return 42 }\n"), 0644)
	}

	sm, err := NewSnapshotManager(tempDir)
	if err != nil {
		b.Fatalf("failed to create SnapshotManager: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tag := fmt.Sprintf("bench-%d", i)
		if err := sm.CreateSnapshot(tag); err != nil {
			b.Fatalf("create failed: %v", err)
		}
		if err := sm.RestoreSnapshot(tag); err != nil {
			b.Fatalf("restore failed: %v", err)
		}
	}
}
