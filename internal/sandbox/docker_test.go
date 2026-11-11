package sandbox

import (
	"context"
	"os"
	"testing"
)

func TestDefaultDockerConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vessel-test-workspace-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := DefaultDockerConfig(tmpDir)

	if cfg.ImageName != "alpine:latest" {
		t.Errorf("expected default image alpine:latest, got: %s", cfg.ImageName)
	}
	if cfg.WorkspacePath != tmpDir {
		t.Errorf("expected workspace path %s, got: %s", tmpDir, cfg.WorkspacePath)
	}
	if cfg.MaxOutputSize <= 0 {
		t.Errorf("expected MaxOutputSize > 0, got: %d", cfg.MaxOutputSize)
	}
}

func TestDockerSandbox_NonexistentWorkspace(t *testing.T) {
	ctx := context.Background()
	cfg := DockerSandboxConfig{
		ImageName:     "alpine:latest",
		WorkspacePath: "/path/that/does/not/exist/vessel-test-12345",
		ContainerName: "test-container",
	}

	_, err := NewDockerSandbox(ctx, cfg, nil)
	if err == nil {
		t.Fatalf("expected error for nonexistent workspace, got nil")
	}
}

func TestDockerSandbox_ClosedGuards(t *testing.T) {
	ctx := context.Background()
	sb := &DockerSandbox{
		closed:      true,
		checkpoints: make(map[string]string),
	}

	_, err := sb.Exec(ctx, "echo test")
	if err != ErrSandboxClosed {
		t.Errorf("Exec on closed sandbox: expected ErrSandboxClosed, got: %v", err)
	}

	err = sb.Checkpoint(ctx, "cp1")
	if err != ErrSandboxClosed {
		t.Errorf("Checkpoint on closed sandbox: expected ErrSandboxClosed, got: %v", err)
	}

	err = sb.Rollback(ctx, "cp1")
	if err != ErrSandboxClosed {
		t.Errorf("Rollback on closed sandbox: expected ErrSandboxClosed, got: %v", err)
	}
}
