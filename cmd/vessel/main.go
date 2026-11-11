package main

import (
	"fmt"
	"os"
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
		fmt.Println("Starting Vessel MCP stdio server... (Press Ctrl+C to stop)")
		// Phase 1 MCP server loop entrypoint
	case "exec":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Error: vessel exec requires a command argument")
			os.Exit(1)
		}
		cmdToRun := os.Args[2]
		fmt.Printf("[vessel] Executing in sandbox: %s\n", cmdToRun)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("Vessel v%s — Local AI Agent Sandbox Engine\n\n", Version)
	fmt.Println("Usage:")
	fmt.Println("  vessel <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  mcp       Start stdio Model Context Protocol server")
	fmt.Println("  exec      Execute a command inside the sandbox container")
	fmt.Println("  version   Display version information")
	fmt.Println("  help      Show this help text")
}
