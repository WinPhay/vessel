package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// Process management methods
	StartProcess(ctx context.Context, cmd string, pty bool, rows, cols int) (*ProcessInfo, error)
	ReadProcessOutput(ctx context.Context, id string, maxBytes int) (string, *int, bool, error)
	WriteProcessInput(ctx context.Context, id string, input string) (int, error)
	KillProcess(ctx context.Context, id string, signal string) error
	ListProcesses(ctx context.Context) ([]*ProcessInfo, error)
}

// MockSandbox provides an in-memory test implementation of the Sandbox interface.
type MockSandbox struct {
	closed      bool
	checkpoints map[string]bool
	mockStdout  string
	mockStderr  string
	mockExit    int
	procMgr     *ProcessManager
}

// NewMockSandbox creates an initialized MockSandbox.
func NewMockSandbox() *MockSandbox {
	return &MockSandbox{
		checkpoints: make(map[string]bool),
		mockStdout:  "mock output",
		mockExit:    0,
		procMgr:     NewProcessManager(),
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
	if err := ValidateCheckpointTag(tag); err != nil {
		return err
	}
	m.checkpoints[tag] = true
	return nil
}

func (m *MockSandbox) Rollback(ctx context.Context, tag string) error {
	if m.closed {
		return ErrSandboxClosed
	}
	if err := ValidateCheckpointTag(tag); err != nil {
		return err
	}
	if !m.checkpoints[tag] {
		return ErrCheckpointNotFound
	}
	return nil
}

func (m *MockSandbox) Close() error {
	m.closed = true
	if m.procMgr != nil {
		_ = m.procMgr.Close()
	}
	return nil
}

func (m *MockSandbox) StartProcess(ctx context.Context, cmd string, pty bool, rows, cols int) (*ProcessInfo, error) {
	if m.closed {
		return nil, ErrSandboxClosed
	}
	r, w := io.Pipe()
	proc := m.procMgr.RegisterProcess(cmd, pty, w, nil, func() error {
		_ = r.Close()
		_ = w.Close()
		return nil
	})
	// Simulate initial echo output into proc.buf
	_, _ = proc.buf.Write([]byte(fmt.Sprintf("[mock-pty:%s]$ %s\n", proc.ID, cmd)))

	// Pipe stdin back to stdout in background for mock interactive testing
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				_, _ = proc.buf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	return &ProcessInfo{
		ID:        proc.ID,
		Command:   proc.Command,
		Running:   proc.Running,
		ExitCode:  proc.ExitCode,
		StartedAt: proc.StartedAt,
		IsPTY:     proc.IsPTY,
	}, nil
}

func (m *MockSandbox) ReadProcessOutput(ctx context.Context, id string, maxBytes int) (string, *int, bool, error) {
	if m.closed {
		return "", nil, false, ErrSandboxClosed
	}
	return m.procMgr.ReadOutput(id, maxBytes)
}

func (m *MockSandbox) WriteProcessInput(ctx context.Context, id string, input string) (int, error) {
	if m.closed {
		return 0, ErrSandboxClosed
	}
	return m.procMgr.WriteInput(id, input)
}

func (m *MockSandbox) KillProcess(ctx context.Context, id string, signal string) error {
	if m.closed {
		return ErrSandboxClosed
	}
	return m.procMgr.Kill(id, signal)
}

func (m *MockSandbox) ListProcesses(ctx context.Context) ([]*ProcessInfo, error) {
	if m.closed {
		return nil, ErrSandboxClosed
	}
	return m.procMgr.List(), nil
}
