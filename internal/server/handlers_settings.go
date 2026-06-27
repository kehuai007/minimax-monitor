package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type keyValidator interface {
	Fetch(ctx context.Context, key string) (interface{}, error)
}

// We use a concrete validator to keep the server package free of apiclient import
// (it also keeps test fakes simple).
type ValidatorFunc func(ctx context.Context, key string) error

func (s *Server) handleSettingsPost(c *gin.Context) {
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.APIKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key required"})
		return
	}
	if s.Validator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "validator not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	if err := s.Validator(ctx, body.APIKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.Store.Set(body.APIKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.OnKeyChange != nil {
		s.OnKeyChange()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleSettingsDelete(c *gin.Context) {
	if err := s.Store.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.OnKeyChange != nil {
		s.OnKeyChange()
	}
	c.Status(http.StatusNoContent)
}