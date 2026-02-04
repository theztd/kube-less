package engine

import (
	"context"
	"log"
	"os"

	appsv1 "k8s.io/api/apps/v1"

	"kube-less/internal/manifest"
	ourruntime "kube-less/internal/runtime" // alias for our internal runtime package
)

// Engine orchestrates the lifecycle of pods based on manifests.
type Engine struct {
	store  *Store
	client *ourruntime.Client
	parser *manifest.Parser
}

// NewEngine creates a new Engine instance.
func NewEngine(store *Store, client *ourruntime.Client, parser *manifest.Parser) *Engine {
	return &Engine{
		store:  store,
		client: client,
		parser: parser,
	}
}

// StartReconciliationLoop starts the main reconciliation loop in a background goroutine.
func (e *Engine) StartReconciliationLoop(ctx context.Context, interval string) {
	// TODO: Parse duration correctly, for now assuming it's valid or handling error
	// In a real app we'd parse time.Duration(interval)
	
	go func() {
		// ticker := time.NewTicker(5 * time.Second) // Temporary hardcoded
		// defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			// case <-ticker.C:
				// e.Reconcile(ctx)
			}
		}
	}()
}

// OnManifestEvent handles file system events from the watcher.
func (e *Engine) OnManifestEvent(event manifest.Event) {
	log.Printf("Engine received event: Type=%s, File=%s", event.Type, event.FilePath)

	if event.Type == manifest.EventAdded || event.Type == manifest.EventModified {
		e.handleUpdate(event.FilePath)
	} else if event.Type == manifest.EventDeleted { // Corrected from EventRemoved
		e.handleRemove(event.FilePath)
	}
}

func (e *Engine) handleUpdate(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("Error reading manifest file %s: %v", filePath, err)
		return
	}

	objects, err := e.parser.Parse(data)
	if err != nil {
		log.Printf("Error parsing manifest file %s: %v", filePath, err)
		return
	}

	for _, obj := range objects {
		switch o := obj.(type) {
		case *appsv1.Deployment:
			log.Printf("Engine processing Deployment: %s/%s", o.Namespace, o.Name)
			e.store.UpdateWorkload(o.Namespace, o.Name, o)
		default:
			// Ignore other types for now in the store (ConfigMaps/Secrets handled separately later)
		}
	}
}

func (e *Engine) handleRemove(filePath string) {
	// TODO: Handling removals is tricky because a file can contain multiple manifests.
	// We would need to know what was in the file *before* it was deleted.
	// For MVP, we might need a mapping of FilePath -> []WorkloadKeys.
	log.Printf("Warning: File removal handling not yet implemented for %s", filePath)
}

// SyncStateFromCRI queries the runtime to update the internal state of running pods.
func (e *Engine) SyncStateFromCRI(ctx context.Context) error {
	sandboxes, err := e.client.ListPodSandbox(ctx)
	if err != nil {
		return err
	}

	for _, sb := range sandboxes {
		name := sb.Metadata.Name
		namespace := sb.Metadata.Namespace
		
		// Update the store with what we found running
		e.store.UpdatePodStatus(namespace, name, sb.Id, TranslateCRIState(sb.State))
	}
	return nil
}
