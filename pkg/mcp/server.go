package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/carlpatchett/nullspace/pkg/indexer"
	"github.com/carlpatchett/nullspace/pkg/telemetry"
)

// MCP Server metadata and instructions.
const (
	ProtocolVersion    = "2024-11-05"
	ServerName         = "null-space"
	ServerVersion      = "1.0.0"
	ServerInstructions = "Lightning-fast negative search space pruner using in-memory Bloom filters. " +
		"START by calling prune_search_space(token) before reading files or running broad greps: " +
		"it mathematically proves where code DOES NOT exist and eliminates 80-99% of directories with zero false negatives. " +
		"Use check_token to check specific files/dirs, and reindex_workspace after modifying files."
)

// Server coordinates JSON-RPC 2.0 stdio communication and tool execution.
type Server struct {
	index     *indexer.WorkspaceIndex
	telemetry *telemetry.Telemetry
	mu        sync.Mutex
}

// NewServer initializes an MCP Server with the given index and telemetry.
func NewServer(idx *indexer.WorkspaceIndex, tel *telemetry.Telemetry) *Server {
	return &Server{
		index:     idx,
		telemetry: tel,
	}
}

// ToolsList returns the MCP tool definitions supported by null-space.
func (s *Server) ToolsList() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "prune_search_space",
			Description: "⚡ INSTANT NEGATIVE SEARCH SPACE PRUNING. Uses in-memory Bloom filters to definitively " +
				"prove where code DOES NOT exist. Call this BEFORE reading files or running full-text grep " +
				"to eliminate 80-99% of directories with zero false negatives. Returns candidate directories " +
				"(where the token may exist) and pruned directories (where the token definitively cannot exist).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token": map[string]any{
						"type":        "string",
						"description": "Identifier, variable name, function call, class name, or literal string to search for.",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Alias for token (identifier or literal to search for).",
					},
					"case_sensitive": map[string]any{
						"type":        "boolean",
						"description": "Whether to perform a case-sensitive token match (default false).",
						"default":     false,
					},
				},
				"required": []string{"token"},
			},
		},
		{
			Name: "check_token",
			Description: "Check if a variable, function, class, or string literal could exist in a specific " +
				"file or directory subtree with mathematical certainty (zero false negatives).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token": map[string]any{
						"type":        "string",
						"description": "Token to check.",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Alias for token.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Relative file path or directory path to check (default root: '.').",
						"default":     ".",
					},
				},
				"required": []string{"token"},
			},
		},
		{
			Name: "reindex_workspace",
			Description: "Rebuild the Bloom filter index from disk. Call after creating, editing, " +
				"or deleting files so future prune queries reflect the current workspace state.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name: "get_filter_stats",
			Description: "Get Bloom filter index statistics, total indexed files, memory footprint, " +
				"and average false-positive probability.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// Serve runs the JSON-RPC read-eval-respond loop over reader and writer.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow large messages (e.g. 16 MB)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	encoder := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(encoder, nil, ErrCodeParseError, "Parse error: "+err.Error())
			continue
		}

		s.handleRequest(&req, encoder)
	}

	return scanner.Err()
}

func (s *Server) handleRequest(req *Request, encoder *json.Encoder) {
	// Notifications (e.g. notifications/initialized) have no ID and require no response
	if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
		return
	}

	switch req.Method {
	case "initialize":
		res := InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: ServerCapabilities{
				Tools: &ToolCapabilities{ListChanged: false},
			},
			ServerInfo: Implementation{
				Name:    ServerName,
				Version: ServerVersion,
			},
			Instructions: ServerInstructions,
		}
		s.sendResult(encoder, req.ID, res)

	case "ping":
		s.sendResult(encoder, req.ID, map[string]any{})

	case "tools/list":
		s.sendResult(encoder, req.ID, ListToolsResult{
			Tools: s.ToolsList(),
		})

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.sendError(encoder, req.ID, ErrCodeInvalidParams, "Invalid params: "+err.Error())
			return
		}
		toolRes := s.executeTool(params.Name, params.Arguments)
		s.sendResult(encoder, req.ID, toolRes)

	default:
		s.sendError(encoder, req.ID, ErrCodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) executeTool(name string, args map[string]any) CallToolResult {
	if args == nil {
		args = make(map[string]any)
	}

	switch name {
	case "prune_search_space":
		token := ""
		if t, ok := args["token"].(string); ok && t != "" {
			token = t
		} else if q, ok := args["query"].(string); ok && q != "" {
			token = q
		}

		if token == "" {
			return CallToolResult{
				Content: []TextContent{{
					Type: "text",
					Text: "Error: missing required argument 'token' (or 'query')",
				}},
				IsError: true,
			}
		}

		caseSensitive := false
		if cs, ok := args["case_sensitive"].(bool); ok {
			caseSensitive = cs
		} else if cs, ok := args["caseSensitive"].(bool); ok {
			caseSensitive = cs
		}

		res, err := s.index.PruneSearchSpace(token, caseSensitive)
		if err != nil {
			return CallToolResult{
				Content: []TextContent{{Type: "text", Text: "Prune error: " + err.Error()}},
				IsError: true,
			}
		}

		resJSON, _ := json.MarshalIndent(res, "", "  ")
		structured := make(map[string]any)
		_ = json.Unmarshal(resJSON, &structured)

		return CallToolResult{
			Content: []TextContent{{
				Type: "text",
				Text: string(resJSON),
			}},
			StructuredContent: structured,
		}

	case "check_token":
		token := ""
		if t, ok := args["token"].(string); ok && t != "" {
			token = t
		} else if q, ok := args["query"].(string); ok && q != "" {
			token = q
		}

		if token == "" {
			return CallToolResult{
				Content: []TextContent{{
					Type: "text",
					Text: "Error: missing required argument 'token' (or 'query')",
				}},
				IsError: true,
			}
		}

		targetPath := "."
		if p, ok := args["path"].(string); ok && p != "" {
			targetPath = p
		}

		res, err := s.index.CheckToken(token, targetPath)
		if err != nil {
			return CallToolResult{
				Content: []TextContent{{Type: "text", Text: "Check error: " + err.Error()}},
				IsError: true,
			}
		}

		resJSON, _ := json.MarshalIndent(res, "", "  ")
		structured := make(map[string]any)
		_ = json.Unmarshal(resJSON, &structured)

		return CallToolResult{
			Content: []TextContent{{
				Type: "text",
				Text: string(resJSON),
			}},
			StructuredContent: structured,
		}

	case "reindex_workspace":
		res, err := s.index.Build()
		if err != nil {
			return CallToolResult{
				Content: []TextContent{{Type: "text", Text: "Reindex error: " + err.Error()}},
				IsError: true,
			}
		}

		resJSON, _ := json.MarshalIndent(res, "", "  ")
		structured := make(map[string]any)
		_ = json.Unmarshal(resJSON, &structured)

		return CallToolResult{
			Content: []TextContent{{
				Type: "text",
				Text: string(resJSON),
			}},
			StructuredContent: structured,
		}

	case "get_filter_stats":
		res, err := s.index.Stats()
		if err != nil {
			return CallToolResult{
				Content: []TextContent{{Type: "text", Text: "Stats error: " + err.Error()}},
				IsError: true,
			}
		}

		resJSON, _ := json.MarshalIndent(res, "", "  ")
		structured := make(map[string]any)
		_ = json.Unmarshal(resJSON, &structured)

		return CallToolResult{
			Content: []TextContent{{
				Type: "text",
				Text: string(resJSON),
			}},
			StructuredContent: structured,
		}

	default:
		return CallToolResult{
			Content: []TextContent{{
				Type: "text",
				Text: fmt.Sprintf("Unknown tool: %s", name),
			}},
			IsError: true,
		}
	}
}

func (s *Server) sendResult(encoder *json.Encoder, id, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = encoder.Encode(Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (s *Server) sendError(encoder *json.Encoder, id any, code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = encoder.Encode(Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	})
}

// RunStdio starts the MCP server over standard input and output.
func RunStdio(idx *indexer.WorkspaceIndex, tel *telemetry.Telemetry) error {
	srv := NewServer(idx, tel)
	return srv.Serve(os.Stdin, os.Stdout)
}
