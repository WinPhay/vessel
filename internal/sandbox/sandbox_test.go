package sandbox

import (
	"context"
	"testing"
)

func TestMockSandboxLifecycle(t *testing.T) {
	ctx := context.Background()
	sb := NewMockSandbox()

	res, err := sb.Exec(ctx, "echo hello")
	if err != nil {
		t.Fatalf("unexpected Exec error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}

	err = sb.Checkpoint(ctx, "cp-1")
	if err != nil {
		t.Fatalf("unexpected Checkpoint error: %v", err)
	}

	err = sb.Rollback(ctx, "cp-1")
	if err != nil {
		t.Fatalf("unexpected Rollback error: %v", err)
	}

	err = sb.Rollback(ctx, "nonexistent")
	if err != ErrCheckpointNotFound {
		t.Errorf("expected ErrCheckpointNotFound, got: %v", err)
	}

	if err := sb.Close(); err != nil {
		t.Fatalf("unexpected Close error: %v", err)
	}

	_, err = sb.Exec(ctx, "echo after close")
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
}
