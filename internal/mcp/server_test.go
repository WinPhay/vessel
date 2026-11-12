package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/WinPhay/vessel/internal/sandbox"
)

func TestMCPServerHandshakeAndTools(t *testing.T) {
	tempWorkspace, err := os.MkdirTemp("", "vessel-mcp-workspace-*")
	if err != nil {
		t.Fatalf("failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempWorkspace)

	mockSB := sandbox.NewMockSandbox()
	var inBuf, outBuf bytes.Buffer

	server := NewServer(mockSB, tempWorkspace, &inBuf, &outBuf)

	// Step 1: Send 'initialize'
	reqInit := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	// Step 2: Send 'tools/list'
	reqList := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	// Step 3: Send 'tools/call' for vessel_exec
	reqExec := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vessel_exec","arguments":{"command":"echo hello"}}}` + "\n"
	// Step 4: Send 'tools/call' for vessel_checkpoint
	reqCP := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"vessel_checkpoint","arguments":{"tag":"v1"}}}` + "\n"
	// Step 5: Send 'tools/call' for vessel_rollback
	reqRB := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"vessel_rollback","arguments":{"tag":"v1"}}}` + "\n"
	// Step 6: Send 'tools/call' for vessel_status
	reqStatus := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"vessel_status","arguments":{}}}` + "\n"
	// Step 7: Send 'tools/call' for vessel_write_file
	reqWrite := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"vessel_write_file","arguments":{"path":"hello.txt","content":"Hello Vessel MCP!"}}}` + "\n"
	// Step 8: Send 'tools/call' for vessel_read_file
	reqRead := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"vessel_read_file","arguments":{"path":"hello.txt"}}}` + "\n"
	// Step 9: Send 'tools/call' for vessel_list_dir
	reqListDir := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"vessel_list_dir","arguments":{}}}` + "\n"

	inBuf.WriteString(reqInit + reqList + reqExec + reqCP + reqRB + reqStatus + reqWrite + reqRead + reqListDir)

	ctx := context.Background()
	err = server.Serve(ctx)
	if err != nil {
		t.Fatalf("unexpected server error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 9 {
		t.Fatalf("expected 9 responses, got %d:\n%s", len(lines), outBuf.String())
	}

	// Verify initialize response
	var resp1 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &resp1); err != nil {
		t.Fatalf("failed to parse resp1: %v", err)
	}
	resMap, ok := resp1.Result.(map[string]any)
	if !ok || resMap["protocolVersion"] != ProtocolVersion {
		t.Errorf("expected protocolVersion %s, got: %v", ProtocolVersion, resMap["protocolVersion"])
	}

	// Verify tools/list response
	var resp2 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &resp2); err != nil {
		t.Fatalf("failed to parse resp2: %v", err)
	}
	toolsMap := resp2.Result.(map[string]any)
	toolsList := toolsMap["tools"].([]any)
	if len(toolsList) != 7 {
		t.Errorf("expected 7 registered tools, got %d", len(toolsList))
	}

	// Verify vessel_exec response
	var resp3 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[2]), &resp3); err != nil {
		t.Fatalf("failed to parse resp3: %v", err)
	}
	if resp3.Error != nil {
		t.Errorf("unexpected error on vessel_exec: %v", resp3.Error)
	}

	// Verify vessel_checkpoint response
	var resp4 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[3]), &resp4); err != nil {
		t.Fatalf("failed to parse resp4: %v", err)
	}
	if resp4.Error != nil {
		t.Errorf("unexpected error on vessel_checkpoint: %v", resp4.Error)
	}

	// Verify vessel_rollback response
	var resp5 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[4]), &resp5); err != nil {
		t.Fatalf("failed to parse resp5: %v", err)
	}
	if resp5.Error != nil {
		t.Errorf("unexpected error on vessel_rollback: %v", resp5.Error)
	}

	// Verify vessel_status response
	var resp6 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[5]), &resp6); err != nil {
		t.Fatalf("failed to parse resp6: %v", err)
	}
	if resp6.Error != nil {
		t.Errorf("unexpected error on vessel_status: %v", resp6.Error)
	}

	// Verify vessel_write_file response
	var resp7 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[6]), &resp7); err != nil {
		t.Fatalf("failed to parse resp7: %v", err)
	}
	if resp7.Error != nil {
		t.Errorf("unexpected error on vessel_write_file: %v", resp7.Error)
	}

	// Verify vessel_read_file response
	var resp8 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[7]), &resp8); err != nil {
		t.Fatalf("failed to parse resp8: %v", err)
	}
	if resp8.Error != nil {
		t.Errorf("unexpected error on vessel_read_file: %v", resp8.Error)
	}

	// Verify vessel_list_dir response
	var resp9 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[8]), &resp9); err != nil {
		t.Fatalf("failed to parse resp9: %v", err)
	}
	if resp9.Error != nil {
		t.Errorf("unexpected error on vessel_list_dir: %v", resp9.Error)
	}
}

func TestMCPServerUnknownMethod(t *testing.T) {
	mockSB := sandbox.NewMockSandbox()
	var inBuf, outBuf bytes.Buffer

	server := NewServer(mockSB, "/test/workspace", &inBuf, &outBuf)
	inBuf.WriteString(`{"jsonrpc":"2.0","id":99,"method":"unknown_foo","params":{}}` + "\n")

	err := server.Serve(context.Background())
	if err != nil {
		t.Fatalf("unexpected server error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("expected method not found error code -32601, got: %v", resp.Error)
	}
}
