package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerSandboxConfig holds initialization parameters for the Docker sandbox.
type DockerSandboxConfig struct {
	ImageName     string
	WorkspacePath string
	ContainerName string
	MaxOutputSize int
	NetworkMode   string
	MemoryBytes   int64   // Memory limit in bytes (0 = unlimited)
	CPUShares     float64 // CPU cores (e.g., 1.5, 2.0; 0 = unlimited)
}

// DockerSandbox manages an isolated execution container via the Docker Engine API.
type DockerSandbox struct {
	cli         client.CommonAPIClient
	containerID string
	cfg         DockerSandboxConfig
	mu          sync.RWMutex
	checkpoints map[string]string // tag -> imageID
	snapshotMgr *SnapshotManager
	closed      bool
}

const (
	// ImageAlpineRaw is the minimal base alpine image.
	ImageAlpineRaw = "alpine:latest"
	// ImageAgentAlpine is the pre-baked lightweight Vessel AgentOS image.
	ImageAgentAlpine = "vessel-agent:alpine"
	// ImageAgentUbuntu is the comprehensive developer Vessel AgentOS image.
	ImageAgentUbuntu = "vessel-agent:ubuntu"
	// ImageAgentBrowser is the headless browser Vessel AgentOS image.
	ImageAgentBrowser = "vessel-agent:browser"
)

// ResolveImageAlias resolves friendly shorthand aliases to full Docker image tags.
func ResolveImageAlias(aliasOrName string) string {
	switch strings.ToLower(strings.TrimSpace(aliasOrName)) {
	case "agent", "vessel-agent", "default":
		return ImageAgentAlpine
	case "ubuntu", "ubuntu-agent":
		return ImageAgentUbuntu
	case "browser", "browser-agent":
		return ImageAgentBrowser
	case "alpine", "alpine-raw":
		return ImageAlpineRaw
	case "":
		return ImageAlpineRaw
	default:
		return aliasOrName
	}
}

// ParseMemoryString converts human-readable memory strings (e.g., "512m", "1g", "2gb", "4096k") to bytes.
// Empty string, "0", or "unlimited" returns 0 (unlimited).
func ParseMemoryString(val string) (int64, error) {
	val = strings.TrimSpace(strings.ToLower(val))
	if val == "" || val == "0" || val == "unlimited" {
		return 0, nil
	}

	val = strings.TrimSuffix(val, "b") // handle "gb", "mb", "kb"
	multiplier := int64(1)
	numStr := val

	if strings.HasSuffix(val, "k") {
		multiplier = 1024
		numStr = strings.TrimSuffix(val, "k")
	} else if strings.HasSuffix(val, "m") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSuffix(val, "m")
	} else if strings.HasSuffix(val, "g") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(val, "g")
	} else if strings.HasSuffix(val, "t") {
		multiplier = 1024 * 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(val, "t")
	}

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory format %q: %w", val, err)
	}
	if num < 0 {
		return 0, fmt.Errorf("memory limit cannot be negative: %s", val)
	}

	return int64(num * float64(multiplier)), nil
}

// DefaultDockerConfig returns sensible defaults for local agent execution.
func DefaultDockerConfig(workspacePath string) DockerSandboxConfig {
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		absPath = workspacePath
	}
	return DockerSandboxConfig{
		ImageName:     ImageAlpineRaw,
		WorkspacePath: absPath,
		ContainerName: fmt.Sprintf("vessel-sandbox-%d", time.Now().UnixNano()),
		MaxOutputSize: 100 * 1024, // 100 KB
		NetworkMode:   "bridge",
		MemoryBytes:   0,
		CPUShares:     0,
	}
}

// NewDockerSandbox creates and starts an isolated container with /workspace mounted.
func NewDockerSandbox(ctx context.Context, cfg DockerSandboxConfig, cli client.CommonAPIClient) (*DockerSandbox, error) {
	cfg.ImageName = ResolveImageAlias(cfg.ImageName)
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = "bridge"
	}

	var err error
	if cli == nil {
		cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Docker client: %w", err)
		}
	}

	if _, err := os.Stat(cfg.WorkspacePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace path does not exist: %s", cfg.WorkspacePath)
	}

	// Ensure base image is available locally or attempt pull
	_, _, err = cli.ImageInspectWithRaw(ctx, cfg.ImageName)
	if err != nil {
		reader, pullErr := cli.ImagePull(ctx, cfg.ImageName, image.PullOptions{})
		if pullErr != nil {
			return nil, fmt.Errorf("failed to pull image %s: %w", cfg.ImageName, pullErr)
		}
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
	}

	containerID, err := createAndStartContainer(ctx, cli, cfg.ImageName, cfg.ContainerName, cfg.WorkspacePath, cfg.NetworkMode, cfg.MemoryBytes, cfg.CPUShares)
	if err != nil {
		return nil, err
	}

	snapMgr, err := NewSnapshotManager(cfg.WorkspacePath)
	if err != nil {
		// Clean up container if snapshot manager initialization fails
		_ = cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to initialize workspace snapshot manager: %w", err)
	}

	return &DockerSandbox{
		cli:         cli,
		containerID: containerID,
		cfg:         cfg,
		checkpoints: make(map[string]string),
		snapshotMgr: snapMgr,
	}, nil
}

func createAndStartContainer(ctx context.Context, cli client.CommonAPIClient, imageName, containerName, workspacePath, networkMode string, memBytes int64, cpus float64) (string, error) {
	if networkMode == "" {
		networkMode = "bridge"
	}

	containerCfg := &container.Config{
		Image:        imageName,
		Cmd:          []string{"sh", "-c", "while true; do sleep 3600; done"},
		WorkingDir:   "/workspace",
		Tty:          false,
		AttachStdout: true,
		AttachStderr: true,
		Env: []string{
			"DEBIAN_FRONTEND=noninteractive",
			"GIT_PAGER=cat",
			"PAGER=cat",
			"TERM=dumb",
		},
	}

	res := container.Resources{}
	if memBytes > 0 {
		res.Memory = memBytes
	}
	if cpus > 0 {
		res.NanoCPUs = int64(cpus * 1e9)
	}

	hostCfg := &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s:/workspace:rw", workspacePath),
		},
		NetworkMode: container.NetworkMode(networkMode),
		Resources:   res,
	}

	resp, err := cli.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create sandbox container: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("failed to start sandbox container: %w", err)
	}

	return resp.ID, nil
}

// Exec runs a command inside the container and returns clean, token-optimized results.
func (d *DockerSandbox) Exec(ctx context.Context, cmd string) (*ExecResult, error) {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, ErrSandboxClosed
	}
	containerID := d.containerID
	d.mu.RUnlock()

	start := time.Now()

	execCfg := container.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   "/workspace",
	}

	execResp, err := d.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec instance: %w", err)
	}

	attachResp, err := d.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach to exec instance: %w", err)
	}
	defer attachResp.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	_, err = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, attachResp.Reader)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read command output: %w", err)
	}

	inspectResp, err := d.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect exec result: %w", err)
	}

	cleanStdout := tokenfilter.CleanOutput(stdoutBuf.String())
	cleanStderr := tokenfilter.CleanOutput(stderrBuf.String())

	truncated := false
	if d.cfg.MaxOutputSize > 0 {
		var wasTruncated bool
		cleanStdout, wasTruncated = tokenfilter.TruncateOutput(cleanStdout, d.cfg.MaxOutputSize)
		if wasTruncated {
			truncated = true
		}
		cleanStderr, wasTruncated = tokenfilter.TruncateOutput(cleanStderr, d.cfg.MaxOutputSize)
		if wasTruncated {
			truncated = true
		}
	}

	totalTokens := tokenfilter.EstimateTokens(cleanStdout) + tokenfilter.EstimateTokens(cleanStderr)

	return &ExecResult{
		ExitCode:      inspectResp.ExitCode,
		Stdout:        cleanStdout,
		Stderr:        cleanStderr,
		Duration:      time.Since(start),
		TokenEstimate: totalTokens,
		Truncated:     truncated,
	}, nil
}

// Checkpoint captures the current container state as a tagged image and snapshots the workspace directory.
func (d *DockerSandbox) Checkpoint(ctx context.Context, tag string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrSandboxClosed
	}

	// 1. Snapshot the host workspace filesystem state
	if d.snapshotMgr != nil {
		if err := d.snapshotMgr.CreateSnapshot(tag); err != nil {
			return fmt.Errorf("failed to create workspace snapshot %s: %w", tag, err)
		}
	}

	// 2. Commit the container rootfs state
	commitOpts := container.CommitOptions{
		Reference: fmt.Sprintf("vessel-checkpoint:%s", tag),
		Comment:   fmt.Sprintf("Vessel snapshot for tag %s", tag),
	}

	resp, err := d.cli.ContainerCommit(ctx, d.containerID, commitOpts)
	if err != nil {
		return fmt.Errorf("failed to commit container checkpoint %s: %w", tag, err)
	}

	d.checkpoints[tag] = resp.ID
	return nil
}

// Rollback restores the container to the snapshot associated with the tag and restores the workspace directory.
func (d *DockerSandbox) Rollback(ctx context.Context, tag string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrSandboxClosed
	}

	imageID, exists := d.checkpoints[tag]
	if !exists {
		return ErrCheckpointNotFound
	}

	// 1. Restore the host workspace directory state
	if d.snapshotMgr != nil {
		if err := d.snapshotMgr.RestoreSnapshot(tag); err != nil {
			return fmt.Errorf("failed to restore workspace snapshot %s: %w", tag, err)
		}
	}

	// 2. Stop and remove current corrupted container
	stopTimeout := 5
	_ = d.cli.ContainerStop(ctx, d.containerID, container.StopOptions{Timeout: &stopTimeout})
	_ = d.cli.ContainerRemove(ctx, d.containerID, container.RemoveOptions{Force: true})

	// 3. Spin up a fresh container using the checkpoint image with same workspace volume
	newContainerName := fmt.Sprintf("%s-rollback-%d", d.cfg.ContainerName, time.Now().UnixNano())
	newID, err := createAndStartContainer(ctx, d.cli, imageID, newContainerName, d.cfg.WorkspacePath, d.cfg.NetworkMode, d.cfg.MemoryBytes, d.cfg.CPUShares)
	if err != nil {
		return fmt.Errorf("failed to restore container from checkpoint %s: %w", tag, err)
	}

	d.containerID = newID
	return nil
}

// Close gracefully stops and removes the sandbox container.
func (d *DockerSandbox) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}
	d.closed = true

	stopTimeout := 2
	_ = d.cli.ContainerStop(context.Background(), d.containerID, container.StopOptions{Timeout: &stopTimeout})
	err := d.cli.ContainerRemove(context.Background(), d.containerID, container.RemoveOptions{Force: true})
	return err
}
