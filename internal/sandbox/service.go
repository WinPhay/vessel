package sandbox

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
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
	Ports     []string          `json:"ports,omitempty"`
}

// ServiceManager coordinates the lifecycle of isolated sidecar containers and their shared network.
type ServiceManager struct {
	cli                  client.CommonAPIClient
	workspaceContainerID string
	sessionID            string
	networkID            string
	networkName          string
	services             map[string]*ServiceInfo // name -> ServiceInfo
	persistent           bool
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check if network already exists
	netInspect, err := cli.NetworkInspect(ctx, netName, network.InspectOptions{})
	if err == nil {
		sm.networkID = netInspect.ID
		// Scan for existing service containers on this network with session label
		filterArgs := filters.NewArgs()
		filterArgs.Add("network", netInspect.ID)
		filterArgs.Add("label", fmt.Sprintf("vessel.session_id=%s", sessionID))
		containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
		if err == nil {
			for _, c := range containers {
				svcName := c.Labels["vessel.service_name"]
				if svcName == "" {
					prefix := "vessel-svc-"
					for _, name := range c.Names {
						trimmed := strings.TrimPrefix(name, "/")
						if strings.HasPrefix(trimmed, prefix) {
							middle := strings.TrimPrefix(trimmed, prefix)
							middle = strings.TrimSuffix(middle, "-"+sessionID)
							svcName = middle
							break
						}
					}
				}
				if svcName != "" {
					var ports []string
					for _, p := range c.Ports {
						if p.PublicPort > 0 {
							ip := p.IP
							if ip == "" {
								ip = "127.0.0.1"
							}
							ports = append(ports, fmt.Sprintf("%s:%d->%d/%s", ip, p.PublicPort, p.PrivatePort, p.Type))
						}
					}
					sm.services[svcName] = &ServiceInfo{
						ID:        c.ID,
						Name:      svcName,
						Image:     c.Image,
						Status:    c.State,
						CreatedAt: time.Unix(c.Created, 0),
						Hostname:  svcName,
						Ports:     ports,
					}
				}
			}
		}
	} else {
		// Create isolated bridge network
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
	}

	// Attach workspace container to the network so it can resolve services by hostname
	if workspaceContainerID != "" {
		alreadyConnected := false
		if netInspect, err := cli.NetworkInspect(ctx, sm.networkID, network.InspectOptions{}); err == nil {
			if _, ok := netInspect.Containers[workspaceContainerID]; ok {
				alreadyConnected = true
			}
		}
		if !alreadyConnected {
			err = cli.NetworkConnect(ctx, sm.networkID, workspaceContainerID, &network.EndpointSettings{
				Aliases: []string{"workspace"},
			})
			if err != nil {
				if !strings.Contains(strings.ToLower(err.Error()), "already exists") &&
					!strings.Contains(strings.ToLower(err.Error()), "already connected") {
					_ = cli.NetworkRemove(context.Background(), sm.networkID)
					return nil, fmt.Errorf("failed to attach workspace container to session network: %w", err)
				}
			}
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

// SetPersistent marks whether the service mesh should survive Close().
func (sm *ServiceManager) SetPersistent(p bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistent = p
}

// IsPersistent returns whether persistence is active for the service mesh.
func (sm *ServiceManager) IsPersistent() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistent
}

// ParsePortSpecs parses and validates port mapping specifications.
// Enforces security invariant: host IP must be strictly 127.0.0.1 (localhost only).
// Supported formats:
//   - "5432:5432"          -> 127.0.0.1:5432 -> 5432/tcp
//   - "5432:5432/udp"      -> 127.0.0.1:5432 -> 5432/udp
//   - "127.0.0.1:5432:5432"-> 127.0.0.1:5432 -> 5432/tcp
//   - "5432"               -> 127.0.0.1 (auto) -> 5432/tcp
func ParsePortSpecs(specs []string) (nat.PortSet, nat.PortMap, []string, error) {
	portSet := make(nat.PortSet)
	portMap := make(nat.PortMap)
	var descriptions []string

	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}

		parts := strings.Split(spec, ":")
		var hostIP, hostPort, containerPortProto string

		switch len(parts) {
		case 1:
			hostIP = "127.0.0.1"
			hostPort = ""
			containerPortProto = parts[0]
		case 2:
			hostIP = "127.0.0.1"
			hostPort = parts[0]
			containerPortProto = parts[1]
		case 3:
			hostIP = parts[0]
			hostPort = parts[1]
			containerPortProto = parts[2]
		default:
			return nil, nil, nil, fmt.Errorf("invalid port specification %q: expected [host_ip:][host_port:]container_port", spec)
		}

		// Security Invariant: Host IP must be strictly loopback (127.0.0.1 or localhost)
		if hostIP != "127.0.0.1" && hostIP != "localhost" {
			return nil, nil, nil, fmt.Errorf("insecure port binding in %q: host IP must be 127.0.0.1 (public host exposure forbidden)", spec)
		}
		hostIP = "127.0.0.1"

		proto := "tcp"
		cPortStr := containerPortProto
		if slashIdx := strings.Index(containerPortProto, "/"); slashIdx != -1 {
			cPortStr = containerPortProto[:slashIdx]
			proto = strings.ToLower(containerPortProto[slashIdx+1:])
			if proto != "tcp" && proto != "udp" {
				return nil, nil, nil, fmt.Errorf("unsupported protocol %q in %q (only tcp and udp supported)", proto, spec)
			}
		}

		cPortNum, err := strconv.Atoi(cPortStr)
		if err != nil || cPortNum < 1 || cPortNum > 65535 {
			return nil, nil, nil, fmt.Errorf("invalid container port %q in %q (must be 1-65535)", cPortStr, spec)
		}

		if hostPort != "" {
			hPortNum, err := strconv.Atoi(hostPort)
			if err != nil || hPortNum < 1 || hPortNum > 65535 {
				return nil, nil, nil, fmt.Errorf("invalid host port %q in %q (must be 1-65535)", hostPort, spec)
			}
		}

		natPort := nat.Port(fmt.Sprintf("%d/%s", cPortNum, proto))
		portSet[natPort] = struct{}{}

		binding := nat.PortBinding{
			HostIP:   hostIP,
			HostPort: hostPort,
		}
		portMap[natPort] = append(portMap[natPort], binding)

		desc := fmt.Sprintf("%s:%s->%s", hostIP, hostPort, natPort)
		if hostPort == "" {
			desc = fmt.Sprintf("%s(auto)->%s", hostIP, natPort)
		}
		descriptions = append(descriptions, desc)
	}

	return portSet, portMap, descriptions, nil
}

// SpawnService launches an isolated sidecar container on the session network with optional localhost port mappings.
func (sm *ServiceManager) SpawnService(ctx context.Context, name, imageName string, env map[string]string, cmd string, ports ...string) (*ServiceInfo, error) {
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

	exposedPorts, portBindings, portDescriptions, err := ParsePortSpecs(ports)
	if err != nil {
		return nil, fmt.Errorf("invalid port specifications: %w", err)
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
			Ports:     portDescriptions,
		}
		sm.services[name] = info
		return info, nil
	}

	// Ensure image is pulled
	_, _, err = sm.cli.ImageInspectWithRaw(ctx, imageName)
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
		Image:        imageName,
		Env:          envList,
		Tty:          false,
		ExposedPorts: exposedPorts,
		Labels: map[string]string{
			"vessel.managed":      "true",
			"vessel.service":      "true",
			"vessel.service_name": name,
			"vessel.session_id":   sm.sessionID,
		},
	}
	if cmd != "" {
		containerCfg.Cmd = []string{"sh", "-c", cmd}
	}

	// Security Invariant: Port bindings are strictly bound to localhost (127.0.0.1)
	hostCfg := &container.HostConfig{
		NetworkMode:  container.NetworkMode(sm.networkName),
		PortBindings: portBindings,
		CapDrop:      []string{"ALL"},
		SecurityOpt:  []string{"no-new-privileges:true"},
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
		Ports:     portDescriptions,
	}

	if len(portBindings) > 0 {
		inspect, err := sm.cli.ContainerInspect(ctx, resp.ID)
		if err == nil && inspect.NetworkSettings != nil && len(inspect.NetworkSettings.Ports) > 0 {
			var actualPorts []string
			for port, bindings := range inspect.NetworkSettings.Ports {
				for _, b := range bindings {
					actualPorts = append(actualPorts, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, port))
				}
			}
			if len(actualPorts) > 0 {
				info.Ports = actualPorts
			}
		}
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

	if sm.persistent {
		return nil
	}

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
