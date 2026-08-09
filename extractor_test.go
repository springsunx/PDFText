package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeRunner struct {
	args   []string
	input  []byte
	result ProcessResult
	err    error
	wait   bool
}

func (r *fakeRunner) Run(ctx context.Context, _ string, args []string, input []byte, _ int64) (ProcessResult, error) {
	r.args = append([]string(nil), args...)
	r.input = append([]byte(nil), input...)
	if r.wait {
		<-ctx.Done()
		return ProcessResult{}, ctx.Err()
	}
	return r.result, r.err
}

func testConfig() Config {
	return Config{
		PDFToTextBinary:   "pdftotext",
		MaxFileBytes:      1024 * 1024,
		MaxOutputBytes:    2 * 1024 * 1024,
		MCPMaxFileBytes:   512 * 1024,
		ExtractionTimeout: time.Second,
	}
}

func TestExtractorBuildsPopplerCommand(t *testing.T) {
	runner := &fakeRunner{result: ProcessResult{Stdout: []byte("你好\n"), OutputBytes: 7}}
	extractor := &PopplerExtractor{config: testConfig(), runner: runner}
	result, err := extractor.Extract(context.Background(), []byte("%PDF-1.7\nbody"), ExtractOptions{
		Layout: true, PageBreaks: false, FirstPage: 2, LastPage: 4, Password: "secret",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-enc", "UTF-8", "-layout", "-nopgbrk", "-f", "2", "-l", "4", "-upw", "secret", "-", "-"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("args = %#v, want %#v", runner.args, want)
	}
	if result.Text != "你好\n" || result.OutputBytes != 7 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestExtractorRejectsInvalidPDF(t *testing.T) {
	extractor := &PopplerExtractor{config: testConfig(), runner: &fakeRunner{}}
	_, err := extractor.Extract(context.Background(), []byte("not a pdf"), ExtractOptions{}, 0)
	var public *PublicError
	if !errors.As(err, &public) || public.Code != "invalid_pdf" {
		t.Fatalf("error = %v, want invalid_pdf", err)
	}
}

func TestExtractorRejectsOversizedPDF(t *testing.T) {
	extractor := &PopplerExtractor{config: testConfig(), runner: &fakeRunner{}}
	_, err := extractor.Extract(context.Background(), append([]byte("%PDF-"), make([]byte, 100)...), ExtractOptions{}, 32)
	var public *PublicError
	if !errors.As(err, &public) || public.Code != "file_too_large" {
		t.Fatalf("error = %v, want file_too_large", err)
	}
}

func TestExtractorMapsTimeout(t *testing.T) {
	config := testConfig()
	config.ExtractionTimeout = 5 * time.Millisecond
	extractor := &PopplerExtractor{config: config, runner: &fakeRunner{wait: true}}
	_, err := extractor.Extract(context.Background(), []byte("%PDF-1.7"), ExtractOptions{}, 0)
	var public *PublicError
	if !errors.As(err, &public) || public.Code != "extraction_timeout" {
		t.Fatalf("error = %v, want extraction_timeout", err)
	}
}

func TestSafePopplerErrorDoesNotLeakStderr(t *testing.T) {
	message := safePopplerError([]byte("internal path /srv/private/report.pdf failed"))
	if message == "" || message == "internal path /srv/private/report.pdf failed" {
		t.Fatalf("unsafe message: %q", message)
	}
}
