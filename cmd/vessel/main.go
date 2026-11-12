package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/WinPhay/vessel/internal/mcp"
	"github.com/WinPhay/vessel/internal/sandbox"
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
		fmt.Printf("Vessel v%s (The local time-traveling sandbox engine)\n", Version)
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
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func runExec(args []string) {
	execFlags := flag.NewFlagSet("exec", flag.ExitOnError)
	workspace := execFlags.String("workspace", ".", "Host directory to mount into /workspace")
	image := execFlags.String("image", "alpine:latest", "Docker container image or alias (agent, ubuntu, alpine)")
	network := execFlags.String("network", "bridge", "Network mode (bridge, none, host)")
	memStr := execFlags.String("memory", "", "Memory limit (e.g. '512m', '2g', '4gb'; default: unlimited)")
	cpus := execFlags.Float64("cpus", 0.0, "CPU core limit (e.g. 1.5, 2.0; default: unlimited)")
	_ = execFlags.Parse(args)

	remaining := execFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: vessel exec requires a command to run inside the sandbox")
		fmt.Fprintln(os.Stderr, "Usage: vessel exec [--workspace <dir>] [--image <img>] [--network <mode>] [--memory <limit>] [--cpus <cores>] \"<command>\"")
		os.Exit(1)
	}

	cmdToRun := remaining[0]
	ctx := context.Background()

	cfg := sandbox.DefaultDockerConfig(*workspace)
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

	fmt.Printf("[vessel] Initializing sandbox container (image: %s, network: %s, workspace: %s)...\n", cfg.ImageName, cfg.NetworkMode, cfg.WorkspacePath)
	sb, err := sandbox.NewDockerSandbox(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error: unable to start Docker sandbox: %v\n", err)
		fmt.Fprintln(os.Stderr, "[vessel] Note: ensure the Docker daemon is running and accessible via /var/run/docker.sock.")
		os.Exit(1)
	}
	defer sb.Close()

	fmt.Printf("[vessel] Executing: %s\n", cmdToRun)
	res, err := sb.Exec(ctx, cmdToRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Execution error: %v\n", err)
		os.Exit(1)
	}

	if res.Stdout != "" {
		fmt.Println(res.Stdout)
	}
	if res.Stderr != "" {
		fmt.Fprintln(os.Stderr, res.Stderr)
	}

	fmt.Printf("\n[vessel] Exit code: %d | Duration: %s | Estimated tokens: %d\n", res.ExitCode, res.Duration, res.TokenEstimate)
	os.Exit(res.ExitCode)
}

func runMCP(args []string) {
	mcpFlags := flag.NewFlagSet("mcp", flag.ExitOnError)
	workspace := mcpFlags.String("workspace", ".", "Host directory to mount into /workspace")
	image := mcpFlags.String("image", "alpine:latest", "Docker container image or alias (agent, ubuntu, alpine)")
	network := mcpFlags.String("network", "bridge", "Network mode (bridge, none, host)")
	memStr := mcpFlags.String("memory", "", "Memory limit (e.g. '512m', '2g', '4gb'; default: unlimited)")
	cpus := mcpFlags.Float64("cpus", 0.0, "CPU core limit (e.g. 1.5, 2.0; default: unlimited)")
	useMock := mcpFlags.Bool("mock", false, "Run in mock sandbox mode (for testing without Docker)")
	_ = mcpFlags.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var sb sandbox.Sandbox
	cfg := sandbox.DefaultDockerConfig(*workspace)
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

	if *useMock {
		sb = sandbox.NewMockSandbox()
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

	server := mcp.NewServer(sb, cfg.WorkspacePath, os.Stdin, os.Stdout)
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] MCP server loop exited: %v\n", err)
	}
}

func runCheckpoint(args []string) {
	cpFlags := flag.NewFlagSet("checkpoint", flag.ExitOnError)
	workspace := cpFlags.String("workspace", ".", "Host directory to manage snapshots for")
	_ = cpFlags.Parse(args)

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

	if err := sm.CreateSnapshot(tag); err != nil {
		fmt.Fprintf(os.Stderr, "[vessel] Error creating checkpoint %s: %v\n", tag, err)
		os.Exit(1)
	}

	fmt.Printf("[vessel] Checkpoint '%s' created successfully for workspace: %s\n", tag, *workspace)
}

func runRollback(args []string) {
	rbFlags := flag.NewFlagSet("rollback", flag.ExitOnError)
	workspace := rbFlags.String("workspace", ".", "Host directory to rollback")
	_ = rbFlags.Parse(args)

	remaining := rbFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: rollback requires a checkpoint tag")
		fmt.Fprintln(os.Stderr, "Usage: vessel rollback <tag> [--workspace <dir>]")
		os.Exit(1)
	}

	tag := remaining[0]
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
	_ = stFlags.Parse(args)

	absPath, err := filepath.Abs(*workspace)
	if err != nil {
		absPath = *workspace
	}

	fmt.Printf("Vessel Engine Status (v%s)\n", Version)
	fmt.Printf("  Workspace:    %s\n", absPath)

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
}

func printUsage() {
	fmt.Printf("Vessel v%s — Local AI Agent Sandbox Engine\n\n", Version)
	fmt.Println("Usage:")
	fmt.Println("  vessel <command> [options] [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  exec        Execute a command inside the sandbox container")
	fmt.Println("              Flags: --workspace <dir>, --image <image/alias>, --network <mode>, --memory <limit>, --cpus <cores>")
	fmt.Println("  mcp         Start stdio Model Context Protocol server")
	fmt.Println("              Flags: --workspace <dir>, --image <image/alias>, --network <mode>, --memory <limit>, --cpus <cores>, --mock")
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
}
