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
	maskSecrets := execFlags.Bool("mask-secrets", true, "Enable automatic secret redaction and masking")
	_ = parseInterspersed(execFlags, args)

	remaining := execFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: vessel exec requires a command to run inside the sandbox")
		fmt.Fprintln(os.Stderr, "Usage: vessel exec [--workspace <dir>] [--image <img>] [--network <mode>] [--memory <limit>] [--cpus <cores>] [--runtime <runtime>] [--secret <KEY=VAL>] [--mask-secrets] \"<command>\"")
		os.Exit(1)
	}

	cmdToRun := remaining[0]
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !*maskSecrets {
		tokenfilter.SetSecretMasking(false)
	}

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
	maskSecrets := mcpFlags.Bool("mask-secrets", true, "Enable automatic secret redaction and masking")
	useMock := mcpFlags.Bool("mock", false, "Run in mock sandbox mode (for testing without Docker)")
	_ = parseInterspersed(mcpFlags, args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !*maskSecrets {
		tokenfilter.SetSecretMasking(false)
	}

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
	_ = parseInterspersed(stFlags, args)

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

func printUsage() {
	fmt.Printf("Vessel v%s — Local AI Agent Sandbox Engine\n\n", strings.TrimPrefix(Version, "v"))
	fmt.Println("Usage:")
	fmt.Println("  vessel <command> [options] [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  exec        Execute a command inside the sandbox container")
	fmt.Println("              Flags: --workspace <dir>, --image <image/alias>, --network <mode>, --memory <limit>, --cpus <cores>, --runtime <runtime>, --secret <KEY=VAL>, --mask-secrets")
	fmt.Println("  mcp         Start stdio Model Context Protocol server")
	fmt.Println("              Flags: --workspace <dir>, --image <image/alias>, --network <mode>, --memory <limit>, --cpus <cores>, --runtime <runtime>, --secret <KEY=VAL>, --mask-secrets, --mock")
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
}
