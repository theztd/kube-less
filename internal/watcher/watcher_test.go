package watcher

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

// newTestWatcher creates a minimal Watcher for unit testing handleEvent
// without touching the filesystem or starting fsnotify.
func newTestWatcher() *Watcher {
	return &Watcher{
		events: make(chan Event, 10),
	}
}

func TestHandleEvent_YAMLExtension_Passes(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "/manifests/nginx.yaml", Op: fsnotify.Create})
	if len(w.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(w.events))
	}
}

func TestHandleEvent_YMLExtension_Passes(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "/manifests/nginx.yml", Op: fsnotify.Write})
	if len(w.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(w.events))
	}
}

func TestHandleEvent_NonYAMLExtension_Ignored(t *testing.T) {
	w := newTestWatcher()
	for _, name := range []string{"file.txt", "file.json", "file.go", "file"} {
		w.handleEvent(fsnotify.Event{Name: name, Op: fsnotify.Create})
	}
	if len(w.events) != 0 {
		t.Errorf("expected 0 events for non-yaml files, got %d", len(w.events))
	}
}

func TestHandleEvent_CreateMapsToAdded(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "deploy.yaml", Op: fsnotify.Create})
	ev := <-w.events
	if ev.Type != EventAdded {
		t.Errorf("expected EventAdded, got %s", ev.Type)
	}
}

func TestHandleEvent_WriteMapsToModified(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "deploy.yaml", Op: fsnotify.Write})
	ev := <-w.events
	if ev.Type != EventModified {
		t.Errorf("expected EventModified, got %s", ev.Type)
	}
}

func TestHandleEvent_RemoveMapsToDeleted(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "deploy.yaml", Op: fsnotify.Remove})
	ev := <-w.events
	if ev.Type != EventDeleted {
		t.Errorf("expected EventDeleted, got %s", ev.Type)
	}
}

func TestHandleEvent_RenameMapsToDeleted(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "deploy.yaml", Op: fsnotify.Rename})
	ev := <-w.events
	if ev.Type != EventDeleted {
		t.Errorf("expected EventDeleted, got %s", ev.Type)
	}
}

func TestHandleEvent_ChmodIgnored(t *testing.T) {
	w := newTestWatcher()
	w.handleEvent(fsnotify.Event{Name: "deploy.yaml", Op: fsnotify.Chmod})
	if len(w.events) != 0 {
		t.Errorf("expected 0 events for Chmod, got %d", len(w.events))
	}
}

func TestHandleEvent_FilePathPreserved(t *testing.T) {
	w := newTestWatcher()
	path := "/etc/kube-less/manifests/nginx.yaml"
	w.handleEvent(fsnotify.Event{Name: path, Op: fsnotify.Create})
	ev := <-w.events
	if ev.FilePath != path {
		t.Errorf("expected FilePath %q, got %q", path, ev.FilePath)
	}
}
