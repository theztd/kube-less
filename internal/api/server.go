package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"kube-less/internal/scheduler"
)

// Server represents the debug API server.
type Server struct {
	store *scheduler.Store
	port  int
}

// NewServer creates a new API server.
func NewServer(store *scheduler.Store, port int) *Server {
	return &Server{
		store: store,
		port:  port,
	}
}

// Endpoint is a single ready workload entry returned by GET /endpoints.
type Endpoint struct {
	Namespace string  `json:"namespace"`
	Name      string  `json:"name"`
	IP        string  `json:"ip"`
	Ports     []int32 `json:"ports"`
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/endpoints", s.handleEndpoints)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Starting Debug API server on %s", addr)

	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	endpoints := []Endpoint{}
	for _, ws := range s.store.GetWorkloads() {
		if !ws.Ready || ws.SandboxIP == "" {
			continue
		}
		endpoints = append(endpoints, Endpoint{
			Namespace: ws.Namespace,
			Name:      ws.Name,
			IP:        ws.SandboxIP,
			Ports:     manifestPorts(ws),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(endpoints); err != nil {
		log.Printf("Error encoding endpoints response: %v", err)
	}
}

// manifestPorts collects unique container port numbers from the workload manifest.
func manifestPorts(ws *scheduler.WorkloadState) []int32 {
	if ws.Manifest == nil {
		return nil
	}
	seen := make(map[int32]struct{})
	var ports []int32
	for _, c := range ws.Manifest.Spec.Template.Spec.Containers {
		for _, p := range c.Ports {
			if _, ok := seen[p.ContainerPort]; !ok {
				seen[p.ContainerPort] = struct{}{}
				ports = append(ports, p.ContainerPort)
			}
		}
	}
	return ports
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
