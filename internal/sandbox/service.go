package sandbox

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

var (
	serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

// ServiceInfo holds runtime metadata for a running sidecar container.
type ServiceInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Status    string            `json:"status"` // "running", "exited", etc.
	CreatedAt time.Time         `json:"created_at"`
	Hostname  string            `json:"hostname"`
	Env       map[string]string `json:"env,omitempty"`
}

// ServiceManager coordinates the lifecycle of isolated sidecar containers and their shared network.
type ServiceManager struct {
	cli                  client.CommonAPIClient
	workspaceContainerID string
	sessionID            string
	networkID            string
	networkName          string
	services             map[string]*ServiceInfo // name -> ServiceInfo
	mu                   sync.RWMutex
	closed               bool
}

// NewServiceManager initializes an ephemeral service mesh for the given workspace container.
func NewServiceManager(cli client.CommonAPIClient, workspaceContainerID, sessionID string) (*ServiceManager, error) {
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	netName := fmt.Sprintf("vessel-net-%s", sessionID)

	sm := &ServiceManager{
		cli:                  cli,
		workspaceContainerID: workspaceContainerID,
		sessionID:            sessionID,
		networkName:          netName,
		services:             make(map[string]*ServiceInfo),
	}

	if cli == nil {
		return sm, nil
	}

	// Create isolated bridge network
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	netResp, err := cli.NetworkCreate(ctx, netName, network.CreateOptions{
		Driver:     "bridge",
		Attachable: true,
		Internal:   false, // Allows sidecars to pull/initialize if needed, but no host port exposure
		Labels: map[string]string{
			"vessel.managed":    "true",
			"vessel.session_id": sessionID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session bridge network: %w", err)
	}
	sm.networkID = netResp.ID

	// Attach workspace container to the network so it can resolve services by hostname
	if workspaceContainerID != "" {
		err = cli.NetworkConnect(ctx, sm.networkID, workspaceContainerID, &network.EndpointSettings{
			Aliases: []string{"workspace"},
		})
		if err != nil {
			_ = cli.NetworkRemove(context.Background(), sm.networkID)
			return nil, fmt.Errorf("failed to attach workspace container to session network: %w", err)
		}
	}

	return sm, nil
}

// NetworkName returns the active Docker network name.
func (sm *ServiceManager) NetworkName() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.networkName
}

// SpawnService launches an isolated sidecar container on the session network.
func (sm *ServiceManager) SpawnService(ctx context.Context, name, imageName string, env map[string]string, cmd string) (*ServiceInfo, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.closed {
		return nil, ErrSandboxClosed
	}

	name = strings.TrimSpace(strings.ToLower(name))
	if !serviceNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid service name %q: must be alphanumeric with hyphens (1-63 chars)", name)
	}

	if _, exists := sm.services[name]; exists {
		return nil, fmt.Errorf("service %q is already running", name)
	}

	if imageName == "" {
		return nil, fmt.Errorf("service image cannot be empty")
	}

	// Register sensitive env vars into tokenfilter DLP vault
	var envList []string
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "PASS") || strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") {
			tokenfilter.RegisterSecret(k, v)
		}
	}

	// If running without Docker daemon (mock/tests)
	if sm.cli == nil {
		info := &ServiceInfo{
			ID:        fmt.Sprintf("mock-svc-%s-%d", name, time.Now().UnixNano()),
			Name:      name,
			Image:     imageName,
			Status:    "running",
			CreatedAt: time.Now(),
			Hostname:  name,
			Env:       env,
		}
		sm.services[name] = info
		return info, nil
	}

	// Ensure image is pulled
	_, _, err := sm.cli.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		reader, pullErr := sm.cli.ImagePull(ctx, imageName, image.PullOptions{})
		if pullErr != nil {
			return nil, fmt.Errorf("failed to pull service image %s: %w", imageName, pullErr)
		}
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
	}

	containerName := fmt.Sprintf("vessel-svc-%s-%s", name, sm.sessionID)
	containerCfg := &container.Config{
		Image: imageName,
		Env:   envList,
		Tty:   false,
	}
	if cmd != "" {
		containerCfg.Cmd = []string{"sh", "-c", cmd}
	}

	// Host-Isolation Invariant: Zero Host Port Exposure
	// We explicitly set PortBindings to nil and CapDrop to ALL
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(sm.networkName),
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		Resources: container.Resources{
			Memory: 512 * 1024 * 1024, // 512 MB memory boundary for sidecars
		},
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			sm.networkName: {
				Aliases: []string{name},
			},
		},
	}

	resp, err := sm.cli.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create service container %s: %w", name, err)
	}

	if err := sm.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = sm.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start service container %s: %w", name, err)
	}

	info := &ServiceInfo{
		ID:        resp.ID,
		Name:      name,
		Image:     imageName,
		Status:    "running",
		CreatedAt: time.Now(),
		Hostname:  name,
		Env:       env,
	}
	sm.services[name] = info

	return info, nil
}

// StopService terminates and removes a sidecar container by name.
func (sm *ServiceManager) StopService(ctx context.Context, name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	name = strings.TrimSpace(strings.ToLower(name))
	info, exists := sm.services[name]
	if !exists {
		return fmt.Errorf("service %q not found", name)
	}

	if sm.cli != nil {
		stopTimeout := 5
		_ = sm.cli.ContainerStop(ctx, info.ID, container.StopOptions{Timeout: &stopTimeout})
		_ = sm.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true})
	}

	delete(sm.services, name)
	return nil
}

// ListServices returns metadata for all currently running sidecars.
func (sm *ServiceManager) ListServices(ctx context.Context) ([]*ServiceInfo, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]*ServiceInfo, 0, len(sm.services))
	for _, info := range sm.services {
		res = append(res, info)
	}
	return res, nil
}

// Close gracefully tears down all sidecar containers and removes the session network.
func (sm *ServiceManager) Close(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.closed {
		return nil
	}
	sm.closed = true

	if sm.cli == nil {
		sm.services = make(map[string]*ServiceInfo)
		return nil
	}

	stopTimeout := 2
	for _, info := range sm.services {
		_ = sm.cli.ContainerStop(ctx, info.ID, container.StopOptions{Timeout: &stopTimeout})
		_ = sm.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true})
	}
	sm.services = make(map[string]*ServiceInfo)

	// Disconnect workspace container and remove network
	if sm.networkID != "" {
		if sm.workspaceContainerID != "" {
			_ = sm.cli.NetworkDisconnect(ctx, sm.networkID, sm.workspaceContainerID, true)
		}
		_ = sm.cli.NetworkRemove(ctx, sm.networkID)
	}

	return nil
}
