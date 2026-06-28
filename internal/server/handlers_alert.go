package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"minimax-monitor/internal/notify"
	"minimax-monitor/internal/storage"
)

// allowedWebhookHosts is the allowlist for webhook URL hosts.
var allowedWebhookHosts = map[string]struct{}{
	"open.feishu.cn":     {},
	"open.larksuite.com": {},
}

// maskURL returns a tail-masked version of a webhook URL safe to send to
// the frontend. Returns "" for empty input.
func maskURL(u string) string {
	if u == "" {
		return ""
	}
	if len(u) <= 8 {
		return u
	}
	if len(u) <= 32 {
		return u[:4] + "..." + u[len(u)-4:]
	}
	return u[:24] + "..." + u[len(u)-4:]
}

func (s *Server) handleAlertGet(c *gin.Context) {
	if s.DB == nil || s.AlertConfig == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alert config unavailable"})
		return
	}
	cfg := s.AlertConfig()
	c.JSON(http.StatusOK, gin.H{
		"enabled":   cfg.Enabled,
		"url":       maskURL(cfg.URL),
		"threshold": cfg.Threshold,
	})
}

type alertPutBody struct {
	Enabled   *bool  `json:"enabled"`
	URL       string `json:"url"`
	Threshold *int   `json:"threshold"`
}

func (s *Server) handleAlertPut(c *gin.Context) {
	var body alertPutBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	enabled := false
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	threshold := 80
	if body.Threshold != nil {
		threshold = *body.Threshold
	}
	urlStr := strings.TrimSpace(body.URL)

	if threshold <= 0 || threshold > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "threshold must be in 1..100"})
		return
	}
	if urlStr != "" {
		u, err := url.Parse(urlStr)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "url must be a valid https URL"})
			return
		}
		if _, ok := allowedWebhookHosts[u.Host]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "url host must be open.feishu.cn or open.larksuite.com"})
			return
		}
	}
	if enabled && urlStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url required when enabled"})
		return
	}

	if s.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db unavailable"})
		return
	}

	ctx := c.Request.Context()
	prev, _ := s.DB.GetAlertConfig(ctx)

	if prev.Enabled && !enabled {
		if err := s.DB.ClearAllAlertStates(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	if err := s.DB.SetAlertConfig(ctx, storage.AlertConfig{
		Enabled:   enabled,
		URL:       urlStr,
		Threshold: threshold,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleAlertTest(c *gin.Context) {
	if s.DB == nil || s.AlertConfig == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alert config unavailable"})
		return
	}
	cfg := s.AlertConfig()
	if !cfg.Enabled || cfg.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config_missing"})
		return
	}
	if s.AlertTest == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alert test not wired"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	sentAt, err := s.AlertTest(ctx)
	if err != nil {
		if errors.Is(err, notify.ErrConfigMissing) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "config_missing"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "webhook_failed", "reason": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "sent_at": sentAt})
}