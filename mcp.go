package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPBase64Input struct {
	PDFBase64  string `json:"pdf_base64" jsonschema:"base64-encoded PDF bytes"`
	Filename   string `json:"filename,omitempty" jsonschema:"source filename for display"`
	Layout     *bool  `json:"layout,omitempty" jsonschema:"preserve the physical text layout; defaults to true"`
	PageBreaks *bool  `json:"page_breaks,omitempty" jsonschema:"preserve page break characters; defaults to true"`
	FirstPage  int    `json:"first_page,omitempty" jsonschema:"first page to extract, starting at 1"`
	LastPage   int    `json:"last_page,omitempty" jsonschema:"last page to extract, starting at 1"`
	Password   string `json:"password,omitempty" jsonschema:"user password for an encrypted PDF"`
}

type MCPFileInput struct {
	RelativePath string `json:"relative_path" jsonschema:"PDF path relative to the server allowed root"`
	Layout       *bool  `json:"layout,omitempty" jsonschema:"preserve the physical text layout; defaults to true"`
	PageBreaks   *bool  `json:"page_breaks,omitempty" jsonschema:"preserve page break characters; defaults to true"`
	FirstPage    int    `json:"first_page,omitempty" jsonschema:"first page to extract, starting at 1"`
	LastPage     int    `json:"last_page,omitempty" jsonschema:"last page to extract, starting at 1"`
	Password     string `json:"password,omitempty" jsonschema:"user password for an encrypted PDF"`
}

type MCPExtractOutput struct {
	Source         string `json:"source" jsonschema:"source filename or relative path"`
	Text           string `json:"text" jsonschema:"extracted UTF-8 text"`
	CharacterCount int    `json:"character_count"`
	InputBytes     int64  `json:"input_bytes"`
	OutputBytes    int64  `json:"output_bytes"`
	ElapsedMS      int64  `json:"elapsed_ms"`
}

func NewMCPServer(config Config, extractor Extractor) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "PDFText", Version: version},
		&mcp.ServerOptions{Instructions: "Extract UTF-8 text from PDF files. Use extract_pdf_base64 for small documents or extract_pdf_file for PDFs in the configured read-only directory."},
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "extract_pdf_base64",
		Description: "Extract text from a small base64-encoded PDF.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input MCPBase64Input) (*mcp.CallToolResult, MCPExtractOutput, error) {
		if int64(base64.StdEncoding.DecodedLen(len(input.PDFBase64))) > config.MCPMaxFileBytes+2 {
			return nil, MCPExtractOutput{}, toolError(fileTooLarge(config.MCPMaxFileBytes))
		}
		pdf, err := base64.StdEncoding.DecodeString(input.PDFBase64)
		if err != nil {
			return nil, MCPExtractOutput{}, errors.New("invalid_base64: pdf_base64 is not valid base64")
		}
		result, err := extractor.Extract(ctx, pdf, mcpOptions(input.Layout, input.PageBreaks, input.FirstPage, input.LastPage, input.Password), config.MCPMaxFileBytes)
		if err != nil {
			return nil, MCPExtractOutput{}, toolError(err)
		}
		source := safeSourceName(input.Filename)
		return nil, mcpOutput(source, result), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "extract_pdf_file",
		Description: "Extract text from a PDF under the server's configured read-only directory.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input MCPFileInput) (*mcp.CallToolResult, MCPExtractOutput, error) {
		pdf, source, err := readAllowedPDF(config, input.RelativePath)
		if err != nil {
			return nil, MCPExtractOutput{}, toolError(err)
		}
		result, err := extractor.Extract(ctx, pdf, mcpOptions(input.Layout, input.PageBreaks, input.FirstPage, input.LastPage, input.Password), config.MaxFileBytes)
		if err != nil {
			return nil, MCPExtractOutput{}, toolError(err)
		}
		return nil, mcpOutput(source, result), nil
	})
	return server
}

func NewMCPHTTPHandler(config Config, server *mcp.Server) http.Handler {
	maxBody := config.MCPMaxFileBytes*4/3 + 1024*1024
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			JSONResponse:                 true,
			MaxRequestBodyBytes:          maxBody,
			PropagateRequestCancellation: true,
		},
	)
	return mcpAccessControl(config, handler)
}

func mcpOptions(layout, pageBreaks *bool, firstPage, lastPage int, password string) ExtractOptions {
	options := ExtractOptions{Layout: true, PageBreaks: true, FirstPage: firstPage, LastPage: lastPage, Password: password}
	if layout != nil {
		options.Layout = *layout
	}
	if pageBreaks != nil {
		options.PageBreaks = *pageBreaks
	}
	return options
}

func mcpOutput(source string, result ExtractResult) MCPExtractOutput {
	return MCPExtractOutput{
		Source: source, Text: result.Text,
		CharacterCount: utf8.RuneCountInString(result.Text),
		InputBytes:     result.InputBytes, OutputBytes: result.OutputBytes, ElapsedMS: result.ElapsedMS,
	}
}

func toolError(err error) error {
	var public *PublicError
	if errors.As(err, &public) {
		return fmt.Errorf("%s: %s", public.Code, public.Message)
	}
	return errors.New("internal_error: extraction failed")
}

func readAllowedPDF(config Config, relativePath string) ([]byte, string, error) {
	if strings.TrimSpace(relativePath) == "" || filepath.IsAbs(relativePath) {
		return nil, "", errors.New("invalid_path: relative_path must be a non-empty relative path")
	}
	root, err := filepath.EvalSymlinks(config.MCPAllowedRoot)
	if err != nil {
		return nil, "", errors.New("allowed_root_unavailable: the configured PDF directory is unavailable")
	}
	target := filepath.Join(root, filepath.Clean(relativePath))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, "", errors.New("file_not_found: the PDF does not exist")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", errors.New("invalid_path: path must remain inside the allowed directory")
	}
	if !strings.EqualFold(filepath.Ext(resolved), ".pdf") {
		return nil, "", errors.New("invalid_file_type: only .pdf files are allowed")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, "", errors.New("file_read_failed: the PDF could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("invalid_file: the path is not a regular PDF file")
	}
	pdf, err := readLimited(file, config.MaxFileBytes)
	if err != nil {
		return nil, "", err
	}
	return pdf, filepath.ToSlash(rel), nil
}

func safeSourceName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." {
		return "document.pdf"
	}
	return name
}

func mcpAccessControl(config Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedValue(r.Host, config.MCPAllowedHosts) {
			http.Error(w, "MCP host is not allowed", http.StatusMisdirectedRequest)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !allowedValue(origin, config.MCPAllowedOrigins) {
			http.Error(w, "MCP origin is not allowed", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedValue(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == value {
			return true
		}
		if strings.HasSuffix(pattern, "*") {
			port := strings.TrimPrefix(value, strings.TrimSuffix(pattern, "*"))
			if port != value && port != "" && onlyASCIIDigits(port) {
				return true
			}
		}
	}
	return false
}

func onlyASCIIDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
