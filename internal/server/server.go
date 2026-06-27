package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"minimax-monitor/internal/storage"
)

// keyringStore is the subset of the keyring store the server depends on.
// It is unexported because the real implementation lives in internal/keyring.
type keyringStore interface {
	Get() (string, error)
	Set(string) error
	Delete() error
}

// Server wires HTTP routes to storage and the WebSocket hub.
type Server struct {
	Engine       *gin.Engine
	DB           *storage.DB
	Store        keyringStore
	Hub          Broadcaster
	DBPath       string
	PollInterval time.Duration
	Stats        func() (time.Time, int, string)
	Validator    ValidatorFunc
	OnKeyChange  func()
}

// New constructs a Server. db and store may be nil at construction time;
// routes that dereference them are only invoked once they are wired in
// later tasks.
func New(db *storage.DB, store keyringStore) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		Engine: gin.New(),
		DB:     db,
		Store:  store,
	}
	s.Engine.Use(gin.Recovery())
	s.routes()
	return s
}

func (s *Server) routes() {
	s.Engine.GET("/api/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	s.Engine.GET("/api/status", s.handleStatus)
	s.Engine.GET("/api/models", s.handleModels)
	s.Engine.GET("/api/history", s.handleHistory)
	s.Engine.POST("/api/settings/key", s.handleSettingsPost)
	s.Engine.DELETE("/api/settings/key", s.handleSettingsDelete)
}

// Run starts the HTTP server on addr.
func (s *Server) Run(addr string) error {
	return s.Engine.Run(addr)
}