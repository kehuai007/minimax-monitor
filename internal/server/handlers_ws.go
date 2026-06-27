package server

import "minimax-monitor/internal/storage"

type Broadcaster interface {
	Broadcast(snap []storage.Snapshot)
}