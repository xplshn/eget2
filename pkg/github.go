package eget2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type GitHubClient struct {
	client  *Client
	apiBase string
	token   string
}

type GitHubConfig struct {
	APIKey    string
	APIBase   string
	RateLimit int
}

func NewGitHubClient(config GitHubConfig) (*GitHubClient, error) {
	if config.APIBase == "" {
		config.APIBase = "https://api.github.com"
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
	return &GitHubClient{
		client:  client,
		apiBase: strings.TrimSuffix(config.APIBase, "/"),
		token:   config.APIKey,
	}, nil
}

func (c *GitHubClient) GetHTTPClient() *Client {
	return c.client
}

func (c *GitHubClient) Close() {
	c.client.Close()
}

type GitHubRelease struct {
	Name        string        `json:"name"`
	TagName     string        `json:"tag_name"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt string        `json:"published_at"`
	Assets      []GitHubAsset `json:"assets"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *GitHubClient) FetchReleases(ctx context.Context, project, tag string) ([]GitHubRelease, error) {
	owner, repo, err := parseProject(project)
	if err != nil {
		return nil, fmt.Errorf("parse project: %w", err)
	}

	path := fmt.Sprintf("/repos/%s/%s/releases", owner, repo)
	if tag != "" {
		path = fmt.Sprintf("%s/tags/%s", path, tag)
	} else {
		path = fmt.Sprintf("%s?per_page=100", path)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var releases []GitHubRelease
	if tag != "" {
		var release GitHubRelease
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		releases = []GitHubRelease{release}
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}

	return releases, nil
}

func (c *GitHubClient) FilterAssets(releases []GitHubRelease, opts FilterOptions) ([]GitHubAsset, error) {
	var selectedRelease *GitHubRelease
	if opts.Tag != "" {
		for _, r := range releases {
			if r.TagName == opts.Tag {
				selectedRelease = &r
				break
			}
		}
	} else {
		for _, r := range releases {
			if !r.Prerelease {
				selectedRelease = &r
				break
			}
		}
		if selectedRelease == nil && len(releases) > 0 {
			selectedRelease = &releases[0]
		}
	}

	if selectedRelease == nil {
		return nil, fmt.Errorf("no matching release found")
	}

	var assets []GitHubAsset
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

func (c *GitHubClient) DownloadAsset(ctx context.Context, asset GitHubAsset, opts DownloadOptions) (string, error) {
	opts.URL = asset.BrowserDownloadURL
	downloader := NewDownloader(c.client)
	return downloader.Download(ctx, opts)
}

func parseProject(project string) (string, string, error) {
	parts := strings.Split(project, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid project format: %s, expected owner/repo", project)
	}
	return parts[0], parts[1], nil
}
