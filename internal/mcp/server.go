// Package mcp provides the MCP server and tool implementations for phonefast,
// built on github.com/mark3labs/mcp-go for MCP protocol compliance.
package mcp

import (
	"encoding/json"
	"fmt"
	"log"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/gezihua123/phonefast/internal/daemon"
)

// MCPConfig holds the MCP server configuration.
type MCPConfig struct {
	Transport string // "sse" or "stdio"
	Host      string
	Port      int
	Path      string
}

// rpcCaller is the subset of *daemon.Client that the MCP server uses to talk
// to the unified daemon. Defining it as an interface lets tests inject a fake
// client without spinning up a real daemon.
type rpcCaller interface {
	Call(method string, params map[string]any) (json.RawMessage, error)
}

// Server exposes phonefast operations as MCP tools. It does NOT hold a device
// session directly — every tool call is routed through the unified daemon via
// JSON-RPC, so device selection and the scrcpy session live in exactly one
// place (the daemon). This avoids the old problem of the MCP process and the
// daemon each holding their own session on the same device and killing each
// other's scrcpy server.
//
// Daemon-crash recovery is handled inside daemon.Client (via daemon.SetEnsurer),
// not here: rpcCall is just Call + Unmarshal.
type Server struct {
	// rpcClient talks to the unified daemon. The daemon injects `device=serial`
	// into every request (see daemon.Client.Call). nil means "no daemon" —
	// rpcCall returns a retry-style error.
	rpcClient rpcCaller

	// mcp-go server instance
	mcpServer *mcpserver.MCPServer
}

// New creates a new MCP server bound to the given device serial via the
// unified daemon. Pass serial="" to let the daemon auto-detect the first
// connected device per request.
func New(serial string) *Server {
	srv := &Server{rpcClient: daemon.NewClient(serial)}
	srv.initMCPServer()
	return srv
}

// newWithClient is the test constructor: inject a fake RPC caller so handlers
// can be exercised without a real daemon.
func newWithClient(c rpcCaller) *Server {
	srv := &Server{rpcClient: c}
	srv.initMCPServer()
	return srv
}

// initMCPServer creates the mcp-go server and registers all tools.
func (s *Server) initMCPServer() {
	s.mcpServer = mcpserver.NewMCPServer(
		"phonefast",
		"0.1.0",
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithInputSchemaValidation(),
	)

	// Register all tools — handlers are defined in tools.go
	s.registerTools()
}

// rpcCall sends a JSON-RPC request to the daemon and decodes the result into
// out. The daemon.Client handles restart-on-unreachable transparently (via
// daemon.SetEnsurer), so this is a plain Call + Unmarshal. Returns an error if
// the daemon is not reachable or the RPC itself failed.
func (s *Server) rpcCall(method string, params map[string]any, out any) error {
	if s.rpcClient == nil {
		return fmt.Errorf("device connecting, retry in a moment")
	}
	result, err := s.rpcClient.Call(method, params)
	if err != nil {
		return err
	}
	if len(result) == 0 {
		return nil
	}
	return json.Unmarshal(result, out)
}

// Run starts the MCP server with the given configuration.
func (s *Server) Run(cfg MCPConfig) error {
	switch cfg.Transport {
	case "sse":
		return s.runSSE(cfg)
	case "stdio":
		return s.runSTDIO()
	default:
		return fmt.Errorf("unknown transport: %s", cfg.Transport)
	}
}

// runSTDIO starts the MCP server in STDIO mode.
func (s *Server) runSTDIO() error {
	log.Printf("[phonefast] MCP STDIO server started (mcp-go)")
	return mcpserver.ServeStdio(s.mcpServer)
}

// runSSE starts the MCP server in SSE (Server-Sent Events) mode.
func (s *Server) runSSE(cfg MCPConfig) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	// Build SSE server with custom path
	var opts []mcpserver.SSEOption
	if cfg.Path != "" && cfg.Path != "/" {
		opts = append(opts,
			mcpserver.WithSSEEndpoint(cfg.Path+"/sse"),
			mcpserver.WithMessageEndpoint(cfg.Path+"/messages"),
		)
		// For base path support, we need to use WithBasePath or custom HTTP server
	}

	sseServer := mcpserver.NewSSEServer(s.mcpServer, opts...)

	log.Printf("[phonefast] MCP SSE server listening on %s", addr)
	return sseServer.Start(addr)
}

// MCPServer returns the underlying mcp-go server (used by tools_test.go).
func (s *Server) MCPServer() *mcpserver.MCPServer {
	return s.mcpServer
}
