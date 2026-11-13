package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnsupportedDriver = errors.New("unsupported sandbox driver")
	ErrDriverNotReady    = errors.New("sandbox driver not ready or not implemented")
)

// DriverType defines the execution engine backend.
type DriverType string

const (
	DriverDocker  DriverType = "docker"
	DriverMock    DriverType = "mock"
	DriverMicroVM DriverType = "microvm"
)

// DriverCapabilities exposes the feature matrix supported by a driver.
type DriverCapabilities struct {
	Type                       DriverType `json:"type"`
	SupportsMemoryRollback     bool       `json:"supports_memory_rollback"`
	SupportsFilesystemRollback bool       `json:"supports_filesystem_rollback"`
	SupportsAirgapNetwork      bool       `json:"supports_airgap_network"`
	SupportsResourceQuotas     bool       `json:"supports_resource_quotas"`
	KernelIsolated             bool       `json:"kernel_isolated"`
}

// NormalizeDriverType canonicalizes user input driver names.
func NormalizeDriverType(raw string) DriverType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "docker", "":
		return DriverDocker
	case "mock", "test":
		return DriverMock
	case "microvm", "vm", "firecracker", "kvm":
		return DriverMicroVM
	default:
		return DriverType(raw)
	}
}

// GetDriverCapabilities returns the static capability matrix for a given driver type.
func GetDriverCapabilities(d DriverType) (DriverCapabilities, error) {
	norm := NormalizeDriverType(string(d))
	switch norm {
	case DriverDocker:
		return DriverCapabilities{
			Type:                       DriverDocker,
			SupportsMemoryRollback:     false, // Docker container tags snapshot filesystem, not RAM
			SupportsFilesystemRollback: true,
			SupportsAirgapNetwork:      true,  // --network none
			SupportsResourceQuotas:     true,  // cgroups memory & cpu
			KernelIsolated:             false, // Standard runc shares host kernel (runsc enables isolation)
		}, nil
	case DriverMock:
		return DriverCapabilities{
			Type:                       DriverMock,
			SupportsMemoryRollback:     true,
			SupportsFilesystemRollback: true,
			SupportsAirgapNetwork:      true,
			SupportsResourceQuotas:     true,
			KernelIsolated:             true,
		}, nil
	case DriverMicroVM:
		return DriverCapabilities{
			Type:                       DriverMicroVM,
			SupportsMemoryRollback:     true, // MicroVM KVM snapshotting
			SupportsFilesystemRollback: true,
			SupportsAirgapNetwork:      true,
			SupportsResourceQuotas:     true,
			KernelIsolated:             true, // Dedicated guest Linux kernel
		}, nil
	default:
		return DriverCapabilities{}, fmt.Errorf("%w: %s", ErrUnsupportedDriver, d)
	}
}

// MicroVMConfig defines the initialization parameters for Phase 2 MicroVM backends.
type MicroVMConfig struct {
	WorkspacePath string
	KernelPath    string
	RootFSPath    string
	VCPUs         int
	MemoryMB      int64
	NetworkAirgap bool
}

// MicroVMSandbox provides the scaffolding contract for Phase 2 MicroVM drivers.
type MicroVMSandbox struct {
	cfg    MicroVMConfig
	closed bool
}

// NewMicroVMSandbox creates an unstarted MicroVM sandbox scaffold.
func NewMicroVMSandbox(cfg MicroVMConfig) (*MicroVMSandbox, error) {
	if cfg.WorkspacePath == "" {
		return nil, errors.New("workspace path is required")
	}
	return &MicroVMSandbox{
		cfg: cfg,
	}, nil
}

func (m *MicroVMSandbox) Exec(ctx context.Context, cmd string) (*ExecResult, error) {
	if m.closed {
		return nil, ErrSandboxClosed
	}
	return nil, fmt.Errorf("%w: microvm driver execution requires firecracker daemon socket", ErrDriverNotReady)
}

func (m *MicroVMSandbox) Checkpoint(ctx context.Context, tag string) error {
	if m.closed {
		return ErrSandboxClosed
	}
	return fmt.Errorf("%w: microvm memory checkpointing requires active vm instance", ErrDriverNotReady)
}

func (m *MicroVMSandbox) Rollback(ctx context.Context, tag string) error {
	if m.closed {
		return ErrSandboxClosed
	}
	return fmt.Errorf("%w: microvm memory rollback requires active vm instance", ErrDriverNotReady)
}

func (m *MicroVMSandbox) Close() error {
	m.closed = true
	return nil
}
