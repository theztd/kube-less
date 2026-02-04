package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"kube-less/internal/engine"
)

// Server represents the debug API server.
type Server struct {
	store *engine.Store
	port  int
}

// NewServer creates a new API server.
func NewServer(store *engine.Store, port int) *Server {
	return &Server{
		store: store,
		port:  port,
	}
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Starting Debug API server on %s", addr)
	
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workloads := s.store.GetWorkloads()
	
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(workloads); err != nil {
		log.Printf("Error encoding status response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
