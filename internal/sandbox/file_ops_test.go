package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecurePath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-jail-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Valid paths
	p1, err := ResolveSecurePath(tempDir, "file.txt")
	if err != nil || !strings.HasPrefix(p1, tempDir) {
		t.Errorf("expected valid path inside tempDir, got: %s (err: %v)", p1, err)
	}

	p2, err := ResolveSecurePath(tempDir, "/workspace/src/main.go")
	if err != nil || !strings.HasSuffix(p2, "src/main.go") {
		t.Errorf("expected container prefix stripping, got: %s (err: %v)", p2, err)
	}

	// Path traversal attacks
	_, err = ResolveSecurePath(tempDir, "../../etc/passwd")
	if err != ErrPathEscapesJail {
		t.Errorf("expected ErrPathEscapesJail, got: %v", err)
	}

	_, err = ResolveSecurePath(tempDir, "../../../")
	if err != ErrPathEscapesJail {
		t.Errorf("expected ErrPathEscapesJail for root breakout, got: %v", err)
	}
}

func TestWriteAndReadFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-fileops-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	content := "Line 1: Alpha\nLine 2: Beta\nLine 3: Gamma\nLine 4: Delta\nLine 5: Epsilon"

	// 1. Write file
	wRes, err := WriteFile(tempDir, "subdir/test.txt", content, true)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if !wRes.Success || wRes.BytesWritten != len(content) {
		t.Errorf("unexpected write result: %+v", wRes)
	}

	// 2. Overwrite protection
	_, err = WriteFile(tempDir, "subdir/test.txt", "new content", false)
	if err != ErrFileExists {
		t.Errorf("expected ErrFileExists, got: %v", err)
	}

	// 3. Read whole file
	rRes, err := ReadFile(tempDir, "subdir/test.txt", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if rRes.TotalLines != 5 || rRes.Content != content {
		t.Errorf("ReadFile mismatch: %+v", rRes)
	}

	// 4. Read slice (lines 2 to 4)
	rSlice, err := ReadFile(tempDir, "subdir/test.txt", 2, 4)
	if err != nil {
		t.Fatalf("ReadFile slice failed: %v", err)
	}
	expectedSlice := "Line 2: Beta\nLine 3: Gamma\nLine 4: Delta"
	if rSlice.Content != expectedSlice || rSlice.StartLine != 2 || rSlice.EndLine != 4 {
		t.Errorf("slice mismatch: expected %q, got %q", expectedSlice, rSlice.Content)
	}

	// 5. Read directory as file should fail
	_, err = ReadFile(tempDir, "subdir", 0, 0)
	if err != ErrIsDirectory {
		t.Errorf("expected ErrIsDirectory, got: %v", err)
	}
}

func TestListDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-listdir-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	_ = os.MkdirAll(filepath.Join(tempDir, "pkg", "sub"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "root.go"), []byte("package main"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "lib.go"), []byte("package pkg"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "sub", "util.go"), []byte("package sub"), 0644)
	// Hidden / ignored
	_ = os.MkdirAll(filepath.Join(tempDir, ".git"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, ".git", "HEAD"), []byte("ref: main"), 0644)

	// Shallow listing
	res, err := ListDir(tempDir, ".", false, 1)
	if err != nil {
		t.Fatalf("ListDir failed: %v", err)
	}
	if res.Count != 2 { // root.go and pkg/ (skipping .git)
		t.Errorf("expected 2 items, got %d: %+v", res.Count, res.Entries)
	}

	// Recursive listing
	resRec, err := ListDir(tempDir, ".", true, 3)
	if err != nil {
		t.Fatalf("ListDir recursive failed: %v", err)
	}
	if resRec.Count < 4 {
		t.Errorf("expected at least 4 items, got %d: %+v", resRec.Count, resRec.Entries)
	}
}

func TestResolveSecurePath_SymlinkedParent(t *testing.T) {
	tempWorkspace, err := os.MkdirTemp("", "vessel-workspace-*")
	if err != nil {
		t.Fatalf("failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempWorkspace)

	outsideDir, err := os.MkdirTemp("", "vessel-outside-*")
	if err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	defer os.RemoveAll(outsideDir)

	// Create symlink inside workspace pointing to outsideDir
	symlinkPath := filepath.Join(tempWorkspace, "linked_dir")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// 1. Attempting to write a non-existent file through the symlinked parent must be rejected
	_, err = ResolveSecurePath(tempWorkspace, "linked_dir/new_file.txt")
	if err != ErrPathEscapesJail {
		t.Errorf("expected ErrPathEscapesJail when writing through symlinked parent, got: %v", err)
	}

	// 2. Writing via WriteFile through symlink must fail
	_, err = WriteFile(tempWorkspace, "linked_dir/evil.sh", "malicious", true)
	if err != ErrPathEscapesJail {
		t.Errorf("WriteFile through symlinked parent expected ErrPathEscapesJail, got: %v", err)
	}
}
