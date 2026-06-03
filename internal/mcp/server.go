package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Server is an MCP server that exposes forktrust commands over stdio.
// Construct with New(binaryPath), then call Serve(stdin, stdout) once.
// Errors during request handling are converted to JSON-RPC error responses;
// Serve only returns when stdin EOFs or a hard I/O error occurs.
type Server struct {
	// Binary is the path to the forktrust executable to invoke for each
	// tool call. Defaults to os.Args[0] when an empty string is passed to
	// New, but callers should resolve via os.Executable() for safety.
	Binary string
	// Version reported in the initialize handshake's serverInfo.version.
	// Empty falls back to DefaultServerVersion ("dev") so the MCP server is
	// still usable in tests / unbuilt checkouts.
	Version string
	// MaxLineSize bounds the largest JSON-RPC frame the server will accept
	// in a single line. Defaults to 16 MiB which is comfortably above
	// realistic MCP payloads.
	MaxLineSize int
}

// New constructs a Server with reasonable defaults. Pass the build-time
// version string so the initialize handshake reports the actual binary
// version (otherwise serverInfo.version drifts from what `forktrust --version`
// reports, which has happened before).
func New(binary, version string) *Server {
	if version == "" {
		version = DefaultServerVersion
	}
	return &Server{Binary: binary, Version: version, MaxLineSize: 16 * 1024 * 1024}
}

// versionOrDefault returns Server.Version, falling back to DefaultServerVersion
// for zero-value Server structs constructed without New().
func (s *Server) versionOrDefault() string {
	if s.Version == "" {
		return DefaultServerVersion
	}
	return s.Version
}

// Serve runs the JSON-RPC 2.0 + MCP read loop until in EOFs. Each line on `in`
// is one request; each response is written as one line on `out`. The function
// is single-threaded by design: MCP tool calls can be slow (verify gates,
// network round-trips) and serializing keeps the contract simple.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	br := bufio.NewReaderSize(in, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := readLine(br, s.MaxLineSize)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		// Trim trailing whitespace; ignore empty/whitespace-only lines so
		// clients that send blank keepalives don't poison the loop.
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
			// We have no ID to echo back; per JSON-RPC, emit null-ID error.
			_ = writeJSON(out, rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}
		if req.JSONRPC != "2.0" {
			_ = writeJSON(out, rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32600, Message: "invalid request: jsonrpc must be \"2.0\""},
			})
			continue
		}

		resp := s.handle(ctx, &req)
		// Notifications (no ID present) get no response. JSON-RPC says ID
		// absent means notification.
		if len(req.ID) == 0 {
			continue
		}
		if resp == nil {
			// Defensive — handlers should always return something when there is an ID.
			resp = &rpcResponse{Error: &rpcError{Code: -32603, Message: "internal error: nil response"}}
		}
		resp.JSONRPC = "2.0"
		resp.ID = req.ID
		if err := writeJSON(out, *resp); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
}

// handle dispatches one request to the appropriate method handler.
func (s *Server) handle(ctx context.Context, req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return s.initialize()
	case "initialized", "notifications/initialized":
		// Notification — no response.
		return nil
	case "ping":
		return &rpcResponse{Result: map[string]any{}}
	case "tools/list":
		return s.toolsList()
	case "tools/call":
		return s.toolsCall(ctx, req)
	default:
		return &rpcResponse{Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

// initialize returns the MCP server capabilities. We advertise the tools
// capability so clients enumerate via tools/list.
func (s *Server) initialize() *rpcResponse {
	return &rpcResponse{
		Result: initializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: map[string]any{
				"tools": map[string]any{
					// listChanged: false because forktrust's tool set is static
					// per binary version; clients can cache the tools/list result.
					"listChanged": false,
				},
			},
			ServerInfo: map[string]any{
				"name":    ServerName,
				"version": s.versionOrDefault(),
			},
		},
	}
}

// toolsList returns the static set of tools defined in tools.go.
func (s *Server) toolsList() *rpcResponse {
	return &rpcResponse{Result: toolsListResult{Tools: allTools}}
}

// toolsCall dispatches to a tool handler. Tool errors become tool-level
// errors (callResult.isError = true), NOT JSON-RPC errors — JSON-RPC errors
// signal "the request was malformed" rather than "the tool ran but failed".
func (s *Server) toolsCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var call callRequest
	if err := json.Unmarshal(req.Params, &call); err != nil {
		return &rpcResponse{Error: &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}}
	}
	if call.Name == "" {
		return &rpcResponse{Error: &rpcError{Code: -32602, Message: "invalid params: tool name required"}}
	}
	handler, ok := handlers[call.Name]
	if !ok {
		return &rpcResponse{Error: &rpcError{Code: -32602, Message: "unknown tool: " + call.Name}}
	}
	text, isErr := handler(ctx, s.Binary, call.Arguments)
	return &rpcResponse{Result: callResult{
		Content: []contentBlock{{Type: "text", Text: text}},
		IsError: isErr,
	}}
}

// readLine reads up to '\n' from r, returning the bytes WITHOUT the
// terminator. Returns io.EOF only when the stream ends cleanly; partial
// final lines without trailing '\n' are returned with io.EOF as the error.
// max bounds the line length to defend against malicious unbounded input.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if len(line) > 0 && errors.Is(err, io.EOF) {
				return line, io.EOF
			}
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > max {
			return nil, fmt.Errorf("line exceeds %d bytes", max)
		}
		if !isPrefix {
			return line, nil
		}
	}
}

// writeJSON marshals v and writes it followed by a newline. We deliberately
// do NOT pretty-print: MCP stdio is newline-framed, and pretty-printing
// would break the framing.
func writeJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
