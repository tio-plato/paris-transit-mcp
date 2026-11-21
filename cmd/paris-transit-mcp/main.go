// Command paris-transit-mcp is an MCP server for Paris public transport.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

// version is set at build time with -ldflags "-X main.version=…".
var version string

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "paris-transit-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	token, err := prim.TokenFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("read the PRIM token: %w", err)
	}
	client := prim.New(prim.Config{BaseURL: prim.DefaultBaseURL, Token: token, Timeout: 0, Rate: 0})
	svc, err := transit.New(client, time.Now)
	if err != nil {
		return fmt.Errorf("start the tools: %w", err)
	}
	info, ok := debug.ReadBuildInfo()
	server, err := newServer(svc, serverVersion(version, info, ok))
	if err != nil {
		return err
	}
	err = server.Run(ctx, &mcp.StdioTransport{})
	// A signal is how clients stop us; whatever Run returns then is fine.
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve MCP over stdio: %w", err)
	}
	return nil
}

func serverVersion(injected string, info *debug.BuildInfo, ok bool) string {
	if injected != "" {
		return injected
	}
	if !ok || info.Main.Version == "" {
		return "devel"
	}
	return info.Main.Version
}
