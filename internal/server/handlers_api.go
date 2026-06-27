package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// --- response types ---

type Status struct {
	KeyringConfigured bool       `json:"keyring_configured"`
	LastFetchAt       *time.Time `json:"last_fetch_at"`
	ConsecErrors      int        `json:"consec_errors"`
	LastError         string     `json:"last_error"`
	PollInterval      string     `json:"poll_interval"`
	DBSizeMB          float64    `json:"db_size_mb"`
}

type ModelLatest struct {
	ModelName            string `json:"model_name"`
	IntervalRemainingPct *int   `json:"interval_remaining_pct"`
	WeeklyRemainingPct   *int   `json:"weekly_remaining_pct"`
	IntervalRemainsMs    *int64 `json:"interval_remains_ms"`
	FetchedAt            int64  `json:"fetched_at"`
}

type BucketPoint struct {
	T   int64   `json:"t"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	Avg float64 `json:"avg"`
}

type History struct {
	Model    string        `json:"model"`
	Range    string        `json:"range"`
	BucketMs int64         `json:"bucket_ms"`
	Points   []BucketPoint `json:"points"`
}

// rangeBucketMs maps a request range keyword to a default bucket size
// in milliseconds. If the client supplies an explicit `bucket` query param
// (non-"auto"), that value overrides this map.
var rangeBucketMs = map[string]int64{
	"1h":  30 * 1000,
	"6h":  120 * 1000,
	"24h": 300 * 1000,
	"7d":  1800 * 1000,
	"31d": 7200 * 1000,
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
		DBSizeMB: dbSizeMB(s.DBPath),
	}
	if s.Store != nil {
		if _, err := s.Store.Get(); err == nil {
			st.KeyringConfigured = true
		}
	}
	if s.PollInterval > 0 {
		st.PollInterval = s.PollInterval.String()
	}
	if s.Stats != nil {
		t, n, lastErr := s.Stats()
		if !t.IsZero() {
			tt := t
			st.LastFetchAt = &tt
		}
		_ = n // reserved for future use
		st.LastError = lastErr
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
			ModelName:            r.ModelName,
			IntervalRemainingPct: r.IntervalRemainingPct,
			WeeklyRemainingPct:   r.WeeklyRemainingPct,
			IntervalRemainsMs:    r.IntervalRemainsMs,
			FetchedAt:            r.FetchedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

// parseRange resolves a `range` keyword into a time.Duration window.
// Examples: "1h" -> 1h, "7d" -> 168h. Defaults to 24h on any error.
func parseRange(s string) time.Duration {
	if len(s) < 2 {
		return 24 * time.Hour
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 24 * time.Hour
	}
	unit := s[len(s)-1]
	switch unit {
	case 'h':
		return time.Duration(n) * time.Hour
	case 'd':
		return time.Duration(n) * 24 * time.Hour
	}
	return 24 * time.Hour
}

// resolveBucket returns the bucket size in milliseconds for the given
// range keyword, honoring a client-supplied `bucket` override (in seconds).
func resolveBucket(rng, bucketParam string) (int64, error) {
	if bucketParam != "" && bucketParam != "auto" {
		v, err := strconv.ParseInt(bucketParam, 10, 64)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("invalid bucket %q", bucketParam)
		}
		return v * 1000, nil
	}
	def, ok := rangeBucketMs[rng]
	if !ok {
		return 0, fmt.Errorf("invalid range %q", rng)
	}
	return def, nil
}

func (s *Server) handleHistory(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	rng := c.Query("range")
	bucketParam := c.Query("bucket")

	bucketMs, err := resolveBucket(rng, bucketParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Derive the from-window: prefer a Go duration string ("24h"),
	// otherwise parse the range keyword ("7d", "1h", ...).
	now := time.Now()
	dur, _ := time.ParseDuration(rng)
	if dur == 0 {
		dur = parseRange(rng)
	}
	fromMs := now.Add(-dur).UnixMilli()
	toMs := now.UnixMilli()

	if s.DB == nil {
		c.JSON(http.StatusOK, History{
			Model:    modelName,
			Range:    rng,
			BucketMs: bucketMs,
			Points:   []BucketPoint{},
		})
		return
	}
	rows, err := s.DB.History(c.Request.Context(), modelName, fromMs, toMs, bucketMs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pts := make([]BucketPoint, 0, len(rows))
	for _, b := range rows {
		pts = append(pts, BucketPoint{T: b.T, Min: b.Min, Max: b.Max, Avg: b.Avg})
	}
	c.JSON(http.StatusOK, History{
		Model:    modelName,
		Range:    rng,
		BucketMs: bucketMs,
		Points:   pts,
	})
}