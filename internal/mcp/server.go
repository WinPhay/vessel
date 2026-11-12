package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/WinPhay/vessel/internal/sandbox"
)

const (
	ProtocolVersion = "2024-11-05"
	ServerName      = "vessel"
	ServerVersion   = "0.1.0-alpha"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 message.
type JSONRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *JSONRPCError    `json:"error,omitempty"`
}

// JSONRPCError represents a standard JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool describes an MCP callable tool schema.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// ContentItem represents a single payload inside a tool call result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallToolResult represents the MCP response to a tool execution.
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Server manages the stdio Model Context Protocol connection.
type Server struct {
	sb        sandbox.Sandbox
	workspace string
	in        io.Reader
	out       io.Writer
	mu        sync.Mutex
}

// NewServer initializes an MCP server instance.
func NewServer(sb sandbox.Sandbox, workspace string, in io.Reader, out io.Writer) *Server {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return &Server{
		sb:        sb,
		workspace: workspace,
		in:        in,
		out:       out,
	}
}

// Serve starts the JSON-RPC event loop over the configured stdio streams.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	// Support messages up to 4MB for tool payloads
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error", err.Error())
			continue
		}

		resp := s.handleRequest(ctx, &req)
		if resp != nil {
			s.sendResponse(resp)
		}
	}

	return scanner.Err()
}

func (s *Server) handleRequest(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	// Notifications (no ID) do not expect a response
	if req.ID == nil && req.Method == "notifications/initialized" {
		return nil
	}

	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    ServerName,
					"version": ServerVersion,
				},
			},
		}

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.getToolsList(),
			},
		}

	case "tools/call":
		return s.handleToolCall(ctx, req)

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

func (s *Server) getToolsList() []Tool {
	return []Tool{
		{
			Name:        "vessel_exec",
			Description: "Executes a shell command inside the isolated Vessel sandbox container.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The command string to execute in the container.",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds (default: 120).",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "vessel_checkpoint",
			Description: "Creates an immutable snapshot checkpoint of the container filesystem state.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tag": map[string]any{
						"type":        "string",
						"description": "Unique alphanumeric tag for the checkpoint (e.g., 'pre-refactor').",
					},
				},
				"required": []string{"tag"},
			},
		},
		{
			Name:        "vessel_rollback",
			Description: "Restores the container filesystem state to a previously saved checkpoint tag.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tag": map[string]any{
						"type":        "string",
						"description": "The checkpoint tag to restore.",
					},
				},
				"required": []string{"tag"},
			},
		},
		{
			Name:        "vessel_status",
			Description: "Returns the current status, active workspace, and health of the Vessel sandbox.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "vessel_read_file",
			Description: "Reads file content from the workspace sandbox with optional line pagination.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to workspace root.",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "Optional starting line number (1-indexed).",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "Optional ending line number (1-indexed).",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "vessel_write_file",
			Description: "Atomically writes content to a file in the workspace sandbox, automatically creating parent directories.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to workspace root.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Text content to write to the file.",
					},
					"overwrite": map[string]any{
						"type":        "boolean",
						"description": "Whether to overwrite existing files (default: true).",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "vessel_list_dir",
			Description: "Lists directory contents in the workspace sandbox, skipping ignored paths (.vessel, .git).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory path relative to workspace root (default: '.').",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Whether to list subdirectories recursively (default: false).",
					},
					"max_depth": map[string]any{
						"type":        "integer",
						"description": "Maximum directory depth when recursive (default: 2).",
					},
				},
			},
		},
		{
			Name:        "vessel_browse_url",
			Description: "Visits a web URL inside the isolated sandbox container, rendering dynamic client-side DOM and returning sanitized Markdown.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to visit (must be http, https, or file protocol).",
					},
					"wait_seconds": map[string]any{
						"type":        "integer",
						"description": "Seconds to wait for page to render (default: 2).",
					},
					"include_links": map[string]any{
						"type":        "boolean",
						"description": "Whether to convert anchors to markdown links (default: true).",
					},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "vessel_screenshot",
			Description: "Captures a PNG screenshot of a web page into the workspace sandbox directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The web URL to screenshot.",
					},
					"output_path": map[string]any{
						"type":        "string",
						"description": "Workspace relative path for the output PNG (default: '.vessel/screenshots/page-<timestamp>.png').",
					},
					"full_page": map[string]any{
						"type":        "boolean",
						"description": "Whether to capture full scrollable page (default: false).",
					},
					"width": map[string]any{
						"type":        "integer",
						"description": "Viewport width in pixels (default: 1280).",
					},
					"height": map[string]any{
						"type":        "integer",
						"description": "Viewport height in pixels (default: 800).",
					},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "vessel_extract_page",
			Description: "Extracts structured interactive affordances (headings, links, input fields, buttons) from a web page as JSON.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The web URL to extract structure from.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (s *Server) handleToolCall(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	if err := json.Unmarshal(req.Params, &callParams); err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32602,
				Message: "Invalid params for tools/call",
				Data:    err.Error(),
			},
		}
	}

	switch callParams.Name {
	case "vessel_exec":
		var args struct {
			Command        string `json:"command"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("invalid arguments for vessel_exec: %v", err))
		}
		if args.Command == "" {
			return s.toolErrorResponse(req.ID, "command parameter is required")
		}

		timeout := 120 * time.Second
		if args.TimeoutSeconds > 0 {
			timeout = time.Duration(args.TimeoutSeconds) * time.Second
		}

		execCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		res, err := s.sb.Exec(execCtx, args.Command)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("execution failed: %v", err))
		}

		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_checkpoint":
		var args struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil || args.Tag == "" {
			return s.toolErrorResponse(req.ID, "tag parameter is required for vessel_checkpoint")
		}

		if err := s.sb.Checkpoint(ctx, args.Tag); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("checkpoint failed: %v", err))
		}
		return s.toolSuccessResponse(req.ID, fmt.Sprintf("Checkpoint '%s' created successfully.", args.Tag))

	case "vessel_rollback":
		var args struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil || args.Tag == "" {
			return s.toolErrorResponse(req.ID, "tag parameter is required for vessel_rollback")
		}

		if err := s.sb.Rollback(ctx, args.Tag); err != nil {
			if errors.Is(err, sandbox.ErrCheckpointNotFound) {
				return s.toolErrorResponse(req.ID, fmt.Sprintf("checkpoint tag '%s' does not exist", args.Tag))
			}
			return s.toolErrorResponse(req.ID, fmt.Sprintf("rollback failed: %v", err))
		}
		return s.toolSuccessResponse(req.ID, fmt.Sprintf("Container state successfully rolled back to checkpoint '%s'.", args.Tag))

	case "vessel_status":
		statusData := map[string]any{
			"status":    "active",
			"workspace": s.workspace,
			"version":   ServerVersion,
		}
		payload, _ := json.MarshalIndent(statusData, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_read_file":
		var args struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("invalid arguments for vessel_read_file: %v", err))
		}
		if args.Path == "" {
			return s.toolErrorResponse(req.ID, "path parameter is required")
		}

		res, err := sandbox.ReadFile(s.workspace, args.Path, args.StartLine, args.EndLine)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("read failed: %v", err))
		}
		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_write_file":
		var args struct {
			Path      string `json:"path"`
			Content   string `json:"content"`
			Overwrite *bool  `json:"overwrite"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("invalid arguments for vessel_write_file: %v", err))
		}
		if args.Path == "" {
			return s.toolErrorResponse(req.ID, "path parameter is required")
		}
		overwrite := true
		if args.Overwrite != nil {
			overwrite = *args.Overwrite
		}

		res, err := sandbox.WriteFile(s.workspace, args.Path, args.Content, overwrite)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("write failed: %v", err))
		}
		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_list_dir":
		var args struct {
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
			MaxDepth  int    `json:"max_depth"`
		}
		_ = json.Unmarshal(callParams.Arguments, &args)
		dirPath := "."
		if args.Path != "" {
			dirPath = args.Path
		}

		res, err := sandbox.ListDir(s.workspace, dirPath, args.Recursive, args.MaxDepth)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("list failed: %v", err))
		}
		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_browse_url":
		var args struct {
			URL          string `json:"url"`
			WaitSeconds  int    `json:"wait_seconds"`
			IncludeLinks *bool  `json:"include_links"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("invalid arguments for vessel_browse_url: %v", err))
		}
		if args.URL == "" {
			return s.toolErrorResponse(req.ID, "url parameter is required")
		}
		includeLinks := true
		if args.IncludeLinks != nil {
			includeLinks = *args.IncludeLinks
		}

		res, err := sandbox.BrowseURL(ctx, s.sb, args.URL, args.WaitSeconds, includeLinks)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("browse failed: %v", err))
		}
		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_screenshot":
		var args struct {
			URL        string `json:"url"`
			OutputPath string `json:"output_path"`
			FullPage   bool   `json:"full_page"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("invalid arguments for vessel_screenshot: %v", err))
		}
		if args.URL == "" {
			return s.toolErrorResponse(req.ID, "url parameter is required")
		}

		res, err := sandbox.TakeScreenshot(ctx, s.sb, s.workspace, args.URL, args.OutputPath, args.FullPage, args.Width, args.Height)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("screenshot failed: %v", err))
		}
		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	case "vessel_extract_page":
		var args struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("invalid arguments for vessel_extract_page: %v", err))
		}
		if args.URL == "" {
			return s.toolErrorResponse(req.ID, "url parameter is required")
		}

		res, err := sandbox.ExtractPage(ctx, s.sb, args.URL)
		if err != nil {
			return s.toolErrorResponse(req.ID, fmt.Sprintf("extract failed: %v", err))
		}
		payload, _ := json.MarshalIndent(res, "", "  ")
		return s.toolSuccessResponse(req.ID, string(payload))

	default:
		return s.toolErrorResponse(req.ID, fmt.Sprintf("unknown tool: %s", callParams.Name))
	}
}

func (s *Server) toolSuccessResponse(id *json.RawMessage, text string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: CallToolResult{
			Content: []ContentItem{
				{Type: "text", Text: text},
			},
			IsError: false,
		},
	}
}

func (s *Server) toolErrorResponse(id *json.RawMessage, errMsg string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: CallToolResult{
			Content: []ContentItem{
				{Type: "text", Text: errMsg},
			},
			IsError: true,
		},
	}
}

func (s *Server) sendResponse(resp *JSONRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(resp)
	if err == nil {
		_, _ = s.out.Write(append(data, '\n'))
	}
}

func (s *Server) sendError(id *json.RawMessage, code int, message string, data any) {
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	s.sendResponse(resp)
}
