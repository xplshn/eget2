package eget2

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Client struct {
	httpClient *http.Client
	headers    http.Header
	limiter    *rate.Limiter
	mu         sync.RWMutex
}

type ClientConfig struct {
	UserAgent string
	Headers   map[string]string
	ProxyURL  string
	RateLimit int
	Timeout   time.Duration
	AuthToken string
}

func NewClient(config ClientConfig) (*Client, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	if config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
	}

	headers := make(http.Header)
	if config.UserAgent != "" {
		headers.Set("User-Agent", config.UserAgent)
	}
	if config.AuthToken != "" {
		headers.Set("Authorization", "Bearer "+config.AuthToken)
	}
	for k, v := range config.Headers {
		headers.Set(k, v)
	}

	var limiter *rate.Limiter
	if config.RateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(config.RateLimit), 1)
	}

	return &Client{
		httpClient: client,
		headers:    headers,
		limiter:    limiter,
	}, nil
}

func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	c.mu.RLock()
	for k, v := range c.headers {
		req.Header[k] = v
	}
	c.mu.RUnlock()

	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	return resp, nil
}

func (c *Client) SetAuthToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if token == "" {
		c.headers.Del("Authorization")
	} else {
		c.headers.Set("Authorization", "Bearer "+token)
	}
}

func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}
