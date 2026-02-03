package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"kube-less/internal/config"
	"kube-less/internal/manifest"
)

func main() {
	configPath := flag.String("config", "", "Path to the configuration YAML file")
	flag.Parse()

	if *configPath == "" {
		fmt.Println("Error: -config flag is required")
		flag.Usage()
		os.Exit(1)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Starting kube-less with config: %+v\n", cfg)

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize Watcher
	watcher, err := manifest.NewWatcher(cfg.ManifestDirs)
	if err != nil {
		log.Fatalf("Failed to initialize watcher: %v", err)
	}

	// Start Watcher in the background
	go watcher.Start(ctx)

	// Main Application Loop
	// TODO: Move this loop to an Engine/Controller later
	go func() {
		for event := range watcher.Events() {
			log.Printf("Manifest Event: Type=%s, File=%s", event.Type, event.FilePath)
		}
	}()

	// Wait for termination signal
	<-sigChan
	log.Println("Shutting down...")
	cancel()
	// Allow some time for cleanup if needed
	// time.Sleep(1 * time.Second)
}
