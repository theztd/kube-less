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
	"kube-less/internal/network"
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

	// Validate CNI presence before doing anything else.
	if err := network.ValidateCNI(cfg.Network.CNIConfDir, cfg.Network.CNIBinDir); err != nil {
		log.Fatalf("CNI validation failed: %v", err)
	}
	log.Printf("CNI validation OK (conf=%s, bin=%s)", cfg.Network.CNIConfDir, cfg.Network.CNIBinDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize Parser and CRI Client
	p := parser.NewParser()
	criClient := cri.NewClient(cfg.CRISocketPath)
	defer criClient.Close()

	if err := criClient.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to CRI runtime: %v", err)
	}

	// Initialize Store and Scheduler
	store := scheduler.NewStore()
	sched := scheduler.NewScheduler(store, criClient, p)

	// ── Startup sequence (spec §2.2) ──────────────────────────────────────────
	// 1. Load all manifests from disk → populate desired state in Store
	log.Println("Loading manifests from disk...")
	if err := sched.LoadManifests(cfg.ManifestDirs); err != nil {
		log.Printf("Warning: LoadManifests errors: %v", err)
	}
	log.Printf("Loaded %d workload(s) from manifest directories", len(store.GetWorkloads()))

	// 2. Reconcile desired state vs. running CRI state
	log.Println("Performing initial sync with CRI...")
	if err := sched.SyncStateFromCRI(ctx); err != nil {
		log.Printf("Warning: SyncStateFromCRI error: %v", err)
	}

	// 3. Start periodic reconciliation loop BEFORE the watcher so the first
	//    tick can create any workloads that weren't running after SyncStateFromCRI.
	sched.StartReconciliationLoop(ctx, cfg.SyncInterval)
	log.Printf("Reconciliation loop started (interval=%s)", cfg.SyncInterval)

	// ── Debug API ──────────────────────────────────────────────────────────────
	apiServer := api.NewServer(store, cfg.DebugAPIPort)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("Debug API Server error: %v", err)
		}
	}()

	// 4. Start the file-system watcher last – now that desired state is already loaded
	w, err := watcher.NewWatcher(cfg.ManifestDirs)
	if err != nil {
		log.Fatalf("Failed to initialize watcher: %v", err)
	}
	go w.Start(ctx)

	go func() {
		for event := range w.Events() {
			sched.OnManifestEvent(event)
		}
	}()

	log.Println("kube-less is running. Press Ctrl+C to stop.")
	<-sigChan
	log.Println("Shutting down...")
	cancel()
}
