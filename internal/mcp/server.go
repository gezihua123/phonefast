// Package mcp provides the MCP server and tool implementations for phonefast,
// built on github.com/mark3labs/mcp-go for MCP protocol compliance.
package mcp

import (
	"fmt"
	"log"
	"sync"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/gezihua123/phonefast/internal/session"
)

// MCPConfig holds the MCP server configuration.
type MCPConfig struct {
	Transport string // "sse" or "stdio"
	Host      string
	Port      int
	Path      string
}

// Server wraps a phonefast session and exposes MCP-compatible tools
// via the mcp-go library.
type Server struct {
	mu      sync.Mutex
	session *session.Session
	serial  string
	scid    int

	// mcp-go server instance
	mcpServer *mcpserver.MCPServer
}

// New creates a new MCP server wrapping a device session.
// session may be nil for lazy initialization (STDIO mode starts before device is ready).
func New(s *session.Session, serial string, scid int) *Server {
	srv := &Server{
		session: s,
		serial:  serial,
		scid:    scid,
	}
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

// SetSession updates the device session (used for lazy init in STDIO mode).
func (s *Server) SetSession(sess *session.Session, serial string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = sess
	s.serial = serial
}

// getSession returns the current session under lock.
func (s *Server) getSession() *session.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

// needSession returns the current session or an error if not yet connected.
func (s *Server) needSession() (*session.Session, error) {
	sess := s.getSession()
	if sess == nil {
		return nil, fmt.Errorf("device connecting, retry in a moment")
	}
	return sess, nil
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

// ── Tool result helpers (kept for backward compat in tools.go) ──

// ToolResult is the JSON-serializable result of a tool call.
type ToolResult struct {
	Content []ToolContent `json:"content"`
}

// ToolContent is a single content item in a tool result.
type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

func textResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ToolContent{
			{Type: "text", Text: text},
		},
	}
}

func errorResult(err error) *ToolResult {
	return &ToolResult{
		Content: []ToolContent{
			{Type: "text", Text: fmt.Sprintf("error: %v", err)},
		},
	}
}
