package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

type ExtractOptions struct {
	Layout     bool
	PageBreaks bool
	FirstPage  int
	LastPage   int
	Password   string
}

type ExtractResult struct {
	Text        string `json:"text"`
	InputBytes  int64  `json:"input_bytes"`
	OutputBytes int64  `json:"output_bytes"`
	ElapsedMS   int64  `json:"elapsed_ms"`
}

type PublicError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *PublicError) Error() string { return e.Message }

type ProcessResult struct {
	Stdout      []byte
	Stderr      []byte
	OutputBytes int64
	Overflow    bool
}

type ProcessRunner interface {
	Run(ctx context.Context, binary string, args []string, input []byte, maxOutput int64) (ProcessResult, error)
}

type ExecRunner struct{}

type boundedWriter struct {
	buffer   bytes.Buffer
	max      int64
	total    int64
	overflow bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	remaining := w.max - int64(w.buffer.Len())
	if remaining > 0 {
		toWrite := int64(len(p))
		if toWrite > remaining {
			toWrite = remaining
		}
		_, _ = w.buffer.Write(p[:toWrite])
	}
	if w.total > w.max {
		w.overflow = true
	}
	return len(p), nil
}

func (ExecRunner) Run(ctx context.Context, binary string, args []string, input []byte, maxOutput int64) (ProcessResult, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = bytes.NewReader(input)
	stdout := &boundedWriter{max: maxOutput}
	stderr := &boundedWriter{max: 4096}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return ProcessResult{
		Stdout:      stdout.buffer.Bytes(),
		Stderr:      stderr.buffer.Bytes(),
		OutputBytes: stdout.total,
		Overflow:    stdout.overflow,
	}, err
}

type Extractor interface {
	Extract(ctx context.Context, pdf []byte, options ExtractOptions, maxFileBytes int64) (ExtractResult, error)
}

type PopplerExtractor struct {
	config Config
	runner ProcessRunner
}

func NewPopplerExtractor(config Config) *PopplerExtractor {
	return &PopplerExtractor{config: config, runner: ExecRunner{}}
}

func (e *PopplerExtractor) Extract(ctx context.Context, pdf []byte, options ExtractOptions, maxFileBytes int64) (ExtractResult, error) {
	if maxFileBytes <= 0 {
		maxFileBytes = e.config.MaxFileBytes
	}
	if int64(len(pdf)) > maxFileBytes {
		return ExtractResult{}, fileTooLarge(maxFileBytes)
	}
	probeLen := min(len(pdf), 1024)
	if len(pdf) == 0 || !bytes.Contains(pdf[:probeLen], []byte("%PDF-")) {
		return ExtractResult{}, &PublicError{Code: "invalid_pdf", Message: "The uploaded file is not a valid PDF.", HTTPStatus: 400}
	}
	if options.FirstPage < 0 || options.LastPage < 0 {
		return ExtractResult{}, invalidOptions("page numbers must be at least 1")
	}
	if options.FirstPage > 0 && options.LastPage > 0 && options.FirstPage > options.LastPage {
		return ExtractResult{}, invalidOptions("first_page cannot be greater than last_page")
	}

	args := []string{"-enc", "UTF-8"}
	if options.Layout {
		args = append(args, "-layout")
	}
	if !options.PageBreaks {
		args = append(args, "-nopgbrk")
	}
	if options.FirstPage > 0 {
		args = append(args, "-f", fmt.Sprint(options.FirstPage))
	}
	if options.LastPage > 0 {
		args = append(args, "-l", fmt.Sprint(options.LastPage))
	}
	if options.Password != "" {
		args = append(args, "-upw", options.Password)
	}
	args = append(args, "-", "-")

	processCtx, cancel := context.WithTimeout(ctx, e.config.ExtractionTimeout)
	defer cancel()
	started := time.Now()
	result, err := e.runner.Run(processCtx, e.config.PDFToTextBinary, args, pdf, e.config.MaxOutputBytes)
	elapsed := time.Since(started).Milliseconds()
	if errors.Is(processCtx.Err(), context.DeadlineExceeded) {
		return ExtractResult{}, &PublicError{
			Code:       "extraction_timeout",
			Message:    fmt.Sprintf("PDF extraction exceeded the %s timeout.", e.config.ExtractionTimeout),
			HTTPStatus: 504,
		}
	}
	if result.Overflow {
		return ExtractResult{}, &PublicError{
			Code:       "output_too_large",
			Message:    fmt.Sprintf("The extracted text exceeds the %d MB output limit.", e.config.MaxOutputBytes/(1024*1024)),
			HTTPStatus: 422,
		}
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ExtractResult{}, &PublicError{Code: "extractor_unavailable", Message: "The pdftotext executable is not available on this server.", HTTPStatus: 503}
		}
		return ExtractResult{}, &PublicError{Code: "extraction_failed", Message: safePopplerError(result.Stderr), HTTPStatus: 422}
	}

	text := string(result.Stdout)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	return ExtractResult{
		Text:        text,
		InputBytes:  int64(len(pdf)),
		OutputBytes: result.OutputBytes,
		ElapsedMS:   elapsed,
	}, nil
}

func fileTooLarge(limit int64) *PublicError {
	return &PublicError{
		Code:       "file_too_large",
		Message:    fmt.Sprintf("The PDF exceeds the %d MB size limit.", max(int64(1), limit/(1024*1024))),
		HTTPStatus: 413,
	}
}

func invalidOptions(message string) *PublicError {
	return &PublicError{Code: "invalid_options", Message: message, HTTPStatus: 422}
}

func safePopplerError(stderr []byte) string {
	lower := strings.ToLower(string(stderr))
	switch {
	case strings.Contains(lower, "password"), strings.Contains(lower, "encrypted"):
		return "The PDF is encrypted or the supplied password is incorrect."
	case strings.Contains(lower, "syntax error"), strings.Contains(lower, "damaged"):
		return "The PDF appears to be damaged or unsupported."
	default:
		return "pdftotext could not extract text from this PDF."
	}
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fileTooLarge(limit)
	}
	return data, nil
}
