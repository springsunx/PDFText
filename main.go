package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	config, err := LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	extractor := NewPopplerExtractor(config)

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(); err != nil {
			log.Fatal(err)
		}
		return
	}

	mcpServer := NewMCPServer(config, extractor)
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := mcpServer.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "usage: %s [serve|mcp|healthcheck]\n", os.Args[0])
		os.Exit(2)
	}

	handler, err := NewHTTPHandler(config, extractor, NewMCPHTTPHandler(config, mcpServer))
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              config.Addr,
		Handler:           handler,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
	}()

	log.Printf("PDFText %s listening on %s", version, config.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func runHealthcheck() error {
	url := envString("PDFTEXT_HEALTHCHECK_URL", "http://127.0.0.1:8000/readyz")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned %s", response.Status)
	}
	return nil
}
