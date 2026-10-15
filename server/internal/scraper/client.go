// Package scraper talks to the headless-browser sidecar that reads an
// Instagram post's image URLs. Like the imaging client, an empty base URL
// yields a disabled client, which is how a deployment without the sidecar
// keeps the rest of the editor working.
package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrDisabled = errors.New("scraper: no sidecar configured")

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a sidecar is configured.
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

// InstagramImages returns the image URLs of one post, in the post's order.
// They are signed CDN links that expire within days: copy them, never store them.
func (c *Client) InstagramImages(ctx context.Context, postURL string) ([]string, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	body, err := json.Marshal(map[string]string{"url": postURL})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/instagram", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("scraper: sidecar returned %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}
	var out struct {
		Images []string `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Images, nil
}
