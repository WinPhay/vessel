package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/WinPhay/vessel/internal/mcp"
	"github.com/WinPhay/vessel/internal/sandbox"
	"github.com/WinPhay/vessel/internal/tokenfilter"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

var Version = "0.1.0-alpha"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	switch command {
	case "version":
		fmt.Printf("Vessel v%s (The local time-traveling sandbox engine)\n", strings.TrimPrefix(Version, "v"))
	case "mcp":
		runMCP(os.Args[2:])
	case "exec":
		runExec(os.Args[2:])
	case "checkpoint":
		runCheckpoint(os.Args[2:])
	case "rollback":
		runRollback(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "process":
		runProcess(os.Args[2:])
	case "service":
		runService(os.Args[2:])
	case "worktree":
		runWorktree(os.Args[2:])
	case "session":
		runSession(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func parseInterspersed(flags *flag.FlagSet, args []string) error {
	var flagArgs []string
	var nonFlagArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			flagName := strings.TrimLeft(arg, "-")
			if !strings.Contains(flagName, "=") {
				f := flags.Lookup(flagName)
				if f != nil {
					if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bf.IsBoolFlag() {
						if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
							i++
							flagArgs = append(flagArgs, args[i])
						}
					}
				}
			}
		} else {
			nonFlagArgs = append(nonFlagArgs, arg)
		}
	}
	return flags.Parse(append(flagArgs, nonFlagArgs...))
}

func runExec(args []string) {
	execFlags := flag.NewFlagSet("exec", flag.ExitOnError)
	workspace := execFlags.String("workspace", ".", "Host directory to mount into /workspace")
	image := execFlags.String("image", "alpine:latest", "Docker container image or alias (agent, ubuntu, alpine)")
	network := execFlags.String("network", "bridge", "Network mode (bridge, none, host)")
	memStr := execFlags.String("memory", "", "Memory limit (e.g. '512m', '2g', '4gb'; default: unlimited)")
	cpus := execFlags.Float64("cpus", 0.0, "CPU core limit (e.g. 1.5, 2.0; default: unlimited)")
	runtime := execFlags.String("runtime", "", "OCI container runtime (e.g. 'runc', 'runsc' for gVisor)")
	var secrets multiStringFlag
	execFlags.Var(&secrets, "secret", "Custom secret to inject and redact in form KEY=VALUE (repeatable)")
	var services multiStringFlag
	execFlags.Var(&services, "service", "Sidecar service to spawn on session network in form NAME=IMAGE (repeatable)")
	var servicePorts multiStringFlag
	execFlags.Var(&servicePorts, "service-port", "Host port mapping for services in form [NAME=]HOST:CONTAINER (e.g. 5432:5432 or db=5432:5432). Strictly bound to 127.0.0.1. (repeatable)")
	execFlags.Var(&servicePorts, "port", "Alias for --service-port")
	useWorktree := execFlags.Bool("worktree", false, "Isolate session in an ephemeral Git worktree")
	maskSecrets := execFlags.Bool("mask-secrets", true, "Enable automatic secret redaction and masking")
	session := execFlags.String("session", "", "Named session ID for reconnect and persistence")
	persist := execFlags.Bool("persist", false, "Persist container, worktree, and service mesh across runs")
	maxTokens := execFlags.Int64("max-tokens", 0, "Max cumulative tokens permitted for session before tripping circuit breaker (0 = unlimited)")
	maxDuration := execFlags.String("max-duration", "", "Max cumulative execution duration (e.g. '30s', '5m', '1h'; default: unlimited)")
	_ = parseInterspersed(execFlags, args)

	remaining := execFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: vessel exec requires a command to run inside the sandbox")
		fmt.Fprintln(os.Stderr, "Usage: vessel exec [--workspace <dir>] [--image <img>] [--network <mode>] [--memory <limit>] [--cpus <cores>] [--runtime <runtime>] [--secret <KEY=VAL>] [--service <NAME=IMG>] [--worktree] [--mask-secrets] \"<command>\"")
		os.Exit(1)
	}

	cmdToRun := remaining[0]
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !*maskSecrets {
		tokenfilter.SetSecretMasking(false)
	}

	cfg := sandbox.DefaultDockerConfig(*workspace)
	if *useWorktree {
		cfg.UseWorktree = true
	}
	if *session != "" {
		cfg.SessionID = *session
	}
	if *persist {
		cfg.Persistent = true
	}
	if *maxTokens > 0 {
		cfg.MaxTokens = *maxTokens
	}
	if *maxDuration != "" {
		d, err := sandbox.ParseDurationString(*maxDuration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid --max-duration: %v\n", err)
			os.Exit(1)
		}
		cfg.MaxDuration = d
	}
	if *image != "" {
		cfg.ImageName = *image
	}
	if *network != "" {
		cfg.NetworkMode = *network
	}
	if *memStr != "" {
		memBytes, err := sandbox.ParseMemoryString(*memStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg.MemoryBytes = memBytes
	}
	if *cpus > 0 {
		cfg.CPUShares = *cpus
	}
	if *runtime != "" {
		cfg.Runtime = *runtime
	}
	for _, s := range secrets {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			cfg.Secrets[parts[0]] = parts[1]
			tokenfilter.RegisterSecret(parts[0], parts[1])
		}
	}

	runtimeDesc := cfg.Runtime
	if runtimeDesc == "" {
		runtimeDesc = "default"
	}
	fmt.Printf("[vessel] Initializing sandbox container (image: %s, network: %s, runtime: %s, workspace: %s)...\n", cfg.ImageName, cfg.NetworkMode, runtimeDesc, cfg.WorkspacePath)
	sb, err := sandbox.NewDockerSandbox(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error: unable to start Docker sandbox: %v\n", err)
		fmt.Fprintln(os.Stderr, "[vessel] Note: ensure the Docker daemon is running and accessible via /var/run/docker.sock.")
		os.Exit(1)
	}
	defer sb.Close()

	svcPortsMap := make(map[string][]string)
	for _, p := range servicePorts {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) == 2 {
			svcPortsMap[strings.TrimSpace(parts[0])] = append(svcPortsMap[strings.TrimSpace(parts[0])], strings.TrimSpace(parts[1]))
		} else {
			svcPortsMap["_default"] = append(svcPortsMap["_default"], strings.TrimSpace(parts[0]))
		}
	}

	for i, s := range services {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			svcName := strings.TrimSpace(parts[0])
			svcImage := strings.TrimSpace(parts[1])
			portsToPass := append([]string{}, svcPortsMap[svcName]...)
			if len(portsToPass) == 0 && (i == 0 || len(services) == 1) {
				portsToPass = append(portsToPass, svcPortsMap["_default"]...)
			}
			portDesc := ""
			if len(portsToPass) > 0 {
				portDesc = fmt.Sprintf(" with ports %v", portsToPass)
			}
			fmt.Printf("[vessel] Spawning sidecar service %s (%s)%s...\n", svcName, svcImage, portDesc)
			_, err := sb.SpawnService(ctx, svcName, svcImage, nil, "", portsToPass...)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[vessel] Warning: failed to spawn sidecar %s: %v\n", svcName, err)
			}
		}
	}

	fmt.Printf("[vessel] Executing: %s\n", cmdToRun)
	res, err := sb.Exec(ctx, cmdToRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Execution error: %v\n", err)
		_ = sb.Close()
		os.Exit(1)
	}

	if res.Stdout != "" {
		fmt.Println(res.Stdout)
	}
	if res.Stderr != "" {
		fmt.Fprintln(os.Stderr, res.Stderr)
	}

	fmt.Printf("\n[vessel] Exit code: %d | Duration: %s | Estimated tokens: %d\n", res.ExitCode, res.Duration, res.TokenEstimate)
	exitCode := res.ExitCode
	_ = sb.Close()
	os.Exit(exitCode)
}

func runMCP(args []string) {
	mcpFlags := flag.NewFlagSet("mcp", flag.ExitOnError)
	workspace := mcpFlags.String("workspace", ".", "Host directory to mount into /workspace")
	image := mcpFlags.String("image", "alpine:latest", "Docker container image or alias (agent, ubuntu, alpine)")
	network := mcpFlags.String("network", "bridge", "Network mode (bridge, none, host)")
	memStr := mcpFlags.String("memory", "", "Memory limit (e.g. '512m', '2g', '4gb'; default: unlimited)")
	cpus := mcpFlags.Float64("cpus", 0.0, "CPU core limit (e.g. 1.5, 2.0; default: unlimited)")
	runtime := mcpFlags.String("runtime", "", "OCI container runtime (e.g. 'runc', 'runsc' for gVisor)")
	var secrets multiStringFlag
	mcpFlags.Var(&secrets, "secret", "Custom secret to inject and redact in form KEY=VALUE (repeatable)")
	var services multiStringFlag
	mcpFlags.Var(&services, "service", "Sidecar service to spawn on session network in form NAME=IMAGE (repeatable)")
	var servicePorts multiStringFlag
	mcpFlags.Var(&servicePorts, "service-port", "Host port mapping for services in form [NAME=]HOST:CONTAINER (e.g. 5432:5432 or db=5432:5432). Strictly bound to 127.0.0.1. (repeatable)")
	mcpFlags.Var(&servicePorts, "port", "Alias for --service-port")
	useWorktree := mcpFlags.Bool("worktree", false, "Isolate session in an ephemeral Git worktree")
	maskSecrets := mcpFlags.Bool("mask-secrets", true, "Enable automatic secret redaction and masking")
	session := mcpFlags.String("session", "", "Named session ID for reconnect and persistence")
	persist := mcpFlags.Bool("persist", false, "Persist container, worktree, and service mesh across runs")
	maxTokens := mcpFlags.Int64("max-tokens", 0, "Max cumulative tokens permitted for session before tripping circuit breaker (0 = unlimited)")
	maxDuration := mcpFlags.String("max-duration", "", "Max cumulative execution duration (e.g. '30s', '5m', '1h'; default: unlimited)")
	useMock := mcpFlags.Bool("mock", false, "Run in mock sandbox mode (for testing without Docker)")
	_ = parseInterspersed(mcpFlags, args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !*maskSecrets {
		tokenfilter.SetSecretMasking(false)
	}

	var sb sandbox.Sandbox
	cfg := sandbox.DefaultDockerConfig(*workspace)
	if *useWorktree {
		cfg.UseWorktree = true
	}
	if *session != "" {
		cfg.SessionID = *session
	}
	if *persist {
		cfg.Persistent = true
	}
	if *maxTokens > 0 {
		cfg.MaxTokens = *maxTokens
	}
	if *maxDuration != "" {
		d, err := sandbox.ParseDurationString(*maxDuration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid --max-duration: %v\n", err)
			os.Exit(1)
		}
		cfg.MaxDuration = d
	}
	if *image != "" {
		cfg.ImageName = *image
	}
	if *network != "" {
		cfg.NetworkMode = *network
	}
	if *memStr != "" {
		memBytes, err := sandbox.ParseMemoryString(*memStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg.MemoryBytes = memBytes
	}
	if *cpus > 0 {
		cfg.CPUShares = *cpus
	}
	if *runtime != "" {
		cfg.Runtime = *runtime
	}
	for _, s := range secrets {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			cfg.Secrets[parts[0]] = parts[1]
			tokenfilter.RegisterSecret(parts[0], parts[1])
		}
	}

	if *useMock {
		mockSB := sandbox.NewMockSandbox()
		if *session != "" {
			mockSB.SetSessionInfo(*session, *persist, false)
		}
		sb = mockSB
	} else {
		realSB, err := sandbox.NewDockerSandbox(ctx, cfg, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Failed to initialize Docker sandbox for MCP: %v\n", err)
			fmt.Fprintln(os.Stderr, "[vessel] Tip: use --mock for testing without an active Docker daemon.")
			os.Exit(1)
		}
		sb = realSB
	}
	defer sb.Close()

	svcPortsMap := make(map[string][]string)
	for _, p := range servicePorts {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) == 2 {
			svcPortsMap[strings.TrimSpace(parts[0])] = append(svcPortsMap[strings.TrimSpace(parts[0])], strings.TrimSpace(parts[1]))
		} else {
			svcPortsMap["_default"] = append(svcPortsMap["_default"], strings.TrimSpace(parts[0]))
		}
	}

	for i, s := range services {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			svcName := strings.TrimSpace(parts[0])
			svcImage := strings.TrimSpace(parts[1])
			portsToPass := append([]string{}, svcPortsMap[svcName]...)
			if len(portsToPass) == 0 && (i == 0 || len(services) == 1) {
				portsToPass = append(portsToPass, svcPortsMap["_default"]...)
			}
			portDesc := ""
			if len(portsToPass) > 0 {
				portDesc = fmt.Sprintf(" with ports %v", portsToPass)
			}
			fmt.Printf("[vessel] Spawning sidecar service %s (%s)%s...\n", svcName, svcImage, portDesc)
			_, err := sb.SpawnService(ctx, svcName, svcImage, nil, "", portsToPass...)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[vessel] Warning: failed to spawn sidecar %s: %v\n", svcName, err)
			}
		}
	}

	server := mcp.NewServer(sb, cfg.WorkspacePath, os.Stdin, os.Stdout)
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] MCP server loop exited: %v\n", err)
	}
}

func runCheckpoint(args []string) {
	cpFlags := flag.NewFlagSet("checkpoint", flag.ExitOnError)
	workspace := cpFlags.String("workspace", ".", "Host directory to manage snapshots for")
	_ = parseInterspersed(cpFlags, args)

	sm, err := sandbox.NewSnapshotManager(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error initializing snapshot manager: %v\n", err)
		os.Exit(1)
	}

	remaining := cpFlags.Args()
	if len(remaining) == 0 || remaining[0] == "list" {
		tags, err := sm.ListSnapshots()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Error listing snapshots: %v\n", err)
			os.Exit(1)
		}
		if len(tags) == 0 {
			fmt.Println("[vessel] No checkpoints found for workspace.")
			return
		}
		fmt.Println("[vessel] Active checkpoints:")
		for _, tag := range tags {
			fmt.Printf("  • %s\n", tag)
		}
		return
	}

	tag := remaining[0]
	if tag == "create" {
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Error: checkpoint tag name required")
			fmt.Fprintln(os.Stderr, "Usage: vessel checkpoint [create] <tag> [--workspace <dir>]")
			os.Exit(1)
		}
		tag = remaining[1]
	}

	if err := sandbox.ValidateCheckpointTag(tag); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Invalid checkpoint tag: %v\n", err)
		os.Exit(1)
	}

	if err := sm.CreateSnapshot(tag); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error creating checkpoint %s: %v\n", tag, err)
		os.Exit(1)
	}

	fmt.Printf("[vessel] Checkpoint '%s' created successfully for workspace: %s\n", tag, *workspace)
}

func runRollback(args []string) {
	rbFlags := flag.NewFlagSet("rollback", flag.ExitOnError)
	workspace := rbFlags.String("workspace", ".", "Host directory to rollback")
	_ = parseInterspersed(rbFlags, args)

	remaining := rbFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: rollback requires a checkpoint tag")
		fmt.Fprintln(os.Stderr, "Usage: vessel rollback <tag> [--workspace <dir>]")
		os.Exit(1)
	}

	tag := remaining[0]
	if err := sandbox.ValidateCheckpointTag(tag); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Invalid checkpoint tag: %v\n", err)
		os.Exit(1)
	}

	sm, err := sandbox.NewSnapshotManager(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error initializing snapshot manager: %v\n", err)
		os.Exit(1)
	}

	if err := sm.RestoreSnapshot(tag); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error rolling back to checkpoint '%s': %v\n", tag, err)
		os.Exit(1)
	}

	fmt.Printf("[vessel] Workspace successfully restored to checkpoint '%s'.\n", tag)
}

func runStatus(args []string) {
	stFlags := flag.NewFlagSet("status", flag.ExitOnError)
	workspace := stFlags.String("workspace", ".", "Host directory to inspect")
	session := stFlags.String("session", "", "Inspect specific persistent session ID")
	_ = parseInterspersed(stFlags, args)

	absPath, err := filepath.Abs(*workspace)
	if err != nil {
		absPath = *workspace
	}

	fmt.Printf("Vessel Engine Status (v%s)\n", Version)
	fmt.Printf("  Workspace:    %s\n", absPath)
	if *session != "" {
		fmt.Printf("  Target Session: %s\n", *session)
	}

	sm, err := sandbox.NewSnapshotManager(absPath)
	if err != nil {
		fmt.Printf("  Snapshots:    unavailable (%v)\n", err)
	} else {
		tags, _ := sm.ListSnapshots()
		if len(tags) == 0 {
			fmt.Printf("  Snapshots:    0 active\n")
		} else {
			fmt.Printf("  Snapshots:    %d active (%s)\n", len(tags), strings.Join(tags, ", "))
		}
	}

	// Check Docker connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Printf("  Docker:       disconnected\n")
	} else {
		_, pingErr := cli.Ping(ctx)
		if pingErr != nil {
			fmt.Printf("  Docker:       disconnected (daemon not running)\n")
		} else {
			fmt.Printf("  Docker:       connected (daemon online)\n")
		}
		_ = cli.Close()
	}
	fmt.Printf("  Default Mode: isolated (image: %s)\n", sandbox.ImageAlpineRaw)
	fmt.Printf("  Drivers:      docker (active), mock, microvm (scaffold)\n")
	fmt.Printf("  OCI Runtime:  daemon default (override via --runtime runsc for gVisor)\n")
}

func runProcess(args []string) {
	procFlags := flag.NewFlagSet("process", flag.ExitOnError)
	workspace := procFlags.String("workspace", ".", "Host directory of workspace")
	signalFlag := procFlags.String("signal", "SIGTERM", "Signal to send (SIGTERM or SIGKILL)")
	maxBytes := procFlags.Int("max-bytes", 65536, "Max bytes to display for logs")
	_ = parseInterspersed(procFlags, args)

	remaining := procFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process subcommand required (list, kill, logs)")
		fmt.Fprintln(os.Stderr, "Usage: vessel process <list | kill <id> | logs <id>> [--workspace <dir>]")
		os.Exit(1)
	}

	action := remaining[0]
	switch action {
	case "list":
		absPath, _ := filepath.Abs(*workspace)
		fmt.Printf("Active Background Processes (workspace: %s):\n", absPath)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Printf("  Docker disconnected: %v\n", err)
			return
		}
		defer cli.Close()

		containers, err := cli.ContainerList(ctx, container.ListOptions{All: false})
		if err != nil {
			fmt.Printf("  Error querying containers: %v\n", err)
			return
		}

		found := 0
		fmt.Println("  CONTAINER ID        CONTAINER NAME      STATUS     COMMAND")
		for _, c := range containers {
			for _, name := range c.Names {
				if strings.Contains(name, "vessel-sandbox") {
					found++
					cID := c.ID
					if len(cID) > 12 {
						cID = cID[:12]
					}
					fmt.Printf("  %-18s  %-18s  %-9s  %s\n", cID, strings.TrimPrefix(name, "/"), c.State, c.Command)
				}
			}
		}
		if found == 0 {
			fmt.Println("  No active container processes found.")
		}

	case "kill":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Error: process kill requires a process or container ID")
			fmt.Fprintln(os.Stderr, "Usage: vessel process kill <id> [--signal <SIGTERM|SIGKILL>]")
			os.Exit(1)
		}
		targetID := remaining[1]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Docker client error: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		err = cli.ContainerKill(ctx, targetID, *signalFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Failed to send %s to process %s: %v\n", *signalFlag, targetID, err)
			os.Exit(1)
		}
		fmt.Printf("[vessel] Process %s terminated with signal %s.\n", targetID, *signalFlag)

	case "logs":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Error: process logs requires a process or container ID")
			fmt.Fprintln(os.Stderr, "Usage: vessel process logs <id> [--max-bytes <N>]")
			os.Exit(1)
		}
		targetID := remaining[1]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Docker client error: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		logReader, err := cli.ContainerLogs(ctx, targetID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail:       "100",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Failed to fetch logs for %s: %v\n", targetID, err)
			os.Exit(1)
		}
		defer logReader.Close()

		buf := make([]byte, *maxBytes)
		n, _ := logReader.Read(buf)
		cleaned := tokenfilter.CleanOutput(string(buf[:n]))
		fmt.Println(cleaned)

	default:
		fmt.Fprintf(os.Stderr, "Unknown process action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Usage: vessel process <list | kill <id> | logs <id>>")
		os.Exit(1)
	}
}

func runService(args []string) {
	serviceFlags := flag.NewFlagSet("service", flag.ExitOnError)
	_ = parseInterspersed(serviceFlags, args)

	remaining := serviceFlags.Args()
	if len(remaining) == 0 || remaining[0] == "list" {
		fmt.Println("[vessel] Active Sidecar Services:")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Printf("  Docker disconnected: %v\n", err)
			return
		}
		defer cli.Close()

		containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
		if err != nil {
			fmt.Printf("  Error querying containers: %v\n", err)
			return
		}
		found := 0
		fmt.Println("  CONTAINER ID        SERVICE NAME        IMAGE               STATUS")
		for _, c := range containers {
			for _, name := range c.Names {
				if strings.Contains(name, "vessel-svc-") {
					found++
					cID := c.ID
					if len(cID) > 12 {
						cID = cID[:12]
					}
					cleanName := strings.TrimPrefix(name, "/")
					cleanName = strings.TrimPrefix(cleanName, "vessel-svc-")
					if idx := strings.LastIndex(cleanName, "-"); idx != -1 {
						cleanName = cleanName[:idx]
					}
					img := c.Image
					if len(img) > 18 {
						img = img[:15] + "..."
					}
					fmt.Printf("  %-18s  %-18s  %-18s  %s\n", cID, cleanName, img, c.State)
				}
			}
		}
		if found == 0 {
			fmt.Println("  No active sidecar services found.")
		}
		return
	}

	action := remaining[0]
	switch action {
	case "stop":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Error: service stop requires a service name or container ID")
			fmt.Fprintln(os.Stderr, "Usage: vessel service stop <name|id>")
			os.Exit(1)
		}
		target := remaining[1]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Docker client error: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		stopTimeout := 2
		_ = cli.ContainerStop(ctx, target, container.StopOptions{Timeout: &stopTimeout})
		err = cli.ContainerRemove(ctx, target, container.RemoveOptions{Force: true})
		if err != nil {
			// Search by name substring
			containers, _ := cli.ContainerList(ctx, container.ListOptions{All: true})
			found := false
			for _, c := range containers {
				for _, name := range c.Names {
					if strings.Contains(name, fmt.Sprintf("vessel-svc-%s-", target)) {
						_ = cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &stopTimeout})
						_ = cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
						found = true
						break
					}
				}
			}
			if !found {
				fmt.Fprintf(os.Stderr, "[vessel] Failed to stop service %s: %v\n", target, err)
				os.Exit(1)
			}
		}
		fmt.Printf("[vessel] Service %s stopped and removed.\n", target)

	default:
		fmt.Fprintf(os.Stderr, "Unknown service action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Usage: vessel service [list | stop <name>]")
		os.Exit(1)
	}
}

func runWorktree(args []string) {
	wtFlags := flag.NewFlagSet("worktree", flag.ExitOnError)
	workspace := wtFlags.String("workspace", ".", "Host workspace directory")
	maxBytes := wtFlags.Int("max-bytes", 65536, "Max bytes of unified diff to display")
	_ = parseInterspersed(wtFlags, args)

	remaining := wtFlags.Args()
	if len(remaining) == 0 || remaining[0] == "status" {
		absPath, _ := filepath.Abs(*workspace)
		fmt.Printf("[vessel] Git Worktree Status (workspace: %s):\n", absPath)
		if !sandbox.IsGitRepo(absPath) {
			fmt.Println("  Workspace is not inside a Git repository. Worktree shield inactive.")
			return
		}
		wtDir := filepath.Join(absPath, ".vessel", "worktrees")
		entries, err := os.ReadDir(wtDir)
		if err != nil || len(entries) == 0 {
			fmt.Println("  No active session worktrees found.")
			return
		}
		fmt.Println("  ACTIVE WORKTREES:")
		for _, entry := range entries {
			if entry.IsDir() {
				fmt.Printf("  • %s (%s)\n", entry.Name(), filepath.Join(wtDir, entry.Name()))
			}
		}
		return
	}

	action := remaining[0]
	switch action {
	case "diff":
		absPath, _ := filepath.Abs(*workspace)
		if !sandbox.IsGitRepo(absPath) {
			fmt.Println("  Workspace is not inside a Git repository.")
			return
		}

		targetDir := absPath
		wtDir := filepath.Join(absPath, ".vessel", "worktrees")
		entries, err := os.ReadDir(wtDir)
		if err == nil && len(entries) > 0 {
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i].IsDir() {
					targetDir = filepath.Join(wtDir, entries[i].Name())
					break
				}
			}
		}

		cmd := exec.Command("git", "-C", targetDir, "diff", "HEAD")
		out, err := cmd.Output()
		if err != nil {
			fmt.Printf("  Diff error: %v\n", err)
			return
		}

		rawDiff := string(out)
		if strings.TrimSpace(rawDiff) == "" {
			fmt.Println("  No modifications found.")
			return
		}

		cleaned := tokenfilter.CleanOutput(rawDiff)
		truncated := false
		if *maxBytes > 0 && len(cleaned) > *maxBytes {
			cleaned, truncated = tokenfilter.TruncateOutput(cleaned, *maxBytes)
		}

		fmt.Printf("[vessel] Diff for %s:\n", targetDir)
		fmt.Println(cleaned)
		if truncated {
			fmt.Println("\n[vessel] (diff truncated to max-bytes)")
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown worktree action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Usage: vessel worktree [status | diff] [--workspace <dir>]")
		os.Exit(1)
	}
}

func runSession(args []string) {
	sessionFlags := flag.NewFlagSet("session", flag.ExitOnError)
	workspace := sessionFlags.String("workspace", "", "Filter sessions by workspace path")
	jsonOutput := sessionFlags.Bool("json", false, "Emit structured JSON output")
	allFlag := sessionFlags.Bool("all", false, "Operate on all sessions including running ones (prune)")
	_ = parseInterspersed(sessionFlags, args)

	remaining := sessionFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: session subcommand required (list, inspect, stop, prune)")
		fmt.Fprintln(os.Stderr, "Usage: vessel session <list | inspect <id> | stop <id> | prune [--all] [--workspace <dir>]>")
		os.Exit(1)
	}

	action := remaining[0]
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error connecting to Docker: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()

	switch action {
	case "list":
		sessions, err := sandbox.ListSessions(ctx, cli)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		if *workspace != "" {
			absFilter, _ := filepath.Abs(*workspace)
			filtered := make([]*sandbox.SessionSummary, 0)
			for _, s := range sessions {
				if s.Workspace == absFilter || s.Workspace == *workspace {
					filtered = append(filtered, s)
				}
			}
			sessions = filtered
		}

		if *jsonOutput {
			payload, _ := json.MarshalIndent(sessions, "", "  ")
			fmt.Println(string(payload))
			return
		}

		if len(sessions) == 0 {
			fmt.Println("[vessel] No active or persistent sessions found.")
			return
		}

		fmt.Printf("%-24s %-10s %-14s %-20s %-12s %s\n", "SESSION ID", "STATUS", "CONTAINER", "IMAGE", "PERSISTENT", "SIDECARS")
		fmt.Println(strings.Repeat("─", 90))
		for _, s := range sessions {
			sidecarsStr := "-"
			if len(s.Sidecars) > 0 {
				sidecarsStr = strings.Join(s.Sidecars, ", ")
			}
			persistStr := "no"
			if s.Persistent {
				persistStr = "yes"
			}
			fmt.Printf("%-24s %-10s %-14s %-20s %-12s %s\n", s.SessionID, s.Status, s.ContainerID, s.Image, persistStr, sidecarsStr)
		}

	case "inspect":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Error: inspect requires a session ID")
			fmt.Fprintln(os.Stderr, "Usage: vessel session inspect <session_id>")
			os.Exit(1)
		}
		targetID := remaining[1]
		detail, err := sandbox.InspectSession(ctx, cli, targetID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Inspect error: %v\n", err)
			os.Exit(1)
		}
		payload, _ := json.MarshalIndent(detail, "", "  ")
		fmt.Println(string(payload))

	case "stop":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Error: stop requires a session ID")
			fmt.Fprintln(os.Stderr, "Usage: vessel session stop <session_id>")
			os.Exit(1)
		}
		targetID := remaining[1]
		if err := sandbox.StopSession(ctx, cli, targetID); err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Failed to stop session %s: %v\n", targetID, err)
			os.Exit(1)
		}
		fmt.Printf("[vessel] Session %q stopped successfully.\n", targetID)

	case "prune":
		workspaceFilter := ""
		if *workspace != "" {
			workspaceFilter, _ = filepath.Abs(*workspace)
		}
		res, err := sandbox.PruneSessions(ctx, cli, *allFlag, workspaceFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vessel] Prune error: %v\n", err)
			os.Exit(1)
		}

		if *jsonOutput {
			payload, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(payload))
			return
		}

		total := len(res.ContainersDeleted) + len(res.NetworksDeleted) + len(res.WorktreesPruned)
		if total == 0 {
			fmt.Println("[vessel] No dormant sessions, networks, or worktrees found to prune.")
			return
		}

		fmt.Printf("[vessel] Pruned %d container(s), %d network(s), %d worktree(s):\n",
			len(res.ContainersDeleted), len(res.NetworksDeleted), len(res.WorktreesPruned))
		for _, c := range res.ContainersDeleted {
			fmt.Printf("  • Container: %s\n", c)
		}
		for _, n := range res.NetworksDeleted {
			fmt.Printf("  • Network:   %s\n", n)
		}
		for _, w := range res.WorktreesPruned {
			fmt.Printf("  • Worktree:  %s\n", w)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown session action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Usage: vessel session <list | inspect <id> | stop <id> | prune [--all] [--workspace <dir>]>")
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("Vessel v%s — Local AI Agent Sandbox Engine\n\n", strings.TrimPrefix(Version, "v"))
	fmt.Println("Usage:")
	fmt.Println("  vessel <command> [options] [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  exec        Execute a command inside the sandbox container")
	fmt.Println("              Flags: --workspace <dir>, --image <image/alias>, --network <mode>, --memory <limit>, --cpus <cores>, --runtime <runtime>, --secret <KEY=VAL>, --service <NAME=IMG>, --worktree, --mask-secrets")
	fmt.Println("  mcp         Start stdio Model Context Protocol server")
	fmt.Println("              Flags: --workspace <dir>, --image <image/alias>, --network <mode>, --memory <limit>, --cpus <cores>, --runtime <runtime>, --secret <KEY=VAL>, --service <NAME=IMG>, --worktree, --mask-secrets, --mock")
	fmt.Println("  session     Inspect and prune persistent sessions and sidecars")
	fmt.Println("              Usage: vessel session [list | inspect <id> | stop <id> | prune [--all] [--workspace <dir>]]")
	fmt.Println("  worktree    Inspect ephemeral Git worktrees and diffs")
	fmt.Println("              Usage: vessel worktree [status | diff] [--workspace <dir>]")
	fmt.Println("  service     Manage ephemeral sidecar containers")
	fmt.Println("              Usage: vessel service [list | stop <name>]")
	fmt.Println("  process     Manage background container processes")
	fmt.Println("              Usage: vessel process [list | kill <id> | logs <id>] [--workspace <dir>]")
	fmt.Println("  checkpoint  Create or list filesystem checkpoints")
	fmt.Println("              Usage: vessel checkpoint [create <tag> | list | <tag>] [--workspace <dir>]")
	fmt.Println("  rollback    Restore workspace to a named checkpoint")
	fmt.Println("              Usage: vessel rollback <tag> [--workspace <dir>]")
	fmt.Println("  status      Display sandbox engine health and active snapshots")
	fmt.Println("              Usage: vessel status [--workspace <dir>]")
	fmt.Println("  version     Display version information")
	fmt.Println("  help        Show this help text")
	fmt.Println()
	fmt.Println("Image Aliases:")
	fmt.Println("  agent       Lightweight pre-baked agent with git, ripgrep, python3 (vessel-agent:alpine)")
	fmt.Println("  ubuntu      Full developer environment with build-essential (vessel-agent:ubuntu)")
	fmt.Println("  browser     Headless Chromium environment for web testing & inspection (vessel-agent:browser)")
	fmt.Println("  alpine      Minimal raw alpine image (alpine:latest)")
	fmt.Println()
	fmt.Println("Network Modes:")
	fmt.Println("  bridge      Standard container network with outbound Internet access (default)")
	fmt.Println("  none        Complete airgap isolation: zero network interfaces or outbound sockets")
	fmt.Println("  host        Share host networking stack")
	fmt.Println()
	fmt.Println("Resource Quotas:")
	fmt.Println("  --memory    Limit container RAM (e.g. '512m', '2g', '4gb'; default: unlimited)")
	fmt.Println("  --cpus      Limit container CPU cores (e.g. '1.5', '2.0'; default: unlimited)")
	fmt.Println()
	fmt.Println("OCI Runtimes:")
	fmt.Println("  runc        Standard OCI container runtime (default)")
	fmt.Println("  runsc       Google gVisor application-kernel sandbox for hostile code isolation")
	fmt.Println("  kata        Kata Containers lightweight VM runtime")
	fmt.Println()
	fmt.Println("Secret & DLP Masking:")
	fmt.Println("  --secret        Inject environment secret into container and redact on egress (e.g. '--secret API_KEY=xyz')")
	fmt.Println("  --mask-secrets  Enable automatic secret redaction and masking (default: true)")
	fmt.Println()
	fmt.Println("Sidecar Services:")
	fmt.Println("  --service       Spawn background sidecar service on session network (e.g. '--service db=postgres:15-alpine')")
	fmt.Println()
	fmt.Println("Git Worktree Shield:")
	fmt.Println("  --worktree      Mount ephemeral Git worktree instead of raw workspace to shield host from unreviewed edits")
	fmt.Println()
	fmt.Println("Session Persistence:")
	fmt.Println("  --session       Attach to or create a named persistent session (e.g. '--session my-project')")
	fmt.Println("  --persist       Keep container, worktree, and service mesh alive upon CLI exit for subsequent reconnect")
	fmt.Println()
	fmt.Println("Budget & Circuit Breakers:")
	fmt.Println("  --max-tokens    Max cumulative tokens permitted before tripping circuit breaker (e.g. '--max-tokens 50000')")
	fmt.Println("  --max-duration  Max cumulative execution duration before tripping circuit breaker (e.g. '--max-duration 15m')")
}
