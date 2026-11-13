package sandbox

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRingBuffer_Basic(t *testing.T) {
	rb := NewRingBuffer(1024)

	n, err := rb.Write([]byte("hello "))
	if err != nil || n != 6 {
		t.Fatalf("Write failed: n=%d, err=%v", n, err)
	}

	_, _ = rb.Write([]byte("world\n"))

	// Read first 5 bytes
	part1 := rb.ReadUnread(5)
	if string(part1) != "hello" {
		t.Errorf("expected 'hello', got %q", string(part1))
	}

	// Read remaining
	part2 := rb.ReadUnread(0)
	if string(part2) != " world\n" {
		t.Errorf("expected ' world\\n', got %q", string(part2))
	}

	// Buffer should now be empty
	part3 := rb.ReadUnread(10)
	if part3 != nil {
		t.Errorf("expected nil for empty buffer, got %q", string(part3))
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	capacity := 10
	rb := NewRingBuffer(capacity)

	// Write 15 bytes into a 10-byte buffer
	_, _ = rb.Write([]byte("1234567890abcde"))

	data := rb.ReadUnread(0)
	if len(data) != 10 {
		t.Fatalf("expected 10 bytes, got %d", len(data))
	}
	// Oldest bytes (12345) should have been dropped; only "67890abcde" remains
	if string(data) != "67890abcde" {
		t.Errorf("expected '67890abcde', got %q", string(data))
	}
}

func TestProcessManager_Lifecycle(t *testing.T) {
	pm := NewProcessManager()
	defer pm.Close()

	r, w := io.Pipe()
	killed := false
	onKill := func() error {
		killed = true
		_ = r.Close()
		_ = w.Close()
		return nil
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	proc := pm.RegisterProcess("python3 -i", true, w, cancel, onKill)
	if proc.ID == "" || !proc.Running || !proc.IsPTY {
		t.Fatalf("invalid process registration: %+v", proc)
	}

	// Simulate output from process
	_, _ = proc.buf.Write([]byte(">>> \x1b[32mprint('hi')\x1b[0m\nhi\n>>> "))

	// 1. Read unread output
	out, exitCode, running, err := pm.ReadOutput(proc.ID, 0)
	if err != nil {
		t.Fatalf("ReadOutput error: %v", err)
	}
	if !running || exitCode != nil {
		t.Errorf("expected running=true, exitCode=nil, got running=%v, exitCode=%v", running, exitCode)
	}
	if !strings.Contains(out, "print('hi')") || strings.Contains(out, "\x1b[32m") {
		t.Errorf("output not properly cleaned of ANSI escapes: %q", out)
	}

	// 2. Write input into stdin
	go func() {
		buf := make([]byte, 100)
		_, _ = r.Read(buf)
	}()

	n, err := pm.WriteInput(proc.ID, "2 + 2\n")
	if err != nil || n != 6 {
		t.Fatalf("WriteInput error: n=%d, err=%v", n, err)
	}

	// 3. List processes
	list := pm.List()
	if len(list) != 1 || list[0].ID != proc.ID {
		t.Fatalf("unexpected list: %+v", list)
	}

	// 4. Kill process
	err = pm.Kill(proc.ID, "SIGTERM")
	if err != nil {
		t.Fatalf("Kill error: %v", err)
	}
	if !killed {
		t.Errorf("expected onKill to be invoked")
	}

	// After kill, running should be false and exit code populated
	_, exitCode, running, _ = pm.ReadOutput(proc.ID, 0)
	if running {
		t.Errorf("expected running=false after kill")
	}
	if exitCode == nil || *exitCode != 143 {
		t.Errorf("expected SIGTERM exit code 143, got %v", exitCode)
	}

	// Writing to exited process should fail with ErrProcessExited
	_, err = pm.WriteInput(proc.ID, "fail")
	if err != ErrProcessExited {
		t.Errorf("expected ErrProcessExited, got: %v", err)
	}

	// Unknown process operations should fail with ErrProcessNotFound
	_, _, _, err = pm.ReadOutput("non-existent", 0)
	if err != ErrProcessNotFound {
		t.Errorf("expected ErrProcessNotFound, got: %v", err)
	}
}

func TestMockSandbox_ProcessManagement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sb := NewMockSandbox()
	defer sb.Close()

	// 1. Start process
	info, err := sb.StartProcess(ctx, "top", true, 24, 80)
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}
	if info.ID == "" || !info.Running {
		t.Fatalf("unexpected process info: %+v", info)
	}

	// 2. Read initial simulated output
	time.Sleep(10 * time.Millisecond)
	out, exitCode, running, err := sb.ReadProcessOutput(ctx, info.ID, 0)
	if err != nil {
		t.Fatalf("ReadProcessOutput failed: %v", err)
	}
	if !running || exitCode != nil {
		t.Errorf("expected running process, got running=%v, exitCode=%v", running, exitCode)
	}
	if !strings.Contains(out, "top") {
		t.Errorf("expected output to contain 'top', got: %q", out)
	}

	// 3. Write input
	written, err := sb.WriteProcessInput(ctx, info.ID, "q")
	if err != nil || written != 1 {
		t.Fatalf("WriteProcessInput failed: written=%d, err=%v", written, err)
	}

	// 4. List processes
	list, err := sb.ListProcesses(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProcesses failed: list=%+v, err=%v", list, err)
	}

	// 5. Kill process
	if err := sb.KillProcess(ctx, info.ID, "SIGTERM"); err != nil {
		t.Fatalf("KillProcess failed: %v", err)
	}

	// 6. Close sandbox and check guards
	if err := sb.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = sb.StartProcess(ctx, "echo 1", true, 24, 80)
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
	_, _, _, err = sb.ReadProcessOutput(ctx, info.ID, 0)
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
	_, err = sb.WriteProcessInput(ctx, info.ID, "test")
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
	err = sb.KillProcess(ctx, info.ID, "SIGTERM")
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
	_, err = sb.ListProcesses(ctx)
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
}
