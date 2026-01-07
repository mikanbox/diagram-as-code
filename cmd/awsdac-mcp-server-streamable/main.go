package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/awslabs/diagram-as-code/internal/ctl"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var port = flag.String("port", "8000", "port to listen on")

func corsMiddleware(port string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", fmt.Sprintf("http://127.0.0.1:%s", port))
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func handleGenerateDiagram(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	yamlContent, ok := args["yamlContent"].(string)
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "Error: Invalid argument. 'yamlContent' must be a string.",
				},
			},
		}, nil
	}

	tempDir, err := os.MkdirTemp("", "awsdac-mcp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	inputFile := filepath.Join(tempDir, "input.yaml")
	if err := os.WriteFile(inputFile, []byte(yamlContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write input file: %v", err)
	}

	outputFile := filepath.Join(tempDir, "output.png")

	opts := &ctl.CreateOptions{
		OverwriteMode: ctl.Force,
	}
	if err := ctl.CreateDiagramFromDacFile(inputFile, &outputFile, opts); err != nil {
		return nil, fmt.Errorf("failed to create diagram: %v", err)
	}

	diagramData, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read generated diagram: %v", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Diagram generated successfully (size: %d bytes)", len(diagramData)),
			},
			mcp.ImageContent{
				Type:     "image",
				Data:     base64.StdEncoding.EncodeToString(diagramData),
				MIMEType: "image/png",
			},
		},
	}, nil
}

func main() {
	flag.Parse()

	mcpServer := server.NewMCPServer(
		"awsdac-mcp-server",
		"v1.0.0",
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	mcpServer.AddTool(mcp.NewTool("generateDiagram",
		mcp.WithDescription("Generate AWS architecture diagrams from YAML-based Diagram-as-code specifications"),
		mcp.WithString("yamlContent",
			mcp.Required(),
			mcp.Description("Complete YAML specification for the AWS architecture diagram"),
		),
	), handleGenerateDiagram)

	streamableServer := server.NewStreamableHTTPServer(mcpServer, server.WithStateLess(true))

	mux := http.NewServeMux()

	mux.Handle("/mcp", streamableServer)

	log.Printf("Server listening at http://localhost:%s", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", *port), corsMiddleware(*port)(mux)))
}
