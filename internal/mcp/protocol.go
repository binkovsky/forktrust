// Package mcp implements a Model Context Protocol (MCP) server over stdio
// that exposes forktrust commands as typed tools. AI agents that speak MCP
// (Claude Code, Cursor, etc.) can call forktrust operations natively without
// shelling out and parsing CLI output themselves.
//
// Wire format: newline-delimited JSON-RPC 2.0 over stdio, per the MCP
// 2024-11-05 spec for the stdio transport. Each message is one line of JSON
// terminated by '\n'.
//
// Each MCP tool is a thin wrapper around the corresponding `forktrust <cmd>
// --json` subprocess: the handler builds the argv from the tool's input
// arguments, invokes the running binary (via os.Executable()), and returns
// the JSON envelope as the tool's text content. This keeps the MCP layer
// decoupled from the internal CLI command implementations — no shared
// globals, no need to mutate cobra flag state — and it inherits every
// hardening fix (pre-flight refusal, JSON envelope contract, etc.) for free.
package mcp

import "encoding/json"

// ProtocolVersion is the MCP spec version we declare on initialize. Bumping
// it requires updating tool schemas to match the spec at that version.
const ProtocolVersion = "2024-11-05"

// ServerName goes into the initialize response so MCP clients can identify
// the implementation in their logs and configs. ServerVersion is the default
// when callers do not override Server.Version — production builds inject the
// real build version via mcp.New(binary, version).
const (
	ServerName            = "forktrust-mcp"
	DefaultServerVersion  = "dev"
)

// rpcRequest is one JSON-RPC 2.0 request frame.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// ID is left as RawMessage so we can pass it back verbatim — JSON-RPC
	// allows string, number, or null IDs and we must not coerce.
	ID json.RawMessage `json:"id,omitempty"`
}

// rpcResponse is one JSON-RPC 2.0 response frame. Either Result OR Error
// is populated, never both.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// rpcError is the JSON-RPC 2.0 error object. Standard JSON-RPC codes:
//
//	-32700 parse error
//	-32600 invalid request
//	-32601 method not found
//	-32602 invalid params
//	-32603 internal error
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// initializeResult is the MCP initialize handshake response payload.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      map[string]any `json:"serverInfo"`
}

// tool is one MCP tool definition. inputSchema is JSON Schema (draft 7-ish);
// MCP clients are responsible for validating inputs against this before
// calling tools/call.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolsListResult is the response payload for tools/list.
type toolsListResult struct {
	Tools []tool `json:"tools"`
}

// callRequest is the params shape for tools/call.
type callRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// callResult is the response payload for tools/call.
type callResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// contentBlock is one item in a tool result's content array. forktrust tools
// always return a single text block whose body is the forktrust JSON envelope
// (or, on tool error, a human-readable error message).
type contentBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}
