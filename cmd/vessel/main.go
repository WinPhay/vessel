package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/WinPhay/vessel/internal/mcp"
	"github.com/WinPhay/vessel/internal/sandbox"
)

const Version = "0.1.0-alpha"

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
	image := execFlags.String("image", "alpine:latest", "Docker container image to use")
	_ = execFlags.Parse(args)

	remaining := execFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(os.Stderr, "Error: vessel exec requires a command to run inside the sandbox")
		fmt.Fprintln(os.Stderr, "Usage: vessel exec [--workspace <dir>] [--image <img>] \"<command>\"")
		os.Exit(1)
	}

	cmdToRun := remaining[0]
	ctx := context.Background()

	cfg := sandbox.DefaultDockerConfig(*workspace)
	if *image != "" {
		cfg.ImageName = *image
	}

	fmt.Printf("[vessel] Initializing sandbox container (image: %s, workspace: %s)...\n", cfg.ImageName, cfg.WorkspacePath)
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
	image := mcpFlags.String("image", "alpine:latest", "Docker container image to use")
	useMock := mcpFlags.Bool("mock", false, "Run in mock sandbox mode (for testing without Docker)")
	_ = mcpFlags.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var sb sandbox.Sandbox
	cfg := sandbox.DefaultDockerConfig(*workspace)
	if *image != "" {
		cfg.ImageName = *image
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

func printUsage() {
	fmt.Printf("Vessel v%s — Local AI Agent Sandbox Engine\n\n", Version)
	fmt.Println("Usage:")
	fmt.Println("  vessel <command> [options] [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  exec      Execute a command inside the sandbox container")
	fmt.Println("            Flags: --workspace <dir>, --image <image>")
	fmt.Println("  mcp       Start stdio Model Context Protocol server")
	fmt.Println("            Flags: --workspace <dir>, --image <image>, --mock")
	fmt.Println("  version   Display version information")
	fmt.Println("  help      Show this help text")
}
