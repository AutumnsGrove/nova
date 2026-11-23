package config

import (
	"path/filepath"
	"testing"
)

// mcpConfig is a two-server config for the MCP tests.
const mcpConfig = `
mcp:
  servers:
    - name: fs
      command: /usr/local/bin/mcp-fs
      args: ["/tmp", "--ro"]
      env:
        LOG: warn
      cwd: ~/work
      timeout_ms: 1500
    - name: db
      command: mcp-db
`

func TestLoad_MCPServers(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), mcpConfig)

	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(cfg.MCP.Servers) != 2 {
		t.Fatalf("servers = %+v, want two", cfg.MCP.Servers)
	}
	fs := cfg.MCP.Servers[0]
	if fs.Name != "fs" || fs.Command != "/usr/local/bin/mcp-fs" {
		t.Fatalf("server = %+v", fs)
	}
	if len(fs.Args) != 2 || fs.Args[1] != "--ro" {
		t.Fatalf("args = %v", fs.Args)
	}
	if fs.Env["LOG"] != "warn" {
		t.Fatalf("env = %v", fs.Env)
	}
	if want := filepath.Join(home, "work"); fs.CWD != want {
		t.Fatalf("cwd = %q, want %q", fs.CWD, want)
	}
	if fs.TimeoutMS != 1500 {
		t.Fatalf("timeout = %d, want 1500", fs.TimeoutMS)
	}
	if db := cfg.MCP.Servers[1]; db.TimeoutMS != DefaultMCPTimeoutMS {
		t.Fatalf("unset timeout = %d, want the %d ms default", db.TimeoutMS, DefaultMCPTimeoutMS)
	}
}

func TestLoad_MCPWarnsOnABadServer(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
mcp:
  servers:
    - command: mcp-nameless
    - name: no-command
    - name: bad name
      command: mcp-x
    - name: good
      command: mcp-good
      timeout_ms: -5
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 4 {
		t.Fatalf("warnings = %v, want one per bad server plus the negative timeout", warnings)
	}
	if len(cfg.MCP.Servers) != 1 || cfg.MCP.Servers[0].Name != "good" {
		t.Fatalf("servers = %+v, want only the valid one", cfg.MCP.Servers)
	}
	if got := cfg.MCP.Servers[0].TimeoutMS; got != DefaultMCPTimeoutMS {
		t.Fatalf("negative timeout = %d, want the default", got)
	}
}

func TestLoad_MCPDropsADuplicateName(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
mcp:
  servers:
    - name: fs
      command: one
    - name: fs
      command: two
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one for the duplicate", warnings)
	}
	if len(cfg.MCP.Servers) != 1 || cfg.MCP.Servers[0].Command != "one" {
		t.Fatalf("servers = %+v, want the first one only", cfg.MCP.Servers)
	}
}
