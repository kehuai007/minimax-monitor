package server

import (
	"net/http"

	"minimax-monitor/internal/storage"
)

type Broadcaster interface {
	Broadcast(snap []storage.Snapshot)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if h, ok := s.Hub.(*WSHub); ok {
		h.ServeWS(w, r)
		return
	}
	http.Error(w, "ws hub not configured", http.StatusServiceUnavailable)
}