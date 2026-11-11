package tests

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

func TestLiveMCPServerLifecycle(t *testing.T) {
	binaryPath, err := filepath.Abs("../bin/vessel")
	if err != nil {
		t.Fatalf("failed to resolve binary path: %v", err)
	}

	cmd := exec.Command(binaryPath, "mcp", "--mock", "--workspace", "..")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to open stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to open stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start vessel binary: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	sendAndReceive := func(id int, method string, params any) *rpcResponse {
		req := rpcRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  method,
			Params:  params,
		}
		data, _ := json.Marshal(req)
		_, _ = stdin.Write(append(data, '\n'))

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("failed to read from server: %v", err)
		}

		var resp rpcResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
			t.Fatalf("failed to parse response %q: %v", line, err)
		}
		return &resp
	}

	// 1. Handshake
	r1 := sendAndReceive(1, "initialize", map[string]any{})
	if !strings.Contains(string(r1.Result), "2024-11-05") {
		t.Errorf("unexpected initialize result: %s", string(r1.Result))
	}

	// 2. Tool list
	r2 := sendAndReceive(2, "tools/list", map[string]any{})
	if !strings.Contains(string(r2.Result), "vessel_exec") {
		t.Errorf("missing vessel_exec in tools/list: %s", string(r2.Result))
	}

	// 3. Tool call: exec
	r3 := sendAndReceive(3, "tools/call", map[string]any{
		"name": "vessel_exec",
		"arguments": map[string]any{
			"command": "echo 'Testing live MCP connection'",
		},
	})
	if r3.Error != nil || !strings.Contains(string(r3.Result), "exit_code") {
		t.Errorf("unexpected vessel_exec error: %v", r3.Error)
	}

	// 4. Tool call: checkpoint
	r4 := sendAndReceive(4, "tools/call", map[string]any{
		"name": "vessel_checkpoint",
		"arguments": map[string]any{
			"tag": "e2e-checkpoint-1",
		},
	})
	if r4.Error != nil {
		t.Errorf("unexpected vessel_checkpoint error: %v", r4.Error)
	}

	// 5. Tool call: rollback
	r5 := sendAndReceive(5, "tools/call", map[string]any{
		"name": "vessel_rollback",
		"arguments": map[string]any{
			"tag": "e2e-checkpoint-1",
		},
	})
	if r5.Error != nil {
		t.Errorf("unexpected vessel_rollback error: %v", r5.Error)
	}
}
