package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// SessionSummary represents a high-level overview of an execution session.
type SessionSummary struct {
	SessionID   string    `json:"session_id"`
	ContainerID string    `json:"container_id"`
	Status      string    `json:"status"` // "running", "exited", "created"
	Image       string    `json:"image"`
	Workspace   string    `json:"workspace"`
	Persistent  bool      `json:"persistent"`
	CreatedAt   time.Time `json:"created_at"`
	Sidecars    []string  `json:"sidecars,omitempty"`
	Worktree    string    `json:"worktree,omitempty"`
}

// SessionDetail provides deep inspection information for an execution session.
type SessionDetail struct {
	SessionSummary
	IPAddress   string         `json:"ip_address,omitempty"`
	NetworkName string         `json:"network_name,omitempty"`
	SidecarInfo []*ServiceInfo `json:"sidecar_info,omitempty"`
	Environment []string       `json:"environment,omitempty"`
}

// PruneResult records resources removed during a session prune operation.
type PruneResult struct {
	ContainersDeleted []string `json:"containers_deleted"`
	NetworksDeleted   []string `json:"networks_deleted"`
	WorktreesPruned   []string `json:"worktrees_pruned"`
}

// ListSessions discovers all Vessel-managed sessions across the Docker daemon.
func ListSessions(ctx context.Context, cli client.CommonAPIClient) ([]*SessionSummary, error) {
	if cli == nil {
		return []*SessionSummary{}, nil
	}

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "vessel.managed=true")

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
	if err != nil {
		return nil, fmt.Errorf("failed to list vessel containers: %w", err)
	}

	// Separate workspace containers from sidecars
	sessionMap := make(map[string]*SessionSummary)
	sidecarsMap := make(map[string][]string)

	for _, c := range containers {
		sid := c.Labels["vessel.session_id"]
		if sid == "" {
			continue
		}

		if c.Labels["vessel.service"] == "true" {
			svcName := c.Labels["vessel.service_name"]
			if svcName == "" && len(c.Names) > 0 {
				svcName = strings.TrimPrefix(c.Names[0], "/")
			}
			sidecarsMap[sid] = append(sidecarsMap[sid], svcName)
			continue
		}

		// Primary workspace sandbox container
		shortID := c.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}

		wsPath := c.Labels["vessel.workspace"]
		persistent := c.Labels["vessel.persistent"] == "true"

		summary := &SessionSummary{
			SessionID:   sid,
			ContainerID: shortID,
			Status:      c.State,
			Image:       c.Image,
			Workspace:   wsPath,
			Persistent:  persistent,
			CreatedAt:   time.Unix(c.Created, 0),
		}

		// Check if worktree directory exists on host for this session
		if wsPath != "" {
			wtDir := filepath.Join(wsPath, ".vessel", "worktrees", fmt.Sprintf("vessel-%s", sid))
			if fi, err := os.Stat(wtDir); err == nil && fi.IsDir() {
				summary.Worktree = wtDir
			}
		}

		sessionMap[sid] = summary
	}

	// Attach sidecars to sessions
	for sid, sidecars := range sidecarsMap {
		if s, ok := sessionMap[sid]; ok {
			s.Sidecars = sidecars
		}
	}

	res := make([]*SessionSummary, 0, len(sessionMap))
	for _, s := range sessionMap {
		res = append(res, s)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].CreatedAt.After(res[j].CreatedAt)
	})

	return res, nil
}

// InspectSession returns deep configuration and topology details for a session.
func InspectSession(ctx context.Context, cli client.CommonAPIClient, sessionID string) (*SessionDetail, error) {
	if cli == nil {
		return nil, fmt.Errorf("docker client is required for inspect")
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("vessel.session_id=%s", sessionID))

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
	if err != nil {
		return nil, fmt.Errorf("failed to search for session containers: %w", err)
	}

	var primaryID string
	var sidecars []*ServiceInfo

	for _, c := range containers {
		if c.Labels["vessel.service"] == "true" {
			svcName := c.Labels["vessel.service_name"]
			if svcName == "" && len(c.Names) > 0 {
				svcName = strings.TrimPrefix(c.Names[0], "/")
			}
			sidecars = append(sidecars, &ServiceInfo{
				ID:        c.ID,
				Name:      svcName,
				Image:     c.Image,
				Status:    c.State,
				CreatedAt: time.Unix(c.Created, 0),
				Hostname:  svcName,
			})
		} else {
			primaryID = c.ID
		}
	}

	if primaryID == "" {
		// Fallback lookup by container name
		containerName := fmt.Sprintf("vessel-sandbox-%s", sessionID)
		inspect, err := cli.ContainerInspect(ctx, containerName)
		if err == nil {
			primaryID = inspect.ID
		} else {
			return nil, fmt.Errorf("no active or persistent session found for ID %q", sessionID)
		}
	}

	inspect, err := cli.ContainerInspect(ctx, primaryID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	wsPath := inspect.Config.Labels["vessel.workspace"]
	persistent := inspect.Config.Labels["vessel.persistent"] == "true"

	shortID := primaryID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}

	detail := &SessionDetail{
		SessionSummary: SessionSummary{
			SessionID:   sessionID,
			ContainerID: shortID,
			Status:      inspect.State.Status,
			Image:       inspect.Config.Image,
			Workspace:   wsPath,
			Persistent:  persistent,
			Sidecars:    make([]string, 0, len(sidecars)),
		},
		SidecarInfo: sidecars,
	}

	for _, s := range sidecars {
		detail.Sidecars = append(detail.Sidecars, s.Name)
	}

	if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
		detail.CreatedAt = t
	}

	if inspect.NetworkSettings != nil {
		detail.IPAddress = inspect.NetworkSettings.IPAddress
		for netName := range inspect.NetworkSettings.Networks {
			if strings.HasPrefix(netName, "vessel-net-") {
				detail.NetworkName = netName
				break
			}
		}
	}

	if wsPath != "" {
		wtDir := filepath.Join(wsPath, ".vessel", "worktrees", fmt.Sprintf("vessel-%s", sessionID))
		if fi, err := os.Stat(wtDir); err == nil && fi.IsDir() {
			detail.Worktree = wtDir
		}
	}

	return detail, nil
}

// StopSession gracefully sends a stop request to the primary container of the session.
func StopSession(ctx context.Context, cli client.CommonAPIClient, sessionID string) error {
	if cli == nil {
		return fmt.Errorf("docker client is required")
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("vessel.session_id=%s", sessionID))

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
	if err != nil {
		return fmt.Errorf("failed to search session containers: %w", err)
	}

	stopped := 0
	timeout := 3
	for _, c := range containers {
		if c.State == "running" {
			_ = cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout})
			stopped++
		}
	}

	if stopped == 0 {
		containerName := fmt.Sprintf("vessel-sandbox-%s", sessionID)
		_ = cli.ContainerStop(ctx, containerName, container.StopOptions{Timeout: &timeout})
	}

	return nil
}

// PruneSessions cleans up stopped or abandoned Vessel containers, networks, and worktrees.
func PruneSessions(ctx context.Context, cli client.CommonAPIClient, pruneAll bool, workspaceFilter string) (*PruneResult, error) {
	res := &PruneResult{
		ContainersDeleted: []string{},
		NetworksDeleted:   []string{},
		WorktreesPruned:   []string{},
	}

	if cli == nil {
		return res, nil
	}

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "vessel.managed=true")

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers for prune: %w", err)
	}

	prunedSessionIDs := make(map[string]string) // sessionID -> workspacePath

	for _, c := range containers {
		wsPath := c.Labels["vessel.workspace"]
		if workspaceFilter != "" && wsPath != "" && wsPath != workspaceFilter {
			continue
		}

		shouldPrune := pruneAll || c.State != "running"
		if shouldPrune {
			err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
			if err == nil {
				name := c.ID
				if len(c.Names) > 0 {
					name = strings.TrimPrefix(c.Names[0], "/")
				}
				res.ContainersDeleted = append(res.ContainersDeleted, name)
				if sid := c.Labels["vessel.session_id"]; sid != "" {
					prunedSessionIDs[sid] = wsPath
				}
			}
		}
	}

	// Remove networks matching vessel.managed=true
	netFilter := filters.NewArgs()
	netFilter.Add("label", "vessel.managed=true")
	networks, err := cli.NetworkList(ctx, network.ListOptions{Filters: netFilter})
	if err == nil {
		for _, n := range networks {
			sid := n.Labels["vessel.session_id"]
			shouldPruneNet := pruneAll
			if !shouldPruneNet && sid != "" {
				if _, ok := prunedSessionIDs[sid]; ok {
					shouldPruneNet = true
				}
			}
			if shouldPruneNet {
				if err := cli.NetworkRemove(ctx, n.ID); err == nil {
					res.NetworksDeleted = append(res.NetworksDeleted, n.Name)
				}
			}
		}
	}

	// Prune worktrees for deleted sessions
	for sid, ws := range prunedSessionIDs {
		if ws == "" {
			continue
		}
		wtDir := filepath.Join(ws, ".vessel", "worktrees", fmt.Sprintf("vessel-%s", sid))
		branchName := fmt.Sprintf("vessel/session-%s", sid)

		if _, err := os.Stat(wtDir); err == nil {
			cmdRm := exec.CommandContext(ctx, "git", "-C", ws, "worktree", "remove", "--force", wtDir)
			_ = cmdRm.Run()
			_ = os.RemoveAll(wtDir)
			res.WorktreesPruned = append(res.WorktreesPruned, wtDir)
		}

		cmdBranch := exec.CommandContext(ctx, "git", "-C", ws, "branch", "-D", branchName)
		_ = cmdBranch.Run()
	}

	return res, nil
}
