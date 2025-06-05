package eget2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type GitLabClient struct {
	client  *Client
	apiBase string
	token   string
}

type GitLabConfig struct {
	APIKey    string
	APIBase   string
	RateLimit int
}

func NewGitLabClient(config GitLabConfig) (*GitLabClient, error) {
	if config.APIBase == "" {
		config.APIBase = "https://gitlab.com/api/v4"
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
	return &GitLabClient{
		client:  client,
		apiBase: strings.TrimSuffix(config.APIBase, "/"),
		token:   config.APIKey,
	}, nil
}

func (c *GitLabClient) GetHTTPClient() *Client {
	return c.client
}

func (c *GitLabClient) Close() {
	c.client.Close()
}

type GitLabRelease struct {
	Name       string       `json:"name"`
	TagName    string       `json:"tag_name"`
	Upcoming   bool         `json:"upcoming_release"`
	ReleasedAt string       `json:"released_at"`
	Assets     GitLabAssets `json:"assets"`
}

type GitLabAssets struct {
	Links []GitLabAsset `json:"links"`
}

type GitLabAsset struct {
	Name           string `json:"name"`
	DirectAssetURL string `json:"direct_asset_url"`
}

func (c *GitLabClient) FetchReleases(ctx context.Context, project, tag string) ([]GitLabRelease, error) {
	encodedProject := strings.ReplaceAll(project, "/", "%2F")
	path := fmt.Sprintf("/projects/%s/releases", encodedProject)
	if tag != "" {
		path = fmt.Sprintf("%s/%s", path, tag)
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

	var releases []GitLabRelease
	if tag != "" {
		var release GitLabRelease
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		releases = []GitLabRelease{release}
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}

	return releases, nil
}

func (c *GitLabClient) FilterAssets(releases []GitLabRelease, opts FilterOptions) ([]GitLabAsset, error) {
	var selectedRelease *GitLabRelease
	if opts.Tag != "" {
		for _, r := range releases {
			if r.TagName == opts.Tag {
				selectedRelease = &r
				break
			}
		}
	} else {
		for _, r := range releases {
			if !r.Upcoming {
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

	var assets []GitLabAsset
	for _, asset := range selectedRelease.Assets.Links {
		if matchesPatterns(asset.Name, opts) {
			assets = append(assets, asset)
		}
	}

	if len(assets) == 0 {
		return nil, fmt.Errorf("no matching assets found")
	}

	return assets, nil
}

func (c *GitLabClient) DownloadAsset(ctx context.Context, asset GitLabAsset, opts DownloadOptions) (string, error) {
	opts.URL = asset.DirectAssetURL
	downloader := NewDownloader(c.client)
	return downloader.Download(ctx, opts)
}
