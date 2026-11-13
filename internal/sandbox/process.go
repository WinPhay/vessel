package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

var (
	ErrProcessNotFound = errors.New("process not found")
	ErrProcessExited   = errors.New("process has already exited")
)

// DefaultRingBufferCapacity is the maximum unread output kept in memory per process (256 KB).
const DefaultRingBufferCapacity = 256 * 1024

// ProcessInfo holds status and metadata for a managed process.
type ProcessInfo struct {
	ID        string    `json:"process_id"`
	Command   string    `json:"command"`
	Running   bool      `json:"running"`
	ExitCode  *int      `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
	IsPTY     bool      `json:"is_pty"`
}

// RingBuffer provides a thread-safe, fixed-capacity circular buffer.
type RingBuffer struct {
	mu       sync.Mutex
	buf      []byte
	capacity int
}

// NewRingBuffer creates a ring buffer with the specified capacity in bytes.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = DefaultRingBufferCapacity
	}
	return &RingBuffer{
		buf:      make([]byte, 0, 4096),
		capacity: capacity,
	}
}

// Write appends data to the ring buffer, dropping oldest bytes if capacity is exceeded.
func (r *RingBuffer) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf = append(r.buf, p...)
	if len(r.buf) > r.capacity {
		overflow := len(r.buf) - r.capacity
		r.buf = r.buf[overflow:]
	}
	return len(p), nil
}

// ReadUnread drains up to maxBytes from the buffer. If maxBytes <= 0, drains all unread bytes.
func (r *RingBuffer) ReadUnread(maxBytes int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.buf) == 0 {
		return nil
	}

	if maxBytes <= 0 || maxBytes > len(r.buf) {
		maxBytes = len(r.buf)
	}

	out := make([]byte, maxBytes)
	copy(out, r.buf[:maxBytes])
	r.buf = r.buf[maxBytes:]
	return out
}

// ManagedProcess tracks the state and I/O streams of a process inside the sandbox.
type ManagedProcess struct {
	ID        string
	Command   string
	IsPTY     bool
	StartedAt time.Time
	Running   bool
	ExitCode  *int

	mu     sync.Mutex
	buf    *RingBuffer
	stdin  io.WriteCloser
	cancel context.CancelFunc
	onKill func() error
}

// ProcessManager manages the lifecycle of background and interactive processes.
type ProcessManager struct {
	mu        sync.RWMutex
	processes map[string]*ManagedProcess
	closed    bool
}

// NewProcessManager creates an empty process manager.
func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		processes: make(map[string]*ManagedProcess),
	}
}

// RegisterProcess creates and stores a new ManagedProcess entry.
func (pm *ProcessManager) RegisterProcess(cmd string, isPTY bool, stdin io.WriteCloser, cancel context.CancelFunc, onKill func() error) *ManagedProcess {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	id := fmt.Sprintf("proc-%d", time.Now().UnixNano())
	proc := &ManagedProcess{
		ID:        id,
		Command:   cmd,
		IsPTY:     isPTY,
		StartedAt: time.Now(),
		Running:   true,
		ExitCode:  nil,
		buf:       NewRingBuffer(DefaultRingBufferCapacity),
		stdin:     stdin,
		cancel:    cancel,
		onKill:    onKill,
	}

	pm.processes[id] = proc
	return proc
}

// GetProcess retrieves a process by ID.
func (pm *ProcessManager) GetProcess(id string) (*ManagedProcess, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	proc, ok := pm.processes[id]
	if !ok {
		return nil, ErrProcessNotFound
	}
	return proc, nil
}

// ReadOutput drains unread terminal output from a process.
func (pm *ProcessManager) ReadOutput(id string, maxBytes int) (string, *int, bool, error) {
	proc, err := pm.GetProcess(id)
	if err != nil {
		return "", nil, false, err
	}

	proc.mu.Lock()
	rawBytes := proc.buf.ReadUnread(maxBytes)
	exitCode := proc.ExitCode
	running := proc.Running
	proc.mu.Unlock()

	cleaned := tokenfilter.CleanOutput(string(rawBytes))
	return cleaned, exitCode, running, nil
}

// WriteInput delivers text to the process's standard input.
func (pm *ProcessManager) WriteInput(id string, input string) (int, error) {
	proc, err := pm.GetProcess(id)
	if err != nil {
		return 0, err
	}

	proc.mu.Lock()
	defer proc.mu.Unlock()

	if !proc.Running {
		return 0, ErrProcessExited
	}
	if proc.stdin == nil {
		return 0, errors.New("stdin is not attached or already closed")
	}

	return proc.stdin.Write([]byte(input))
}

// Kill terminates a managed process.
func (pm *ProcessManager) Kill(id string, signal string) error {
	proc, err := pm.GetProcess(id)
	if err != nil {
		return err
	}

	proc.mu.Lock()
	defer proc.mu.Unlock()

	if !proc.Running {
		return nil
	}

	proc.Running = false
	defaultExit := 137 // SIGKILL
	if signal == "SIGTERM" || signal == "" {
		defaultExit = 143 // SIGTERM
	}
	if proc.ExitCode == nil {
		proc.ExitCode = &defaultExit
	}

	if proc.cancel != nil {
		proc.cancel()
	}
	if proc.stdin != nil {
		_ = proc.stdin.Close()
	}
	if proc.onKill != nil {
		_ = proc.onKill()
	}

	return nil
}

// List returns a snapshot of all tracked processes.
func (pm *ProcessManager) List() []*ProcessInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	out := make([]*ProcessInfo, 0, len(pm.processes))
	for _, p := range pm.processes {
		p.mu.Lock()
		var exitCopy *int
		if p.ExitCode != nil {
			v := *p.ExitCode
			exitCopy = &v
		}
		info := &ProcessInfo{
			ID:        p.ID,
			Command:   p.Command,
			Running:   p.Running,
			ExitCode:  exitCopy,
			StartedAt: p.StartedAt,
			IsPTY:     p.IsPTY,
		}
		p.mu.Unlock()
		out = append(out, info)
	}
	return out
}

// Close terminates all active processes and releases resources.
func (pm *ProcessManager) Close() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.closed {
		return nil
	}
	pm.closed = true

	for _, p := range pm.processes {
		p.mu.Lock()
		if p.Running {
			p.Running = false
			code := 137
			p.ExitCode = &code
			if p.cancel != nil {
				p.cancel()
			}
			if p.stdin != nil {
				_ = p.stdin.Close()
			}
			if p.onKill != nil {
				_ = p.onKill()
			}
		}
		p.mu.Unlock()
	}
	return nil
}
