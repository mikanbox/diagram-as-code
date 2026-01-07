package main

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/spf13/pflag"

	"github.com/awslabs/diagram-as-code/internal/ctl"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	log "github.com/sirupsen/logrus"
)

//go:embed prompts/*
var promptsFS embed.FS

var (
	writeFileFunc = os.WriteFile
	readFileFunc  = os.ReadFile
)

type ToolName string

const (
	GENERATE_DIAGRAM           ToolName = "generateDiagram"
	GENERATE_DIAGRAM_TO_FILE   ToolName = "generateDiagramToFile"
	GET_DIAGRAM_AS_CODE_FORMAT ToolName = "getDiagramAsCodeFormat"
)

const (
	USER_REQUIREMENTS_TEMPLATE_FILE = "prompts/generate_dac_from_user_requirements.txt"
)

const (
	GENERATE_DIAGRAM_DESC           = "Generate AWS architecture diagrams from YAML-based Diagram-as-code specifications."
	GENERATE_DIAGRAM_TO_FILE_DESC   = "Generate AWS architecture diagrams from YAML and save directly to file."
	GET_FORMAT_DESC                 = "Get Diagram-as-code format specification, examples, and best practices."
)

func NewMCPServer() *server.MCPServer {
	hooks := &server.Hooks{}

	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		log.Infof("beforeAny: %s, %v", method, id)
	})
	hooks.AddOnSuccess(func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
		log.Infof("onSuccess: %s, %v", method, id)
	})
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		log.Errorf("onError: %s, %v, %v", method, id, err)
	})

	mcpServer := server.NewMCPServer(
		"awsdac-mcp-server-streamable",
		"0.0.1",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
		server.WithHooks(hooks),
		server.WithInstructions(`AWS Diagram-as-Code MCP Server (Streamable HTTP)

PURPOSE:
Generate professional AWS architecture diagrams from YAML-based specifications via HTTP transport.

ESSENTIAL WORKFLOW:
1. Call 'getDiagramAsCodeFormat' first to understand the format and get examples
2. Use the format guide to create proper YAML content
3. Call 'generateDiagram' or 'generateDiagramToFile' with the complete YAML specification
4. Receive a base64-encoded PNG diagram

CAPABILITIES:
- Generate PNG diagrams with AWS resource icons and relationships
- Support hierarchical layouts with Canvas → Cloud → Region → VPC → Subnets → Resources
- Create network connections with Links (straight or orthogonal lines)
- Handle complex layouts using VerticalStack and HorizontalStack groupings

OUTPUT: Base64-encoded PNG images suitable for embedding in responses`),
	)

	mcpServer.AddTool(mcp.NewTool(string(GENERATE_DIAGRAM),
		mcp.WithDescription(GENERATE_DIAGRAM_DESC),
		mcp.WithString("yamlContent",
			mcp.Required(),
			mcp.Description("Complete YAML specification for the AWS architecture diagram"),
		),
	), withPanicRecovery("generateDiagram", handleGenerateDiagram))

	mcpServer.AddTool(mcp.NewTool(string(GENERATE_DIAGRAM_TO_FILE),
		mcp.WithDescription(GENERATE_DIAGRAM_TO_FILE_DESC),
		mcp.WithString("yamlContent",
			mcp.Required(),
			mcp.Description("Complete YAML specification for the AWS architecture diagram"),
		),
		mcp.WithString("outputFilePath",
			mcp.Required(),
			mcp.Description("Path where the generated PNG file should be saved"),
		),
	), withPanicRecovery("generateDiagramToFile", handleGenerateDiagramToFile))

	mcpServer.AddTool(mcp.NewTool(string(GET_DIAGRAM_AS_CODE_FORMAT),
		mcp.WithDescription(GET_FORMAT_DESC),
	), withPanicRecovery("getDiagramAsCodeFormat", handleGenerateDacFromUserRequirements))

	return mcpServer
}

func handleGenerateDiagram(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	arguments := request.GetArguments()
	yamlContentArg, exists := arguments["yamlContent"]
	if !exists {
		return nil, fmt.Errorf("missing yamlContent argument")
	}
	yamlContent, ok := yamlContentArg.(string)
	if !ok {
		return nil, fmt.Errorf("invalid yamlContent argument")
	}

	outputFormatArg, _ := arguments["outputFormat"]
	outputFormat, _ := outputFormatArg.(string)
	if outputFormat == "" {
		outputFormat = "png"
	}

	tempDir, err := os.MkdirTemp("", "awsdac-mcp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			log.Printf("Failed to remove temp directory: %v", err)
		}
	}()

	inputFile := filepath.Join(tempDir, "input.yaml")
	if err := writeFileFunc(inputFile, []byte(yamlContent), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input file: %v", err)
	}

	outputFile := filepath.Join(tempDir, "output.png")

	opts := &ctl.CreateOptions{
		OverwriteMode: ctl.Force,
	}
	if err := createDiagramSafely(inputFile, &outputFile, opts); err != nil {
		return nil, fmt.Errorf("failed to create diagram: %v", err)
	}

	diagramData, err := readFileFunc(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read generated diagram: %v", err)
	}

	base64Diagram := base64.StdEncoding.EncodeToString(diagramData)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Diagram generated successfully",
			},
			mcp.ImageContent{
				Type:     "image",
				Data:     base64Diagram,
				MIMEType: "image/png",
			},
		},
	}, nil
}

func handleGenerateDiagramToFile(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	arguments := request.GetArguments()

	yamlContentArg, exists := arguments["yamlContent"]
	if !exists {
		return nil, fmt.Errorf("missing yamlContent argument")
	}
	yamlContent, ok := yamlContentArg.(string)
	if !ok {
		return nil, fmt.Errorf("invalid yamlContent argument")
	}

	outputFilePathArg, exists := arguments["outputFilePath"]
	if !exists {
		return nil, fmt.Errorf("missing outputFilePath argument")
	}
	outputFilePath, ok := outputFilePathArg.(string)
	if !ok {
		return nil, fmt.Errorf("invalid outputFilePath argument")
	}

	outputDir := filepath.Dir(outputFilePath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %v", err)
	}

	tempDir, err := os.MkdirTemp("", "awsdac-mcp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			log.Printf("Failed to remove temp directory: %v", err)
		}
	}()

	inputFile := filepath.Join(tempDir, "input.yaml")
	if err := writeFileFunc(inputFile, []byte(yamlContent), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input file: %v", err)
	}

	opts := &ctl.CreateOptions{
		OverwriteMode: ctl.NoOverwrite,
	}
	if err := createDiagramSafely(inputFile, &outputFilePath, opts); err != nil {
		return nil, fmt.Errorf("failed to create diagram: %v", err)
	}

	if _, err := os.Stat(outputFilePath); err != nil {
		return nil, fmt.Errorf("failed to verify generated diagram file: %v", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Diagram successfully generated and saved to: %s", outputFilePath),
			},
		},
	}, nil
}

func handleGenerateDacFromUserRequirements(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	templateContent, err := readPromptFile(USER_REQUIREMENTS_TEMPLATE_FILE)
	if err != nil {
		return nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: string(templateContent),
			},
		},
	}, nil
}

func withPanicRecovery(handlerName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.WithFields(log.Fields{
					"handler":      handlerName,
					"panic_value":  r,
					"request_name": request.Params.Name,
				}).Errorf("Panic recovered in handler: %v\nStack trace:\n%s", r, debug.Stack())

				result = &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{
							Type: "text",
							Text: "An unexpected error occurred while processing your request.\n\n" +
								"The server has recovered and is ready to process new requests.\n" +
								"Please check the server logs for detailed diagnostic information.",
						},
					},
					IsError: true,
				}
				err = nil
			}
		}()

		return handler(ctx, request)
	}
}

func createDiagramSafely(inputFile string, outputFile *string, opts *ctl.CreateOptions) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{
				"panic_value": r,
				"input_file":  inputFile,
				"output_file": func() string {
					if outputFile != nil {
						return *outputFile
					}
					return "<nil>"
				}(),
			}).Errorf("Panic in diagram creation: %v\nStack trace:\n%s", r, debug.Stack())

			err = fmt.Errorf("panic occurred during diagram creation: %v", r)
		}
	}()
	return ctl.CreateDiagramFromDacFile(inputFile, outputFile, opts)
}

func readPromptFile(filePath string) ([]byte, error) {
	content, err := promptsFS.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded prompt file %s: %v", filePath, err)
	}
	return content, nil
}

func main() {
	logFilePath := pflag.String("log-file", "", "Path to log file")
	port := pflag.String("port", "8080", "Port to listen on")
	endpoint := pflag.String("endpoint", "/mcp", "Endpoint path for MCP requests")
	stateless := pflag.Bool("stateless", false, "Enable stateless mode (no session management, new transport per request)")
	pflag.Parse()

	var actualLogPath string
	if *logFilePath != "" {
		actualLogPath = *logFilePath
	} else {
		actualLogPath = filepath.Join(os.TempDir(), "awsdac-mcp-server-streamable.log")
	}

	logFile, err := os.OpenFile(actualLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			log.Printf("Failed to close log file: %v", err)
		}
	}()
	log.SetOutput(logFile)
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	log.Infof("Starting MCP server with streamable HTTP transport on port %s, endpoint %s, stateless mode: %v", *port, *endpoint, *stateless)
	
	mcpServer := NewMCPServer()
	
	var httpServerOpts []server.StreamableHTTPOption
	httpServerOpts = append(httpServerOpts, server.WithEndpointPath(*endpoint))
	
	if *stateless {
		httpServerOpts = append(httpServerOpts, server.WithStateLess(true))
		log.Infof("Stateless mode enabled: no session management, each request is independent")
	}
	
	httpServer := server.NewStreamableHTTPServer(mcpServer, httpServerOpts...)

	addr := ":" + *port
	log.Infof("Server listening on %s%s", addr, *endpoint)
	
	if err := httpServer.Start(addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

