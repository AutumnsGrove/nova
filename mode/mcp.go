package mode

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mcp"
)

// mcpShutdownTimeout bounds stopping every MCP server on close or reload.
const mcpShutdownTimeout = 10 * time.Second

// buildMCPHost constructs the MCP host for cfg. It is not started here; New and
// a reload start it in the background.
func (s *Server) buildMCPHost(cfg *config.Config) *mcp.Host {
	return mcp.NewHost(cfg.MCP, mcp.HostOptions{
		Registry: s.tools,
		Version:  s.opt.Version,
		Scrub:    mcpScrub(cfg),
		Warn:     s.emitExtensionError,
	})
}

// mcpScrub names the variables an MCP server never inherits: Nova's own
// credentials, plus anything the user asked to hide from extension processes,
// minus the names they asked to pass through anyway. A server is a third-party
// program, and the credential of a provider it does not call is a key it can
// leak.
func mcpScrub(cfg *config.Config) []string {
	patterns := append(providerauth.CredentialEnv(), cfg.Extensions.ScrubEnv...)
	if len(cfg.Extensions.PassEnv) == 0 {
		return patterns
	}
	return slices.DeleteFunc(patterns, func(p string) bool { return slices.Contains(cfg.Extensions.PassEnv, p) })
}

// startMCP connects every configured server in the background and reports a
// failure as an extension_error: a server that will not start must never delay a
// run, and must never be silent either. done closes when it is finished, so
// Close and a reload never race a start still in flight.
func (s *Server) startMCP(host *mcp.Host, done chan struct{}) {
	defer close(done)
	for _, err := range host.Start(context.Background()) {
		s.emitExtensionError(err)
	}
}

// reloadMCP replaces the host with one built from the current config, so a
// changed server, command, environment or tool list arrives on reload. The old
// processes are gone before the new ones start.
func (s *Server) reloadMCP() {
	s.mu.Lock()
	old, done, cfg := s.mcp, s.mcpStarted, s.cfg
	s.mu.Unlock()
	if old != nil {
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), mcpShutdownTimeout)
		_ = old.Shutdown(ctx)
		cancel()
	}
	host, started := s.buildMCPHost(cfg), make(chan struct{})
	s.mu.Lock()
	s.mcp, s.mcpStarted = host, started
	s.mu.Unlock()
	go s.startMCP(host, started)
}

// mcpStatus reports every server's state, for get_extensions and doctor.
func (s *Server) mcpStatus() []mcp.Status {
	s.mu.Lock()
	host := s.mcp
	s.mu.Unlock()
	if host == nil {
		return nil
	}
	return host.Status()
}

// mcpReport renders the server table `/mcp` prints. A server that failed shows
// its reason on the same line, because that is the line a user reads to decide
// whether to fix the config.
func mcpReport(status []mcp.Status) string {
	if len(status) == 0 {
		return "no MCP servers configured (mcp.servers)"
	}
	lines := make([]string, 0, len(status)+1)
	for _, s := range status {
		line := fmt.Sprintf("%s: %s, %d tool(s)", s.Name, s.State, s.Tools)
		if s.Error != "" {
			line += ": " + s.Error
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// shutdownMCP stops every server, waiting for a start in flight first.
func (s *Server) shutdownMCP(ctx context.Context) error {
	s.mu.Lock()
	host, done := s.mcp, s.mcpStarted
	s.mu.Unlock()
	if host == nil {
		return nil
	}
	<-done
	return host.Shutdown(ctx)
}
