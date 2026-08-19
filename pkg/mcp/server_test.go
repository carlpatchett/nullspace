package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"null-space/pkg/indexer"
	"null-space/pkg/telemetry"
)

func createTestWorkspaceForMCP(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"services/payments/retry.py": `
class ExponentialBackoffRetry:
    def __init__(self, max_retries: int = 5):
        self.max_retries = max_retries
`,
		"services/auth/jwt.go": `
package auth
type TokenManager struct {
    SecretKey []byte
}
`,
		"tools/notifier.cs": `
namespace Tools;
public class NotifierService {}
`,
	}

	for relPath, content := range files {
		fullPath := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
	}
	return root
}

func TestMCPServerLifecycle(t *testing.T) {
	root := createTestWorkspaceForMCP(t)
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	tel := telemetry.New(telemetry.Config{TracePath: tracePath})
	defer tel.Close()

	cfg := indexer.DefaultConfig(root)
	cfg.Telemetry = tel
	idx, err := indexer.New(cfg)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	srv := NewServer(idx, tel)

	// Step 1: Initialize
	initReq := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	initReqBytes, _ := json.Marshal(initReq)
	initReqBytes = append(initReqBytes, '\n')

	inBuf := bytes.NewBuffer(initReqBytes)
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(inBuf, outBuf); err != nil {
		t.Fatalf("Serve error on init: %v", err)
	}

	var initResp Response
	if err := json.Unmarshal(outBuf.Bytes(), &initResp); err != nil {
		t.Fatalf("failed to parse init response: %v, raw: %s", err, outBuf.String())
	}
	if initResp.Error != nil {
		t.Fatalf("init returned error: %v", initResp.Error)
	}

	// Step 2: List Tools
	listReq := Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	listReqBytes, _ := json.Marshal(listReq)
	listReqBytes = append(listReqBytes, '\n')

	inBuf = bytes.NewBuffer(listReqBytes)
	outBuf.Reset()

	if err := srv.Serve(inBuf, outBuf); err != nil {
		t.Fatalf("Serve error on tools/list: %v", err)
	}

	var listResp Response
	if err := json.Unmarshal(outBuf.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to parse tools/list response: %v", err)
	}
	resMap, ok := listResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result from tools/list")
	}
	toolsList, ok := resMap["tools"].([]any)
	if !ok || len(toolsList) != 4 {
		t.Fatalf("expected 4 tools, got %v", toolsList)
	}

	// Step 3: Call prune_search_space
	callParams, _ := json.Marshal(CallToolParams{
		Name: "prune_search_space",
		Arguments: map[string]any{
			"token": "ExponentialBackoffRetry",
		},
	})
	callReq := Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  callParams,
	}
	callReqBytes, _ := json.Marshal(callReq)
	callReqBytes = append(callReqBytes, '\n')

	inBuf = bytes.NewBuffer(callReqBytes)
	outBuf.Reset()

	if err := srv.Serve(inBuf, outBuf); err != nil {
		t.Fatalf("Serve error on tools/call: %v", err)
	}

	var callResp Response
	if err := json.Unmarshal(outBuf.Bytes(), &callResp); err != nil {
		t.Fatalf("failed to parse tools/call response: %v", err)
	}
	callResMap, ok := callResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result from tools/call")
	}
	if isErr, ok := callResMap["isError"].(bool); ok && isErr {
		t.Fatalf("tool call returned error result: %v", callResMap)
	}

	// Step 4: Call check_token
	checkParams, _ := json.Marshal(CallToolParams{
		Name: "check_token",
		Arguments: map[string]any{
			"token": "TokenManager",
			"path":  "services/auth/jwt.go",
		},
	})
	checkReq := Request{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params:  checkParams,
	}
	checkReqBytes, _ := json.Marshal(checkReq)
	checkReqBytes = append(checkReqBytes, '\n')

	inBuf = bytes.NewBuffer(checkReqBytes)
	outBuf.Reset()

	if err := srv.Serve(inBuf, outBuf); err != nil {
		t.Fatalf("Serve error on check_token: %v", err)
	}

	// Step 5: Call get_filter_stats
	statsParams, _ := json.Marshal(CallToolParams{
		Name:      "get_filter_stats",
		Arguments: map[string]any{},
	})
	statsReq := Request{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params:  statsParams,
	}
	statsReqBytes, _ := json.Marshal(statsReq)
	statsReqBytes = append(statsReqBytes, '\n')

	inBuf = bytes.NewBuffer(statsReqBytes)
	outBuf.Reset()

	if err := srv.Serve(inBuf, outBuf); err != nil {
		t.Fatalf("Serve error on get_filter_stats: %v", err)
	}
}
