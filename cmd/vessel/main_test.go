package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureOutput(f func()) (string, string) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()

	os.Stdout = wOut
	os.Stderr = wErr

	outC := make(chan string)
	errC := make(chan string)

	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rOut)
		outC <- buf.String()
	}()

	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rErr)
		errC <- buf.String()
	}()

	f()

	_ = wOut.Close()
	_ = wErr.Close()

	os.Stdout = origStdout
	os.Stderr = origStderr

	return <-outC, <-errC
}

func TestCLI_PrintUsage(t *testing.T) {
	stdout, _ := captureOutput(func() {
		printUsage()
	})

	if !strings.Contains(stdout, "Vessel v") {
		t.Errorf("expected usage to mention Vessel v, got: %s", stdout)
	}
	if !strings.Contains(stdout, "checkpoint") || !strings.Contains(stdout, "rollback") || !strings.Contains(stdout, "status") || !strings.Contains(stdout, "process") {
		t.Errorf("usage missing new subcommands: %s", stdout)
	}
	if !strings.Contains(stdout, "runsc") || !strings.Contains(stdout, "OCI Runtimes") {
		t.Errorf("usage missing runtime documentation: %s", stdout)
	}
	if !strings.Contains(stdout, "--secret") || !strings.Contains(stdout, "--mask-secrets") {
		t.Errorf("usage missing secret masking documentation: %s", stdout)
	}
	if !strings.Contains(stdout, "service") || !strings.Contains(stdout, "--service") {
		t.Errorf("usage missing sidecar service documentation: %s", stdout)
	}
	if !strings.Contains(stdout, "worktree") || !strings.Contains(stdout, "--worktree") {
		t.Errorf("usage missing git worktree shield documentation: %s", stdout)
	}
}

func TestCLI_CheckpointAndRollback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-cli-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("initial state"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// 1. Create checkpoint via runCheckpoint
	stdout, _ := captureOutput(func() {
		runCheckpoint([]string{"--workspace", tempDir, "v1"})
	})
	if !strings.Contains(stdout, "created successfully") {
		t.Errorf("unexpected checkpoint output: %s", stdout)
	}

	// 2. List checkpoints
	stdout, _ = captureOutput(func() {
		runCheckpoint([]string{"--workspace", tempDir, "list"})
	})
	if !strings.Contains(stdout, "v1") {
		t.Errorf("expected list to include v1, got: %s", stdout)
	}

	// 3. Mutate file
	if err := os.WriteFile(testFile, []byte("corrupted state"), 0644); err != nil {
		t.Fatalf("failed to mutate file: %v", err)
	}

	// 4. Rollback to v1
	stdout, _ = captureOutput(func() {
		runRollback([]string{"--workspace", tempDir, "v1"})
	})
	if !strings.Contains(stdout, "restored to checkpoint 'v1'") {
		t.Errorf("unexpected rollback output: %s", stdout)
	}

	// 5. Verify file contents restored
	data, err := os.ReadFile(testFile)
	if err != nil || string(data) != "initial state" {
		t.Errorf("expected 'initial state', got %q", string(data))
	}
}

func TestCLI_Status(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-cli-status-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stdout, _ := captureOutput(func() {
		runStatus([]string{"--workspace", tempDir})
	})

	if !strings.Contains(stdout, "Vessel Engine Status") {
		t.Errorf("expected status header, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Workspace:") {
		t.Errorf("expected workspace in status, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Drivers:") || !strings.Contains(stdout, "OCI Runtime:") {
		t.Errorf("expected drivers/runtime in status, got: %s", stdout)
	}
}

func TestCLI_InterspersedFlags(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-cli-interspersed-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "file.txt")
	_ = os.WriteFile(testFile, []byte("v1 content"), 0644)

	// Flag AFTER positional arg (fixes Bug 5)
	stdout, _ := captureOutput(func() {
		runCheckpoint([]string{"v1", "--workspace", tempDir})
	})
	if !strings.Contains(stdout, "created successfully") {
		t.Errorf("interspersed flag checkpoint failed: %s", stdout)
	}

	_ = os.WriteFile(testFile, []byte("corrupted"), 0644)

	// Rollback with flag AFTER positional arg
	stdout, _ = captureOutput(func() {
		runRollback([]string{"v1", "--workspace", tempDir})
	})
	if !strings.Contains(stdout, "restored to checkpoint 'v1'") {
		t.Errorf("interspersed flag rollback failed: %s", stdout)
	}

	data, _ := os.ReadFile(testFile)
	if string(data) != "v1 content" {
		t.Errorf("expected 'v1 content', got %q", string(data))
	}
}

func TestCLI_ProcessSubcommand(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-cli-proc-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stdout, _ := captureOutput(func() {
		runProcess([]string{"list", "--workspace", tempDir})
	})

	if !strings.Contains(stdout, "Background Processes") {
		t.Errorf("expected process list header, got: %s", stdout)
	}
}

func TestCLI_ServiceSubcommand(t *testing.T) {
	stdout, _ := captureOutput(func() {
		runService([]string{"list"})
	})

	if !strings.Contains(stdout, "Active Sidecar Services") {
		t.Errorf("expected service list header, got: %s", stdout)
	}
}

func TestCLI_WorktreeSubcommand(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-cli-worktree-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Status on non-git dir
	stdout, _ := captureOutput(func() {
		runWorktree([]string{"status", "--workspace", tempDir})
	})
	if !strings.Contains(stdout, "Git Worktree Status") {
		t.Errorf("expected worktree status header, got: %s", stdout)
	}
	if !strings.Contains(stdout, "not inside a Git repository") {
		t.Errorf("expected inactive notice, got: %s", stdout)
	}

	// 2. Status with default arg
	stdout, _ = captureOutput(func() {
		runWorktree([]string{"--workspace", tempDir})
	})
	if !strings.Contains(stdout, "Git Worktree Status") {
		t.Errorf("expected worktree status header with default arg, got: %s", stdout)
	}

	// 3. Diff on non-git dir
	stdout, _ = captureOutput(func() {
		runWorktree([]string{"diff", "--workspace", tempDir})
	})
	if !strings.Contains(stdout, "not inside a Git repository") {
		t.Errorf("expected not inside git repo message, got: %s", stdout)
	}
}
