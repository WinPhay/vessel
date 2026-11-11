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
