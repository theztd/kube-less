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
	"kube-less/internal/cri"
	"kube-less/internal/parser"
	"kube-less/internal/scheduler"
	"kube-less/internal/watcher"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize Parser
	p := parser.NewParser()

	// Initialize CRI Client
	criClient := cri.NewClient(cfg.CRISocketPath)
	defer criClient.Close()

	if err := criClient.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to CRI runtime: %v", err)
	}

	// Initialize Store and Scheduler
	store := scheduler.NewStore()
	sched := scheduler.NewScheduler(store, criClient, p)

	// Initial sync from CRI
	log.Println("Performing initial sync with CRI...")
	if err := sched.SyncStateFromCRI(ctx); err != nil {
		log.Printf("Warning: Failed to sync initial state from CRI: %v", err)
	}

	// Start Debug API Server
	apiServer := api.NewServer(store, cfg.DebugAPIPort)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("Debug API Server error: %v", err)
		}
	}()

	// Initialize and start Watcher
	w, err := watcher.NewWatcher(cfg.ManifestDirs)
	if err != nil {
		log.Fatalf("Failed to initialize watcher: %v", err)
	}

	go w.Start(ctx)

	// Process watcher events via Scheduler
	go func() {
		for event := range w.Events() {
			sched.OnManifestEvent(event)
		}
	}()

	<-sigChan
	log.Println("Shutting down...")
	cancel()
}
