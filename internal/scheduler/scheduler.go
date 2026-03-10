package scheduler

import (
	"context"
	"log"
	"os"

	appsv1 "k8s.io/api/apps/v1"

	"kube-less/internal/cri"
	"kube-less/internal/parser"
	"kube-less/internal/watcher"
)

// Scheduler orchestrates the lifecycle of pods based on manifests.
type Scheduler struct {
	store  *Store
	client *cri.Client
	parser *parser.Parser
}

// NewScheduler creates a new Scheduler instance.
func NewScheduler(store *Store, client *cri.Client, p *parser.Parser) *Scheduler {
	return &Scheduler{
		store:  store,
		client: client,
		parser: p,
	}
}

// StartReconciliationLoop starts the main reconciliation loop in a background goroutine.
func (s *Scheduler) StartReconciliationLoop(ctx context.Context, interval string) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			}
		}
	}()
}

// OnManifestEvent handles file system events from the watcher.
func (s *Scheduler) OnManifestEvent(event watcher.Event) {
	log.Printf("Scheduler received event: Type=%s, File=%s", event.Type, event.FilePath)

	if event.Type == watcher.EventAdded || event.Type == watcher.EventModified {
		s.handleUpdate(event.FilePath)
	} else if event.Type == watcher.EventDeleted {
		s.handleRemove(event.FilePath)
	}
}

func (s *Scheduler) handleUpdate(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("Error reading manifest file %s: %v", filePath, err)
		return
	}

	objects, err := s.parser.Parse(data)
	if err != nil {
		log.Printf("Error parsing manifest file %s: %v", filePath, err)
		return
	}

	for _, obj := range objects {
		switch o := obj.(type) {
		case *appsv1.Deployment:
			log.Printf("Scheduler processing Deployment: %s/%s", o.Namespace, o.Name)
			s.store.UpdateWorkload(o.Namespace, o.Name, o)
		}
	}
}

func (s *Scheduler) handleRemove(filePath string) {
	// TODO: requires fileToWorkloads mapping (Milestone B)
	log.Printf("Warning: file removal handling not yet implemented for %s", filePath)
}

// SyncStateFromCRI queries the runtime to populate initial state.
func (s *Scheduler) SyncStateFromCRI(ctx context.Context) error {
	sandboxes, err := s.client.ListPodSandbox(ctx)
	if err != nil {
		return err
	}

	for _, sb := range sandboxes {
		s.store.UpdatePodStatus(sb.Metadata.Namespace, sb.Metadata.Name, sb.Id, TranslateCRIState(sb.State))
	}
	return nil
}
