package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	// Step 10: Send 'tools/call' for vessel_browse_url
	reqBrowse := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"vessel_browse_url","arguments":{"url":"https://example.com"}}}` + "\n"
	// Step 11: Send 'tools/call' for vessel_screenshot
	reqScreen := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"vessel_screenshot","arguments":{"url":"https://example.com","output_path":"screen.png"}}}` + "\n"
	// Step 12: Send 'tools/call' for vessel_extract_page
	reqExtract := `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"vessel_extract_page","arguments":{"url":"https://example.com"}}}` + "\n"

	inBuf.WriteString(reqInit + reqList + reqExec + reqCP + reqRB + reqStatus + reqWrite + reqRead + reqListDir + reqBrowse + reqScreen + reqExtract)

	ctx := context.Background()
	err = server.Serve(ctx)
	if err != nil {
		t.Fatalf("unexpected server error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 12 {
		t.Fatalf("expected 12 responses, got %d:\n%s", len(lines), outBuf.String())
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
	if len(toolsList) != 16 {
		t.Errorf("expected 16 registered tools, got %d", len(toolsList))
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

	// Verify vessel_browse_url response
	var resp10 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[9]), &resp10); err != nil {
		t.Fatalf("failed to parse resp10: %v", err)
	}
	if resp10.Error != nil {
		t.Errorf("unexpected error on vessel_browse_url: %v", resp10.Error)
	}

	// Verify vessel_screenshot response
	var resp11 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[10]), &resp11); err != nil {
		t.Fatalf("failed to parse resp11: %v", err)
	}
	if resp11.Error != nil {
		t.Errorf("unexpected error on vessel_screenshot: %v", resp11.Error)
	}

	// Verify vessel_extract_page response
	var resp12 JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[11]), &resp12); err != nil {
		t.Fatalf("failed to parse resp12: %v", err)
	}
	if resp12.Error != nil {
		t.Errorf("unexpected error on vessel_extract_page: %v", resp12.Error)
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

func TestMCPServerProcessTools(t *testing.T) {
	mockSB := sandbox.NewMockSandbox()
	var inBuf, outBuf bytes.Buffer

	server := NewServer(mockSB, "/test/workspace", &inBuf, &outBuf)

	// 1. Start process
	reqStart := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vessel_start_process","arguments":{"command":"python3 -i","pty":true,"rows":24,"cols":80}}}` + "\n"
	inBuf.WriteString(reqStart)

	ctx := context.Background()
	_ = server.Serve(ctx)

	var respStart JSONRPCResponse
	line, err := outBuf.ReadString('\n')
	if err != nil {
		t.Fatalf("failed reading start response: %v", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &respStart); err != nil {
		t.Fatalf("failed unmarshaling start response: %v", err)
	}
	if respStart.Error != nil {
		t.Fatalf("vessel_start_process returned error: %v", respStart.Error)
	}

	resultMap := respStart.Result.(map[string]any)
	contentList := resultMap["content"].([]any)
	firstContent := contentList[0].(map[string]any)
	textJSON := firstContent["text"].(string)

	var info sandbox.ProcessInfo
	if err := json.Unmarshal([]byte(textJSON), &info); err != nil {
		t.Fatalf("failed parsing process info JSON: %v", err)
	}
	if info.ID == "" || !info.Running {
		t.Fatalf("unexpected process info: %+v", info)
	}

	// 2. Write process input
	outBuf.Reset()
	inBuf.Reset()
	reqWrite := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vessel_process_write","arguments":{"process_id":"` + info.ID + `","input":"print(123)\n"}}}` + "\n"
	inBuf.WriteString(reqWrite)
	_ = server.Serve(ctx)

	var respWrite JSONRPCResponse
	line, _ = outBuf.ReadString('\n')
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &respWrite); err != nil || respWrite.Error != nil {
		t.Fatalf("vessel_process_write failed: %v", respWrite.Error)
	}

	// 3. Read process output
	outBuf.Reset()
	inBuf.Reset()
	reqOutput := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vessel_process_output","arguments":{"process_id":"` + info.ID + `"深化"}}}`
	// fix JSON string
	reqOutput = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vessel_process_output","arguments":{"process_id":"` + info.ID + `"}}}` + "\n"
	inBuf.WriteString(reqOutput)
	_ = server.Serve(ctx)

	var respOutput JSONRPCResponse
	line, _ = outBuf.ReadString('\n')
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &respOutput); err != nil || respOutput.Error != nil {
		t.Fatalf("vessel_process_output failed: %v", respOutput.Error)
	}

	// 4. Kill process
	outBuf.Reset()
	inBuf.Reset()
	reqKill := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"vessel_kill_process","arguments":{"process_id":"` + info.ID + `","signal":"SIGTERM"}}}` + "\n"
	inBuf.WriteString(reqKill)
	_ = server.Serve(ctx)

	var respKill JSONRPCResponse
	line, _ = outBuf.ReadString('\n')
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &respKill); err != nil || respKill.Error != nil {
		t.Fatalf("vessel_kill_process failed: %v", respKill.Error)
	}
}

func TestMCPServerSearchAndPatchTools(t *testing.T) {
	tempWorkspace, err := os.MkdirTemp("", "vessel-mcp-search-patch-*")
	if err != nil {
		t.Fatalf("failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempWorkspace)

	samplePath := filepath.Join(tempWorkspace, "code.txt")
	_ = os.WriteFile(samplePath, []byte("alpha\nbeta\ngamma\n"), 0644)

	mockSB := sandbox.NewMockSandbox()
	var inBuf, outBuf bytes.Buffer
	server := NewServer(mockSB, tempWorkspace, &inBuf, &outBuf)
	ctx := context.Background()

	// 1. Test vessel_grep
	reqGrep := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vessel_grep","arguments":{"pattern":"beta"}}}` + "\n"
	inBuf.WriteString(reqGrep)
	_ = server.Serve(ctx)

	var respGrep JSONRPCResponse
	line, err := outBuf.ReadString('\n')
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(line)), &respGrep) != nil || respGrep.Error != nil {
		t.Fatalf("vessel_grep failed: line=%s, err=%v", line, respGrep.Error)
	}

	resultMap := respGrep.Result.(map[string]any)
	contentList := resultMap["content"].([]any)
	firstContent := contentList[0].(map[string]any)
	textJSON := firstContent["text"].(string)

	var grepResult sandbox.GrepResult
	_ = json.Unmarshal([]byte(textJSON), &grepResult)
	if grepResult.TotalMatches != 1 || len(grepResult.Matches) != 1 {
		t.Errorf("expected 1 match for beta, got %d", grepResult.TotalMatches)
	}

	// 2. Test vessel_apply_patch
	patch := `--- a/code.txt
+++ b/code.txt
@@ -1,3 +1,3 @@
 alpha
-beta
+beta-updated
 gamma
`
	patchJSON, _ := json.Marshal(patch)
	outBuf.Reset()
	inBuf.Reset()
	reqPatch := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vessel_apply_patch","arguments":{"patch":` + string(patchJSON) + `,"dry_run":false}}}` + "\n"
	inBuf.WriteString(reqPatch)
	_ = server.Serve(ctx)

	var respPatch JSONRPCResponse
	line, err = outBuf.ReadString('\n')
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(line)), &respPatch) != nil || respPatch.Error != nil {
		t.Fatalf("vessel_apply_patch failed: line=%s, err=%v", line, respPatch.Error)
	}

	updatedData, _ := os.ReadFile(samplePath)
	if !strings.Contains(string(updatedData), "beta-updated") {
		t.Errorf("expected file to be patched, got: %s", string(updatedData))
	}
}
