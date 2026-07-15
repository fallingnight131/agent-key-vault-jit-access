package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fallingnight/akv/internal/mcp"
)

func main() {
	controlURL := os.Getenv("AKV_CONTROL_URL")
	if controlURL == "" {
		controlURL = "http://127.0.0.1:8080"
	}
	executionURL := os.Getenv("AKV_EXECUTION_URL")
	if executionURL == "" {
		executionURL = "http://127.0.0.1:8081"
	}
	client, err := mcp.NewClient(mcp.Config{ControlURL: controlURL, ExecutionURL: executionURL, TokenFile: os.Getenv("AKV_AGENT_TOKEN_FILE")})
	if err != nil {
		fmt.Fprintln(os.Stderr, "AKV MCP configuration unavailable")
		os.Exit(2)
	}
	defer client.Close()
	if err := mcp.NewServer(client).Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "AKV MCP transport stopped")
		os.Exit(1)
	}
}
