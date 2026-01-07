package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"

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

func handleAddTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	a, aOk := args["a"].(float64)
	b, bOk := args["b"].(float64)

	if !aOk || !bOk {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "Error: Invalid arguments. Both 'a' and 'b' must be numbers.",
				},
			},
		}, nil
	}

	result := int(a) + int(b)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("The sum of %d and %d is %d", int(a), int(b), result),
			},
		},
	}, nil
}

func main() {
	flag.Parse()

	mcpServer := server.NewMCPServer(
		"go-bin-mcp-server",
		"v1.0.0",
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	mcpServer.AddTool(mcp.NewTool("addgen",
		mcp.WithDescription("Add two numbers"),
		mcp.WithNumber("a",
			mcp.Required(),
			mcp.Description("First number to add"),
		),
		mcp.WithNumber("b",
			mcp.Required(),
			mcp.Description("Second number to add"),
		),
	), handleAddTool)

	streamableServer := server.NewStreamableHTTPServer(mcpServer, server.WithStateLess(true))

	mux := http.NewServeMux()

	mux.Handle("/mcp", streamableServer)

	log.Printf("Server listening at http://localhost:%s", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", *port), corsMiddleware(*port)(mux)))
}
