// Command paris-transit-mcp is a read-only MCP server for Île-de-France public
// transport on Île-de-France Mobilités' PRIM data, over stdio.
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
	server, err := newServer(svc, version())
	if err != nil {
		return err
	}
	err = server.Run(ctx, &mcp.StdioTransport{})
	// SIGINT or SIGTERM is how a client stops the server: whatever the
	// session returns then, closed pipes included, is no failure.
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve MCP over stdio: %w", err)
	}
	return nil
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "devel"
	}
	return info.Main.Version
}
