// Package imaging renders resized copies of library images through the imaging
// sidecar and serves the result to the public API.
//
// The CMS stores originals exactly as uploaded, which for photo desk work means
// 5-80MB camera JPEGs. WordPress used to generate smaller copies on upload; the
// CMS did not, so the public site served the originals into 400px cards. This
// package restores that step, off the request path: a reconciler renders the
// library in the background, and an in-memory index lets article responses
// advertise whatever has been rendered so far.
package imaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrDisabled is returned when no sidecar is configured.
var ErrDisabled = errors.New("imaging: no sidecar configured")

// UnprocessableError means the sidecar rejected the image itself: undecodable,
// too many pixels, too large. Sending the same bytes again will fail the same
// way, so the reconciler records it instead of retrying.
type UnprocessableError struct {
	Detail string
}

func (e *UnprocessableError) Error() string {
	return "imaging: sidecar rejected the image: " + e.Detail
}

type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for baseURL. An empty baseURL yields a disabled client,
// which is how a deployment without the sidecar keeps serving originals.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a sidecar is configured.
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

// Recipe is what the sidecar will produce: its name, and the widths it renders,
// ascending. The name changes whenever the output would.
type Recipe struct {
	Name   string `json:"recipe"`
	Format string `json:"format"`
	Widths []int  `json:"widths"`
}

// Recipe asks the sidecar what it is currently configured to render.
func (c *Client) Recipe(ctx context.Context) (Recipe, error) {
	if !c.Enabled() {
		return Recipe{}, ErrDisabled
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return Recipe{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Recipe{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Recipe{}, fmt.Errorf("imaging: sidecar health returned %s", resp.Status)
	}

	var recipe Recipe
	if err := json.NewDecoder(resp.Body).Decode(&recipe); err != nil {
		return Recipe{}, err
	}
	return recipe, nil
}

// Rendered is one encoded rendition and the dimensions it actually came out at.
// The sidecar never enlarges, so Width can be smaller than what was asked for.
type Rendered struct {
	Data   []byte
	Width  int
	Height int
}

// Render resizes src to fit width and returns the encoded result.
func (c *Client) Render(ctx context.Context, src []byte, width int) (Rendered, error) {
	if !c.Enabled() {
		return Rendered{}, ErrDisabled
	}

	endpoint := c.baseURL + "/render?width=" + strconv.Itoa(width)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(src))
	if err != nil {
		return Rendered{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return Rendered{}, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnprocessableEntity,
		resp.StatusCode == http.StatusRequestEntityTooLarge:
		return Rendered{}, &UnprocessableError{Detail: sidecarDetail(resp.Body)}
	default:
		return Rendered{}, fmt.Errorf("imaging: sidecar returned %s: %s", resp.Status, sidecarDetail(resp.Body))
	}

	out := Rendered{}
	if out.Width, err = strconv.Atoi(resp.Header.Get("X-Image-Width")); err != nil || out.Width <= 0 {
		return Rendered{}, fmt.Errorf("imaging: sidecar sent no usable X-Image-Width")
	}
	if out.Height, err = strconv.Atoi(resp.Header.Get("X-Image-Height")); err != nil || out.Height <= 0 {
		return Rendered{}, fmt.Errorf("imaging: sidecar sent no usable X-Image-Height")
	}
	if out.Data, err = io.ReadAll(resp.Body); err != nil {
		return Rendered{}, err
	}
	if len(out.Data) == 0 {
		return Rendered{}, fmt.Errorf("imaging: sidecar returned an empty rendition")
	}
	return out, nil
}

// sidecarDetail pulls FastAPI's {"detail": ...} out of an error body, falling
// back to the raw text.
func sidecarDetail(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 512))
	var decoded struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(raw, &decoded) == nil && decoded.Detail != nil {
		if s, ok := decoded.Detail.(string); ok {
			return s
		}
	}
	return strings.TrimSpace(string(raw))
}
