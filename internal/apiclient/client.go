package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"minimax-monitor/internal/model"
)

type Client struct {
	url     string
	cli     *http.Client
	backoff func(attempt int) time.Duration
}

func New(url string) *Client {
	return &Client{
		url:     url,
		cli:     &http.Client{Timeout: 10 * time.Second},
		backoff: defaultBackoff,
	}
}

func defaultBackoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second // 1s, 2s, 4s
}

func (c *Client) Fetch(ctx context.Context, apiKey string) (*model.APIResponse, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := c.cli.Do(req)
		if err != nil {
			lastErr = err
			if i < 2 {
				time.Sleep(c.backoff(i))
				continue
			}
			break
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
			resp.Body.Close()
			if i < 2 {
				time.Sleep(c.backoff(i))
				continue
			}
			break
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, fmt.Errorf("upstream status %d", resp.StatusCode)
		}
		var out model.APIResponse
		dec := json.NewDecoder(resp.Body)
		decErr := dec.Decode(&out)
		resp.Body.Close()
		if decErr != nil {
			return nil, decErr
		}
		if out.BaseResp.StatusCode != 0 {
			return nil, fmt.Errorf("api error: %s", out.BaseResp.StatusMsg)
		}
		return &out, nil
	}
	return nil, fmt.Errorf("fetch failed after retries: %w", lastErr)
}
