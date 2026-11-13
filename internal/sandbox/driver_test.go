package sandbox

import (
	"context"
	"testing"
)

func TestNormalizeDriverType(t *testing.T) {
	tests := []struct {
		input    string
		expected DriverType
	}{
		{"", DriverDocker},
		{"docker", DriverDocker},
		{"DOCKER", DriverDocker},
		{"mock", DriverMock},
		{"test", DriverMock},
		{"microvm", DriverMicroVM},
		{"vm", DriverMicroVM},
		{"firecracker", DriverMicroVM},
		{"kvm", DriverMicroVM},
		{"unknown-custom", DriverType("unknown-custom")},
	}

	for _, tc := range tests {
		actual := NormalizeDriverType(tc.input)
		if actual != tc.expected {
			t.Errorf("NormalizeDriverType(%q): expected %s, got %s", tc.input, tc.expected, actual)
		}
	}
}

func TestGetDriverCapabilities(t *testing.T) {
	// Docker
	dockerCaps, err := GetDriverCapabilities(DriverDocker)
	if err != nil {
		t.Fatalf("unexpected error for DriverDocker: %v", err)
	}
	if dockerCaps.Type != DriverDocker {
		t.Errorf("expected Type %s, got %s", DriverDocker, dockerCaps.Type)
	}
	if dockerCaps.SupportsMemoryRollback {
		t.Errorf("Docker should not support memory rollback")
	}
	if !dockerCaps.SupportsFilesystemRollback || !dockerCaps.SupportsAirgapNetwork || !dockerCaps.SupportsResourceQuotas {
		t.Errorf("Docker missing expected capability flags: %+v", dockerCaps)
	}

	// Mock
	mockCaps, err := GetDriverCapabilities(DriverMock)
	if err != nil {
		t.Fatalf("unexpected error for DriverMock: %v", err)
	}
	if !mockCaps.KernelIsolated || !mockCaps.SupportsMemoryRollback {
		t.Errorf("Mock missing expected capability flags: %+v", mockCaps)
	}

	// MicroVM
	vmCaps, err := GetDriverCapabilities(DriverMicroVM)
	if err != nil {
		t.Fatalf("unexpected error for DriverMicroVM: %v", err)
	}
	if !vmCaps.KernelIsolated || !vmCaps.SupportsMemoryRollback {
		t.Errorf("MicroVM missing expected capability flags: %+v", vmCaps)
	}

	// Unsupported
	_, err = GetDriverCapabilities(DriverType("invalid-driver"))
	if err == nil {
		t.Errorf("expected error for invalid driver type, got nil")
	}
}

func TestMicroVMSandboxLifecycle(t *testing.T) {
	ctx := context.Background()

	// Empty workspace validation
	_, err := NewMicroVMSandbox(MicroVMConfig{})
	if err == nil {
		t.Errorf("expected error on empty workspace path, got nil")
	}

	sb, err := NewMicroVMSandbox(MicroVMConfig{
		WorkspacePath: "/tmp",
	})
	if err != nil {
		t.Fatalf("unexpected error creating MicroVMSandbox: %v", err)
	}

	// Exec should return ErrDriverNotReady scaffold error
	_, err = sb.Exec(ctx, "echo test")
	if err == nil {
		t.Errorf("expected error from scaffold Exec, got nil")
	}

	// Checkpoint should return ErrDriverNotReady scaffold error
	err = sb.Checkpoint(ctx, "cp1")
	if err == nil {
		t.Errorf("expected error from scaffold Checkpoint, got nil")
	}

	// Rollback should return ErrDriverNotReady scaffold error
	err = sb.Rollback(ctx, "cp1")
	if err == nil {
		t.Errorf("expected error from scaffold Rollback, got nil")
	}

	// Close
	if err := sb.Close(); err != nil {
		t.Errorf("unexpected error closing MicroVMSandbox: %v", err)
	}

	// After Close, closed guards should return ErrSandboxClosed
	_, err = sb.Exec(ctx, "echo test")
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
	err = sb.Checkpoint(ctx, "cp1")
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
	err = sb.Rollback(ctx, "cp1")
	if err != ErrSandboxClosed {
		t.Errorf("expected ErrSandboxClosed, got: %v", err)
	}
}
