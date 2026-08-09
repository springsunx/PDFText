package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed src/pdftext/templates/index.html src/pdftext/static/* src/pdftext/openapi.json
var webAssets embed.FS

type API struct {
	config    Config
	extractor Extractor
	mcp       http.Handler
}

type extractResponse struct {
	Filename       string `json:"filename"`
	Text           string `json:"text"`
	CharacterCount int    `json:"character_count"`
	InputBytes     int64  `json:"input_bytes"`
	OutputBytes    int64  `json:"output_bytes"`
	ElapsedMS      int64  `json:"elapsed_ms"`
}

func NewHTTPHandler(config Config, extractor Extractor, mcpHandler http.Handler) (http.Handler, error) {
	api := &API{config: config, extractor: extractor, mcp: mcpHandler}
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(webAssets, "src/pdftext/static")
	if err != nil {
		return nil, fmt.Errorf("load embedded static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /{$}", api.index)
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /readyz", api.readiness)
	mux.HandleFunc("GET /api/v1/info", api.info)
	mux.HandleFunc("GET /openapi.json", api.openAPI)
	mux.HandleFunc("POST /api/v1/extract", api.extract)
	if mcpHandler != nil {
		mux.Handle("/mcp", mcpHandler)
	}

	return securityHeaders(mux), nil
}

func (a *API) openAPI(w http.ResponseWriter, _ *http.Request) {
	content, err := webAssets.ReadFile("src/pdftext/openapi.json")
	if err != nil {
		http.Error(w, "OpenAPI definition unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(content)
}

func (a *API) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	content, err := webAssets.ReadFile("src/pdftext/templates/index.html")
	if err != nil {
		http.Error(w, "WebUI unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(content)
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": version})
}

func (a *API) readiness(w http.ResponseWriter, _ *http.Request) {
	binary, err := exec.LookPath(a.config.PDFToTextBinary)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "pdftotext": a.config.PDFToTextBinary})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "pdftotext": binary})
}

func (a *API) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":               "PDFText",
		"version":            version,
		"max_file_size_mb":   a.config.MaxFileBytes / (1024 * 1024),
		"max_output_size_mb": a.config.MaxOutputBytes / (1024 * 1024),
		"timeout_seconds":    int(a.config.ExtractionTimeout.Seconds()),
		"single_binary":      true,
		"mcp": map[string]any{
			"streamable_http": "/mcp",
			"stdio_command":   "pdftext mcp",
			"base64_limit_mb": a.config.MCPMaxFileBytes / (1024 * 1024),
		},
	})
}

func (a *API) extract(w http.ResponseWriter, r *http.Request) {
	maxBody := a.config.MaxFileBytes + 2*1024*1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(min(a.config.MaxFileBytes, 8*1024*1024)); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writePublicError(w, fileTooLarge(a.config.MaxFileBytes))
			return
		}
		writePublicError(w, &PublicError{Code: "invalid_request", Message: "Expected a multipart form containing a PDF file.", HTTPStatus: 400})
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writePublicError(w, &PublicError{Code: "missing_file", Message: "The file field is required.", HTTPStatus: 400})
		return
	}
	defer file.Close()
	pdf, err := readLimited(file, a.config.MaxFileBytes)
	if err != nil {
		writeError(w, err)
		return
	}
	options, err := parseFormOptions(r.MultipartForm)
	if err != nil {
		writeError(w, err)
		return
	}

	result, err := a.extractor.Extract(r.Context(), pdf, options, a.config.MaxFileBytes)
	if err != nil {
		writeError(w, err)
		return
	}
	filename := safeFilename(header)
	writeJSON(w, http.StatusOK, extractResponse{
		Filename:       filename,
		Text:           result.Text,
		CharacterCount: utf8.RuneCountInString(result.Text),
		InputBytes:     result.InputBytes,
		OutputBytes:    result.OutputBytes,
		ElapsedMS:      result.ElapsedMS,
	})
}

func safeFilename(header *multipart.FileHeader) string {
	name := "document.pdf"
	if header != nil && header.Filename != "" {
		name = path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	}
	if name == "." || name == "/" || name == "" {
		return "document.pdf"
	}
	return name
}

func parseFormOptions(form *multipart.Form) (ExtractOptions, error) {
	options := ExtractOptions{Layout: true, PageBreaks: true}
	var err error
	if value := firstValue(form, "layout"); value != "" {
		options.Layout, err = strconv.ParseBool(value)
		if err != nil {
			return ExtractOptions{}, invalidOptions("layout must be true or false")
		}
	}
	if value := firstValue(form, "page_breaks"); value != "" {
		options.PageBreaks, err = strconv.ParseBool(value)
		if err != nil {
			return ExtractOptions{}, invalidOptions("page_breaks must be true or false")
		}
	}
	if options.FirstPage, err = parseOptionalPage(firstValue(form, "first_page"), "first_page"); err != nil {
		return ExtractOptions{}, err
	}
	if options.LastPage, err = parseOptionalPage(firstValue(form, "last_page"), "last_page"); err != nil {
		return ExtractOptions{}, err
	}
	options.Password = firstValue(form, "password")
	if len(options.Password) > 1024 {
		return ExtractOptions{}, invalidOptions("password is too long")
	}
	if options.FirstPage > 0 && options.LastPage > 0 && options.FirstPage > options.LastPage {
		return ExtractOptions{}, invalidOptions("first_page cannot be greater than last_page")
	}
	return options, nil
}

func firstValue(form *multipart.Form, name string) string {
	if form == nil || len(form.Value[name]) == 0 {
		return ""
	}
	return form.Value[name][0]
}

func parseOptionalPage(raw, field string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, invalidOptions(field + " must be an integer of at least 1")
	}
	return value, nil
}

func writeError(w http.ResponseWriter, err error) {
	var public *PublicError
	if errors.As(err, &public) {
		writePublicError(w, public)
		return
	}
	writePublicError(w, &PublicError{Code: "internal_error", Message: "An unexpected error occurred.", HTTPStatus: 500})
}

func writePublicError(w http.ResponseWriter, err *PublicError) {
	writeJSON(w, err.HTTPStatus, map[string]any{"error": map[string]string{"code": err.Code, "message": err.Message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
