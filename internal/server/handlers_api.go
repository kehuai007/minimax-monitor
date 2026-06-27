package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// --- response types ---

type Status struct {
	KeyringConfigured bool      `json:"keyring_configured"`
	LastPoll          time.Time `json:"last_poll"`
	SnapshotCount     int       `json:"snapshot_count"`
	DatabasePath      string    `json:"database_path"`
	DatabaseSizeMB    float64   `json:"database_size_mb"`
	PollInterval      string    `json:"poll_interval"`
}

type ModelLatest struct {
	Model                 string `json:"model"`
	IntervalRemainingPct  *int   `json:"interval_remaining_pct,omitempty"`
	IntervalStatus        *int   `json:"interval_status,omitempty"`
	IntervalEndAt         *int64 `json:"interval_end_at,omitempty"`
	IntervalRemainsMs     *int64 `json:"interval_remains_ms,omitempty"`
	WeeklyRemainingPct    *int   `json:"weekly_remaining_pct,omitempty"`
	WeeklyStatus          *int   `json:"weekly_status,omitempty"`
	WeeklyEndAt           *int64 `json:"weekly_end_at,omitempty"`
	WeeklyRemainsMs       *int64 `json:"weekly_remains_ms,omitempty"`
	FetchedAt             int64  `json:"fetched_at"`
}

type BucketPoint struct {
	T    int64   `json:"t"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Avg  float64 `json:"avg"`
}

type History struct {
	Model  string        `json:"model"`
	Range  string        `json:"range"`
	Bucket int           `json:"bucket_sec"`
	Points []BucketPoint `json:"points"`
}

// rangeBucketSec maps a request range keyword to a default bucket size
// in seconds. If the client supplies an explicit `bucket` query param
// (non-"auto"), that value overrides this map.
var rangeBucketSec = map[string]int{
	"1h":  30,
	"6h":  120,
	"24h": 300,
	"7d":  1800,
	"31d": 7200,
}

// dbSizeMB returns the file size of path in MB. Returns 0 if the file
// does not exist (e.g. fresh install, before first poll).
func dbSizeMB(path string) float64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(fi.Size()) / (1024.0 * 1024.0)
}

// --- handlers ---

func (s *Server) handleStatus(c *gin.Context) {
	st := Status{
		KeyringConfigured: s.Store != nil,
		DatabasePath:      s.DBPath,
		DatabaseSizeMB:    dbSizeMB(s.DBPath),
		PollInterval:      s.PollInterval.String(),
	}
	if s.Stats != nil {
		t, n, _ := s.Stats()
		st.LastPoll = t
		st.SnapshotCount = n
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) handleModels(c *gin.Context) {
	if s.DB == nil {
		c.JSON(http.StatusOK, []ModelLatest{})
		return
	}
	rows, err := s.DB.Latest(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]ModelLatest, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModelLatest{
			Model:                r.ModelName,
			IntervalRemainingPct: r.IntervalRemainingPct,
			IntervalStatus:       r.IntervalStatus,
			IntervalEndAt:        r.IntervalEndAt,
			IntervalRemainsMs:    r.IntervalRemainsMs,
			WeeklyRemainingPct:   r.WeeklyRemainingPct,
			WeeklyStatus:         r.WeeklyStatus,
			WeeklyEndAt:          r.WeeklyEndAt,
			WeeklyRemainsMs:      r.WeeklyRemainsMs,
			FetchedAt:            r.FetchedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

// parseRange resolves a `range` keyword (or a `bucket` override) into
// a bucket size in seconds. Returns the bucket and the to/from window
// in milliseconds (to = now, from = now - windowMs).
func parseRange(rng, bucketParam string, now time.Time) (bucketSec, windowMs int64, err error) {
	if bucketParam != "" && bucketParam != "auto" {
		v, perr := strconv.ParseInt(bucketParam, 10, 64)
		if perr != nil || v <= 0 {
			return 0, 0, fmt.Errorf("invalid bucket %q", bucketParam)
		}
		bucketSec = v
	} else {
		def, ok := rangeBucketSec[rng]
		if !ok {
			return 0, 0, fmt.Errorf("invalid range %q", rng)
		}
		bucketSec = int64(def)
	}
	// derive a window from the chosen bucket: use the next-larger named
	// range if available, else default to 24h.
	ranges := []struct {
		key    string
		window time.Duration
	}{
		{"1h", 1 * time.Hour},
		{"6h", 6 * time.Hour},
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"31d", 31 * 24 * time.Hour},
	}
	// pick window matching the supplied range keyword when present,
	// otherwise default to 24h
	if rng == "" {
		rng = "24h"
	}
	for _, r := range ranges {
		if r.key == rng {
			windowMs = r.window.Milliseconds()
			break
		}
	}
	if windowMs == 0 {
		windowMs = (24 * time.Hour).Milliseconds()
	}
	return bucketSec, windowMs, nil
}

func (s *Server) handleHistory(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	rng := c.Query("range")
	bucketParam := c.Query("bucket")

	now := time.Now()
	bucketSec, windowMs, err := parseRange(rng, bucketParam, now)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fromMs := now.UnixMilli() - windowMs
	toMs := now.UnixMilli()

	if s.DB == nil {
		c.JSON(http.StatusOK, History{
			Model:  modelName,
			Range:  rng,
			Bucket: int(bucketSec),
			Points: []BucketPoint{},
		})
		return
	}
	rows, err := s.DB.History(c.Request.Context(), modelName, fromMs, toMs, bucketSec*1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pts := make([]BucketPoint, 0, len(rows))
	for _, b := range rows {
		pts = append(pts, BucketPoint{T: b.T, Min: b.Min, Max: b.Max, Avg: b.Avg})
	}
	c.JSON(http.StatusOK, History{
		Model:  modelName,
		Range:  rng,
		Bucket: int(bucketSec),
		Points: pts,
	})
}

// silence unused import in builds where context isn't otherwise used.
var _ = context.Background