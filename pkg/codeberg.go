package eget2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type CodebergClient struct {
	client  *Client
	apiBase string
	token   string
}

type CodebergConfig struct {
	APIKey    string
	APIBase   string
	RateLimit int
}

func NewCodebergClient(config CodebergConfig) (*CodebergClient, error) {
	if config.APIBase == "" {
		config.APIBase = "https://codeberg.org/api/v1"
	}

	clientConfig := ClientConfig{
		UserAgent: "eget2/1.0",
		AuthToken: config.APIKey,
		RateLimit: config.RateLimit,
		Timeout:   30 * time.Second,
	}

	client, err := NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return &CodebergClient{
		client:  client,
		apiBase: strings.TrimSuffix(config.APIBase, "/"),
		token:   config.APIKey,
	}, nil
}

func (c *CodebergClient) GetHTTPClient() *Client {
	return c.client
}

func (c *CodebergClient) Close() {
	c.client.Close()
}

type CodebergRelease struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	TagName     string         `json:"tag_name"`
	PublishedAt string         `json:"published_at"`
	Assets      []CodebergAsset `json:"assets"`

	Attachments []CodebergAsset `json:"attachments"`
}

type CodebergAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`

	DownloadURL        string `json:"download_url"`
	URL                string `json:"url"`
}

func (c *CodebergClient) FetchReleases(ctx context.Context, project, tag string) ([]CodebergRelease, error) {
	owner, repo, err := parseProject(project)
	if err != nil {
		return nil, fmt.Errorf("parse project: %w", err)
	}

	var releases []CodebergRelease

	if tag != "" {
		// Get specific release by tag
		path := fmt.Sprintf("/repos/%s/%s/releases/tags/%s", owner, repo, tag)

		req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+path, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.client.Do(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}

		var release CodebergRelease
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		releases = []CodebergRelease{release}
	} else {
		// Get all releases
		path := fmt.Sprintf("/repos/%s/%s/releases", owner, repo)

		req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+path, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.client.Do(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}

	// Try to fetch additional assets if not already present
	for i := range releases {
		// If release already has assets from the main call, use those
		if len(releases[i].Assets) > 0 {
			// Normalize download URLs
			c.normalizeAssetURLs(&releases[i])
			continue
		}

		// Check if attachments field has data instead
		if len(releases[i].Attachments) > 0 {
			releases[i].Assets = releases[i].Attachments
			c.normalizeAssetURLs(&releases[i])
			continue
		}

		// Try to fetch assets separately
		if releases[i].ID > 0 {
			assets, err := c.fetchReleaseAssets(ctx, owner, repo, releases[i].ID)
			if err != nil {
				// Don't fail completely, just continue
				continue
			}
			releases[i].Assets = assets
			c.normalizeAssetURLs(&releases[i])
		}
	}

	return releases, nil
}

func (c *CodebergClient) normalizeAssetURLs(release *CodebergRelease) {
	for i := range release.Assets {
		asset := &release.Assets[i]
		// Use the first available URL field
		if asset.BrowserDownloadURL == "" {
			if asset.DownloadURL != "" {
				asset.BrowserDownloadURL = asset.DownloadURL
			} else if asset.URL != "" {
				asset.BrowserDownloadURL = asset.URL
			}
		}
	}
}

func (c *CodebergClient) fetchReleaseAssets(ctx context.Context, owner, repo string, releaseID int64) ([]CodebergAsset, error) {
	// Try different possible endpoints for assets
	endpoints := []string{
		fmt.Sprintf("/repos/%s/%s/releases/%d/assets", owner, repo, releaseID),
		fmt.Sprintf("/repos/%s/%s/releases/%d/attachments", owner, repo, releaseID),
	}

	for _, path := range endpoints {
		req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+path, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.client.Do(ctx, req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var assets []CodebergAsset
			if err := json.NewDecoder(resp.Body).Decode(&assets); err != nil {
				continue
			}
			return assets, nil
		}
	}

	return nil, fmt.Errorf("no valid assets endpoint found")
}

func (c *CodebergClient) FilterAssets(releases []CodebergRelease, opts FilterOptions) ([]CodebergAsset, error) {
	var selectedRelease *CodebergRelease
	if opts.Tag != "" {
		for _, r := range releases {
			if r.TagName == opts.Tag {
				selectedRelease = &r
				break
			}
		}
	} else {
		// Find the most recent release that has assets
		for _, r := range releases {
			if len(r.Assets) > 0 {
				selectedRelease = &r
				break
			}
		}
	}

	if selectedRelease == nil {
		return nil, fmt.Errorf("no release found with assets")
	}



	var assets []CodebergAsset
	for _, asset := range selectedRelease.Assets {
		if matchesPatterns(asset.Name, opts) {
			assets = append(assets, asset)
		}
	}

	if len(assets) == 0 {
		return nil, fmt.Errorf("no matching assets found")
	}

	return assets, nil
}

func (c *CodebergClient) DownloadAsset(ctx context.Context, asset CodebergAsset, opts DownloadOptions) (string, error) {
	opts.URL = asset.BrowserDownloadURL
	downloader := NewDownloader(c.client)
	return downloader.Download(ctx, opts)
}
