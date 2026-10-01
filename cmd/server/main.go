package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/klortekhq/server-emus-ps5/internal/catalog"
	"github.com/klortekhq/server-emus-ps5/internal/config"
	"github.com/klortekhq/server-emus-ps5/internal/httpapi"
	setupwizard "github.com/klortekhq/server-emus-ps5/internal/setup"
)

func main() {
	configPath := flag.String("config", "config.json", "path to JSON configuration")
	rescan := flag.Duration("rescan", 5*time.Minute, "catalog rescan interval; 0 disables automatic rescans")
	setupMode := flag.Bool("setup", false, "interactive bilingual configuration wizard")
	flag.Parse()

	if *setupMode {
		if err := setupwizard.Run(os.Stdin, os.Stdout, *configPath); err != nil {
			log.Fatalf("setup: %v", err)
		}
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cat, err := catalog.New(cfg)
	if err != nil {
		log.Fatalf("catalog: %v", err)
	}

	api := httpapi.NewManaged(cat, cfg, *configPath)
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("listen %s: %v", cfg.Listen, err)
	}
	log.Printf("SERVER-EMUS-PS5 listening on http://%s", listener.Addr())
	for _, stat := range cat.Systems() {
		log.Printf("system=%s files=%d", stat.System, stat.Files)
	}

	stopRescan := make(chan struct{})
	if *rescan > 0 {
		go func() {
			ticker := time.NewTicker(*rescan)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := cat.Rebuild(); err != nil {
						log.Printf("catalog rescan failed: %v", err)
					}
				case <-stopRescan:
					return
				}
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("shutdown signal: %s", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}

	close(stopRescan)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
	}
}
