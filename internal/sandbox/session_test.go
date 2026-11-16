package sandbox

import (
	"context"
	"testing"
)

func TestSession_NilClientGuards(t *testing.T) {
	ctx := context.Background()

	// 1. ListSessions with nil client
	sessions, err := ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("expected nil error with nil client, got: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions with nil client, got: %d", len(sessions))
	}

	// 2. InspectSession with nil client or empty ID
	_, err = InspectSession(ctx, nil, "")
	if err == nil {
		t.Errorf("expected error for empty session ID and nil client")
	}

	_, err = InspectSession(ctx, nil, "valid-id")
	if err == nil {
		t.Errorf("expected error for nil client in InspectSession")
	}

	// 3. StopSession with nil client or empty ID
	err = StopSession(ctx, nil, "")
	if err == nil {
		t.Errorf("expected error for empty session ID in StopSession")
	}

	err = StopSession(ctx, nil, "valid-id")
	if err == nil {
		t.Errorf("expected error for nil client in StopSession")
	}

	// 4. PruneSessions with nil client
	pruneRes, err := PruneSessions(ctx, nil, false, "")
	if err != nil {
		t.Fatalf("expected nil error for nil client in PruneSessions, got: %v", err)
	}
	if len(pruneRes.ContainersDeleted) != 0 || len(pruneRes.NetworksDeleted) != 0 || len(pruneRes.WorktreesPruned) != 0 {
		t.Errorf("expected empty prune result with nil client, got: %+v", pruneRes)
	}
}
