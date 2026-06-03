package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// runOneRequest sends one JSON-RPC request line into Server.Serve and reads
// back any responses until EOF. Used by unit tests to exercise the protocol
// without a real subprocess.
func runOneRequest(t *testing.T, s *Server, req map[string]any) []map[string]any {
	t.Helper()
	in, out := bytes.Buffer{}, bytes.Buffer{}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	in.Write(data)
	in.WriteByte('\n')
	// Server reads until EOF; our buffer already has all the input.
	if err := s.Serve(context.Background(), &in, &out); err != nil && err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}
	// Decode possibly multiple responses (one per line).
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("response is not JSON: %v\nraw: %q", err, line)
		}
		responses = append(responses, r)
	}
	return responses
}

func TestInitialize(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
			"capabilities":    map[string]any{},
		},
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	r := resps[0]
	if r["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", r["jsonrpc"])
	}
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %v", r["result"])
	}
	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %v", res["protocolVersion"], ProtocolVersion)
	}
	info, ok := res["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo missing or wrong type")
	}
	if info["name"] != ServerName {
		t.Errorf("serverInfo.name = %v, want %v", info["name"], ServerName)
	}
}

func TestToolsList(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	res, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not an object")
	}
	tools, ok := res["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not a list")
	}
	if len(tools) < 10 {
		t.Errorf("expected >=10 tools, got %d", len(tools))
	}
	// Every tool must have name, description, inputSchema, and inputSchema must be a JSON Schema object.
	names := map[string]bool{}
	for i, raw := range tools {
		tl, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tools[%d] is not an object", i)
		}
		name, _ := tl["name"].(string)
		if name == "" {
			t.Errorf("tools[%d].name is empty", i)
		}
		if names[name] {
			t.Errorf("duplicate tool name: %s", name)
		}
		names[name] = true
		if desc, _ := tl["description"].(string); desc == "" {
			t.Errorf("tools[%d] (%s).description is empty", i, name)
		}
		schema, ok := tl["inputSchema"].(map[string]any)
		if !ok {
			t.Errorf("tools[%d] (%s).inputSchema is not an object", i, name)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("tools[%d] (%s).inputSchema.type = %v, want \"object\"", i, name, schema["type"])
		}
	}
	// Spot-check that key tools are present.
	for _, expected := range []string{
		"forktrust_list", "forktrust_new", "forktrust_finish", "forktrust_rm",
		"forktrust_scope", "forktrust_pr", "forktrust_pr_status", "forktrust_doctor",
		"forktrust_cd", "forktrust_status",
	} {
		if !names[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}
}

func TestUnknownMethod(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "totally/made/up",
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %v", resps[0])
	}
	if errObj["code"].(float64) != -32601 {
		t.Errorf("code = %v, want -32601", errObj["code"])
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	s := New("dummy")
	// No "id" field → notification per JSON-RPC 2.0
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	if len(resps) != 0 {
		t.Errorf("notifications must not produce responses, got %d", len(resps))
	}
}

func TestParseErrorOnMalformedLine(t *testing.T) {
	s := New("dummy")
	in := bytes.NewBufferString("not json at all\n")
	out := bytes.Buffer{}
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var r map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &r); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	errObj, ok := r["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object")
	}
	if errObj["code"].(float64) != -32700 {
		t.Errorf("code = %v, want -32700 parse error", errObj["code"])
	}
}

func TestInvalidJSONRPCVersion(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "1.0", // wrong
		"id":      1,
		"method":  "tools/list",
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %v", resps[0])
	}
	if errObj["code"].(float64) != -32600 {
		t.Errorf("code = %v, want -32600 invalid request", errObj["code"])
	}
}

func TestPing(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      "p1",
		"method":  "ping",
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	if _, ok := resps[0]["result"].(map[string]any); !ok {
		t.Errorf("ping should return result object, got: %v", resps[0])
	}
}

func TestToolsCallMissingArguments(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      "x",
		"method":  "tools/call",
		// no params at all → invalid params
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object")
	}
	if errObj["code"].(float64) != -32602 {
		t.Errorf("code = %v, want -32602", errObj["code"])
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      "x",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "no_such_tool",
			"arguments": map[string]any{},
		},
	})
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object")
	}
	if errObj["code"].(float64) != -32602 {
		t.Errorf("code = %v, want -32602", errObj["code"])
	}
}

func TestStringIDRoundtrip(t *testing.T) {
	// JSON-RPC IDs can be strings, numbers, or null. We must pass them through verbatim.
	s := New("dummy")
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-abc-123",
		"method":  "tools/list",
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	if got, want := resps[0]["id"], "req-abc-123"; got != want {
		t.Errorf("id = %v, want %v", got, want)
	}
}

func TestMultipleRequestsInOneStream(t *testing.T) {
	s := New("dummy")
	in := bytes.Buffer{}
	for i := 1; i <= 3; i++ {
		data, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      i,
			"method":  "ping",
		})
		in.Write(data)
		in.WriteByte('\n')
	}
	out := bytes.Buffer{}
	if err := s.Serve(context.Background(), &in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d: %q", len(lines), out.String())
	}
}

// TestScopeMutexEnforced verifies the MCP layer rejects --set + --clear in the same call.
// The forktrust scope command also enforces this, but catching it at the MCP layer means
// the model gets a faster, clearer error.
func TestScopeMutexEnforced(t *testing.T) {
	s := New("dummy") // tool handlers never reach exec because mutex check fails first
	resps := runOneRequest(t, s, map[string]any{
		"jsonrpc": "2.0",
		"id":      "x",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "forktrust_scope",
			"arguments": map[string]any{
				"slug":  "foo",
				"set":   "a/**",
				"clear": true,
			},
		},
	})
	res, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got: %v", resps[0])
	}
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Errorf("expected isError=true on mutex violation, got: %v", res)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("expected content array with at least one block")
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	if !strings.Contains(text, "mutually exclusive") {
		t.Errorf("expected mutex error in text, got: %q", text)
	}
}
