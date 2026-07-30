package akismet

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultEndpoint  = "https://rest.akismet.com/1.1/comment-check"
	defaultUserAgent = "TriangleCMS/1.0 | Akismet/1.1"
	defaultTimeout   = 3 * time.Second
)

type Checker interface {
	CheckComment(ctx context.Context, comment Comment) (bool, error)
}

type Config struct {
	APIKey     string
	BlogURL    string
	Endpoint   string
	UserAgent  string
	HTTPClient *http.Client
}

type Comment struct {
	UserIP      string
	UserAgent   string
	Referrer    string
	Permalink   string
	Type        string
	Author      string
	AuthorEmail string
	AuthorURL   string
	Content     string
	CreatedAt   time.Time
	IsTest      bool
}

type Client struct {
	apiKey     string
	blogURL    string
	endpoint   string
	userAgent  string
	httpClient *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("akismet api key is required")
	}

	blogURL := strings.TrimSpace(cfg.BlogURL)
	if blogURL == "" {
		return nil, fmt.Errorf("akismet blog url is required")
	}
	parsedBlogURL, err := url.ParseRequestURI(blogURL)
	if err != nil || parsedBlogURL == nil || parsedBlogURL.Scheme == "" || parsedBlogURL.Host == "" {
		return nil, fmt.Errorf("akismet blog url must be a full URL")
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsedEndpoint, err := url.ParseRequestURI(endpoint)
	if err != nil || parsedEndpoint == nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return nil, fmt.Errorf("akismet endpoint must be a full URL")
	}

	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Client{
		apiKey:     apiKey,
		blogURL:    blogURL,
		endpoint:   endpoint,
		userAgent:  userAgent,
		httpClient: httpClient,
	}, nil
}

func (c *Client) CheckComment(ctx context.Context, comment Comment) (bool, error) {
	values := url.Values{}
	values.Set("api_key", c.apiKey)
	values.Set("blog", c.blogURL)
	values.Set("user_ip", strings.TrimSpace(comment.UserIP))
	values.Set("user_agent", strings.TrimSpace(comment.UserAgent))
	values.Set("referrer", strings.TrimSpace(comment.Referrer))
	values.Set("permalink", strings.TrimSpace(comment.Permalink))
	values.Set("comment_type", valueOrDefault(comment.Type, "comment"))
	values.Set("comment_author", strings.TrimSpace(comment.Author))
	values.Set("comment_author_email", strings.TrimSpace(comment.AuthorEmail))
	values.Set("comment_author_url", strings.TrimSpace(comment.AuthorURL))
	values.Set("comment_content", strings.TrimSpace(comment.Content))
	values.Set("blog_charset", "UTF-8")
	if !comment.CreatedAt.IsZero() {
		values.Set("comment_date_gmt", comment.CreatedAt.UTC().Format(time.RFC3339))
	}
	if comment.IsTest {
		values.Set("is_test", "1")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return false, fmt.Errorf("create akismet request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("akismet comment check failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return false, fmt.Errorf("read akismet response: %w", err)
	}
	body := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("akismet returned %s: %s", resp.Status, body)
	}

	switch body {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("akismet returned unexpected response %q", body)
	}
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
