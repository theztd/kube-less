package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"kube-less/internal/api"
	"kube-less/internal/config"
	"kube-less/internal/engine"
	"kube-less/internal/manifest"
	"kube-less/internal/runtime"
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

	// Initialize Manifest Parser
	parser := manifest.NewParser()

	// Initialize CRI Runtime Client
	criClient := runtime.NewClient(cfg.CRISocketPath)
	defer criClient.Close() // Ensure CRI client is closed on exit

	if err := criClient.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to CRI runtime: %v", err)
	}

	// Initialize Store and Engine
	store := engine.NewStore()
	eng := engine.NewEngine(store, criClient, parser)

	// Initial Sync from CRI
	log.Println("Performing initial sync with CRI...")
	if err := eng.SyncStateFromCRI(ctx); err != nil {
		log.Printf("Warning: Failed to sync initial state from CRI: %v", err)
	}

	// Start Debug API Server
	apiServer := api.NewServer(store, cfg.DebugAPIPort)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("Debug API Server error: %v", err)
		}
	}()

	// Initialize Watcher
	watcher, err := manifest.NewWatcher(cfg.ManifestDirs)
	if err != nil {
		log.Fatalf("Failed to initialize watcher: %v", err)
	}

	// Start Watcher in the background
	go watcher.Start(ctx)

	// Main Application Loop - Processing Events via Engine
	go func() {
		for event := range watcher.Events() {
			eng.OnManifestEvent(event)
		}
	}()

	// Wait for termination signal
	<-sigChan
	log.Println("Shutting down...")
	cancel()
}
