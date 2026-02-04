package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"kube-less/internal/config"
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

	// Example: List Pod Sandboxes
	sandboxes, err := criClient.ListPodSandbox(ctx)
	if err != nil {
		log.Printf("Warning: Failed to list pod sandboxes: %v", err)
	} else {
		log.Printf("Found %d pod sandboxes:", len(sandboxes))
		for _, sb := range sandboxes {
			log.Printf("- PodSandbox ID: %s, Name: %s/%s", sb.GetId(), sb.GetMetadata().GetNamespace(), sb.GetMetadata().GetName())
		}
	}

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

			// For ADDED/MODIFIED events, try to parse the file
			if event.Type == manifest.EventAdded || event.Type == manifest.EventModified {
				data, err := os.ReadFile(event.FilePath)
				if err != nil {
					log.Printf("Error reading manifest file %s: %v", event.FilePath, err)
					continue
				}

				objects, err := parser.Parse(data)
				if err != nil {
					log.Printf("Error parsing manifest file %s: %v", event.FilePath, err)
					continue
				}

				for _, obj := range objects {
					// Use a type switch to inspect the object type and print relevant info
					switch o := obj.(type) {
					case *appsv1.Deployment:
						log.Printf("Parsed Deployment: %s/%s, Replicas: %d", o.Namespace, o.Name, *o.Spec.Replicas)
					case *corev1.ConfigMap:
						log.Printf("Parsed ConfigMap: %s/%s, Data Keys: %d", o.Namespace, o.Name, len(o.Data))
					case *corev1.Secret:
						log.Printf("Parsed Secret: %s/%s, Data Keys: %d", o.Namespace, o.Name, len(o.Data))
					default:
						// This should not happen often due to GVK filtering in parser
						log.Printf("Parsed unknown object type: %T", o)
					}
				}
			}
		}
	}()

	// Wait for termination signal
	<-sigChan
	log.Println("Shutting down...")
	cancel()
	// Allow some time for cleanup if needed
	// time.Sleep(1 * time.Second)
}
