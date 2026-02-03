package manifest

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// EventType represents the type of file system event.
type EventType string

const (
	EventAdded    EventType = "ADDED"
	EventModified EventType = "MODIFIED"
	EventDeleted  EventType = "DELETED"
)

// Event represents a change in the manifest directory.
type Event struct {
	Type     EventType
	FilePath string
}

// Watcher monitors directories for manifest changes.
type Watcher struct {
	watcher *fsnotify.Watcher
	dirs    []string
	events  chan Event
}

// NewWatcher creates a new Watcher for the specified directories.
func NewWatcher(dirs []string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	w := &Watcher{
		watcher: fsWatcher,
		dirs:    dirs,
		events:  make(chan Event, 100), // Buffered channel
	}

	for _, dir := range dirs {
		// Ensure directory exists
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			log.Printf("Warning: Manifest directory does not exist, skipping: %s", dir)
			continue
		}

		if err := fsWatcher.Add(dir); err != nil {
			// Clean up if adding fails
			fsWatcher.Close()
			return nil, fmt.Errorf("failed to watch directory %s: %w", dir, err)
		}
		log.Printf("Watching directory: %s", dir)
	}

	return w, nil
}

// Events returns the channel to receive manifest events.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Start begins listening for file system events. It runs until the context is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	defer w.watcher.Close()
	defer close(w.events)

	log.Println("Manifest watcher started")

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping manifest watcher")
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// handleEvent processes the raw fsnotify event and sends it to the Events channel if relevant.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Ignore temporary files or non-yaml files
	ext := strings.ToLower(filepath.Ext(event.Name))
	if ext != ".yaml" && ext != ".yml" {
		return
	}

	var eventType EventType

	// Determine generic event type
	if event.Has(fsnotify.Create) {
		eventType = EventAdded
	} else if event.Has(fsnotify.Write) {
		eventType = EventModified
	} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		eventType = EventDeleted
	} else {
		return // Ignore Chmod and others
	}

	w.events <- Event{
		Type:     eventType,
		FilePath: event.Name,
	}
}
