package main

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPToolsAreDiscoverableAndCallable(t *testing.T) {
	config := testConfig()
	extractor := &fakeExtractor{result: ExtractResult{Text: "hello", InputBytes: 8, OutputBytes: 5, ElapsedMS: 1}}
	server := NewMCPServer(config, extractor)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	tools, err := clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools.Tools))
	}

	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "extract_pdf_base64",
		Arguments: map[string]any{
			"pdf_base64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.7")),
			"filename":   "sample.pdf",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %#v", result.Content)
	}
}

func TestMCPStreamableHTTPAtMountedEndpoint(t *testing.T) {
	config := testConfig()
	config.MCPAllowedHosts = []string{"127.0.0.1:*"}
	config.MCPAllowedOrigins = []string{"http://127.0.0.1:*"}
	extractor := &fakeExtractor{result: ExtractResult{Text: "hello", InputBytes: 8, OutputBytes: 5}}
	server := NewMCPServer(config, extractor)
	handler, err := NewHTTPHandler(config, extractor, NewMCPHTTPHandler(config, server))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools.Tools))
	}
}

func TestMCPAccessControl(t *testing.T) {
	patterns := []string{"localhost", "localhost:*"}
	for _, value := range []string{"localhost", "localhost:8000"} {
		if !allowedValue(value, patterns) {
			t.Fatalf("%q should be allowed", value)
		}
	}
	if allowedValue("attacker.example", patterns) {
		t.Fatal("unexpected host allowance")
	}
	if allowedValue("localhost:8000.evil", patterns) {
		t.Fatal("wildcard port accepted a non-numeric suffix")
	}
}
