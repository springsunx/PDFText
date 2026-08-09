package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeExtractor struct {
	result ExtractResult
	err    error
}

func (f *fakeExtractor) Extract(_ context.Context, _ []byte, _ ExtractOptions, _ int64) (ExtractResult, error) {
	return f.result, f.err
}

func testHTTPHandler(t *testing.T, extractor Extractor) http.Handler {
	t.Helper()
	config := testConfig()
	config.ExtractionTimeout = time.Second
	handler, err := NewHTTPHandler(config, extractor, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHealthAndEmbeddedWebUI(t *testing.T) {
	handler := testHTTPHandler(t, &fakeExtractor{})
	for _, route := range []string{"/", "/healthz", "/api/v1/info", "/openapi.json", "/static/styles.css"} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", route, response.Code)
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("GET %s omitted security headers", route)
		}
	}
}

func TestExtractEndpoint(t *testing.T) {
	wantText := "第一页\n第二页"
	handler := testHTTPHandler(t, &fakeExtractor{result: ExtractResult{
		Text: wantText, InputBytes: 13, OutputBytes: int64(len([]byte(wantText))), ElapsedMS: 7,
	}})
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, err := writer.CreateFormFile("file", "sample.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("%PDF-1.7\nbody"))
	_ = writer.WriteField("layout", "true")
	_ = writer.WriteField("page_breaks", "false")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/extract", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result extractResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Filename != "sample.pdf" || result.Text != wantText || result.CharacterCount != 7 {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestExtractRejectsInvertedRange(t *testing.T) {
	handler := testHTTPHandler(t, &fakeExtractor{})
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, _ := writer.CreateFormFile("file", "sample.pdf")
	_, _ = file.Write([]byte("%PDF-1.7"))
	_ = writer.WriteField("first_page", "5")
	_ = writer.WriteField("last_page", "2")
	_ = writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/extract", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
