package sandbox

import (
	"context"
	"errors"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

var (
	ErrSandboxClosed      = errors.New("sandbox is closed")
	ErrCheckpointNotFound = errors.New("checkpoint not found")
	ErrExecutionTimeout   = errors.New("command execution timed out")
)

// ExecResult contains the structured response of a command executed inside the sandbox.
type ExecResult struct {
	ExitCode      int           `json:"exit_code"`
	Stdout        string        `json:"stdout"`
	Stderr        string        `json:"stderr"`
	Duration      time.Duration `json:"duration_ms"`
	TokenEstimate int           `json:"token_estimate"`
	Truncated     bool          `json:"truncated"`
}

// Sandbox defines the contract for an isolated execution environment.
type Sandbox interface {
	// Exec runs a shell command inside the sandbox container.
	Exec(ctx context.Context, cmd string) (*ExecResult, error)

	// Checkpoint snapshots the filesystem state under the given tag.
	Checkpoint(ctx context.Context, tag string) error

	// Rollback restores the filesystem state to the specified tag.
	Rollback(ctx context.Context, tag string) error

	// Close terminates and cleans up the sandbox container.
	Close() error
}

// MockSandbox provides an in-memory test implementation of the Sandbox interface.
type MockSandbox struct {
	closed      bool
	checkpoints map[string]bool
	mockStdout  string
	mockStderr  string
	mockExit    int
}

// NewMockSandbox creates an initialized MockSandbox.
func NewMockSandbox() *MockSandbox {
	return &MockSandbox{
		checkpoints: make(map[string]bool),
		mockStdout:  "mock output",
		mockExit:    0,
	}
}

func (m *MockSandbox) Exec(ctx context.Context, cmd string) (*ExecResult, error) {
	if m.closed {
		return nil, ErrSandboxClosed
	}

	cleanStdout := tokenfilter.CleanOutput(m.mockStdout)
	cleanStderr := tokenfilter.CleanOutput(m.mockStderr)

	return &ExecResult{
		ExitCode:      m.mockExit,
		Stdout:        cleanStdout,
		Stderr:        cleanStderr,
		Duration:      10 * time.Millisecond,
		TokenEstimate: tokenfilter.EstimateTokens(cleanStdout + cleanStderr),
		Truncated:     false,
	}, nil
}

func (m *MockSandbox) Checkpoint(ctx context.Context, tag string) error {
	if m.closed {
		return ErrSandboxClosed
	}
	m.checkpoints[tag] = true
	return nil
}

func (m *MockSandbox) Rollback(ctx context.Context, tag string) error {
	if m.closed {
		return ErrSandboxClosed
	}
	if !m.checkpoints[tag] {
		return ErrCheckpointNotFound
	}
	return nil
}

func (m *MockSandbox) Close() error {
	m.closed = true
	return nil
}
