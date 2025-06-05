package eget2

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zeebo/blake3"
)

type OCIClientConfig struct {
	APIKey  string
	APIBase string
}

type OCIClient struct {
	client  *Client
	apiBase string
	token   string
}

type OCILayer struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}

type OCIManifest struct {
	MediaType string     `json:"mediaType"`
	Config    OCIConfig  `json:"config"`
	Layers    []OCILayer `json:"layers"`
}

type OCIConfig struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type OCIReference struct {
	Package string
	Tag     string
}

func NewOCIClient(config OCIClientConfig) (*OCIClient, error) {
	if config.APIBase == "" {
		config.APIBase = "https://ghcr.io/v2"
	}
	clientConfig := ClientConfig{
		UserAgent: "eget2/1.0",
		AuthToken: config.APIKey,
		Timeout:   30 * time.Second,
	}
	client, err := NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return &OCIClient{
		client:  client,
		apiBase: strings.TrimSuffix(config.APIBase, "/"),
		token:   config.APIKey,
	}, nil
}

func (c *OCIClient) GetAPIBase() string {
	return c.apiBase
}

func (c *OCIClient) Close() {
	c.client.Close()
}

func ParseOCIReference(ref string) OCIReference {
	ref = strings.TrimPrefix(ref, "ghcr.io/")
	if parts := strings.SplitN(ref, "@", 2); len(parts) == 2 {
		return OCIReference{Package: parts[0], Tag: parts[1]}
	}
	if parts := strings.SplitN(ref, ":", 2); len(parts) == 2 {
		return OCIReference{Package: parts[0], Tag: parts[1]}
	}
	return OCIReference{Package: ref, Tag: "latest"}
}

func (c *OCIClient) getAuthToken(ctx context.Context, repository string) (string, error) {
	url := fmt.Sprintf("https://ghcr.io/token?service=ghcr.io&scope=repository:%s:pull", repository)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	resp, err := c.client.Do(ctx, req)
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	defer resp.Body.Close()
	var tokenResponse struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Fetching token from: %s\n", url)
	fmt.Fprintf(os.Stdout, "Token fetched successfully\n")
	return tokenResponse.Token, nil
}

func (c *OCIClient) FetchManifest(ctx context.Context, ref OCIReference) (OCIManifest, error) {
	token, err := c.getAuthToken(ctx, ref.Package)
	if err != nil {
		return OCIManifest{}, fmt.Errorf("get auth token: %w", err)
	}
	url := fmt.Sprintf("%s/%s/manifests/%s", c.apiBase, ref.Package, ref.Tag)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return OCIManifest{}, fmt.Errorf("create manifest request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(ctx, req)
	if err != nil {
		return OCIManifest{}, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return OCIManifest{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	var manifest OCIManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return OCIManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Fetching manifest from: %s\n", url)
	return manifest, nil
}

func (c *OCIClient) DownloadLayer(ctx context.Context, ref OCIReference, layer OCILayer, opts DownloadOptions) (string, error) {
	token, err := c.getAuthToken(ctx, ref.Package)
	if err != nil {
		return "", fmt.Errorf("get auth token: %w", err)
	}

	blobURL := fmt.Sprintf("%s/%s/blobs/%s", c.apiBase, ref.Package, layer.Digest)
	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = layer.Annotations["org.opencontainers.image.title"]
		if outputPath == "" {
			hash := blake3.New()
			hash.Write([]byte(blobURL))
			outputPath = hex.EncodeToString(hash.Sum(nil))
		}
	}

	if opts.FileMode == FileModeSkipExisting && fileExists(outputPath) {
		return outputPath, nil
	}

	if opts.FileMode == FileModePromptOverwrite && fileExists(outputPath) {
		if !promptOverwrite(outputPath) {
			return outputPath, nil
		}
	}

	tempPath := outputPath + ".part"
	metaPath := outputPath + ".part.meta"

	var resumeOffset int64
	var etag, lastModified string
	if meta, err := readMetadata(metaPath); err == nil {
		resumeOffset = meta.Offset
		etag = meta.ETag
		lastModified = meta.LastModified
	}

	req, err := http.NewRequestWithContext(ctx, "GET", blobURL, nil)
	if err != nil {
		return "", fmt.Errorf("create blob request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if resumeOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeOffset))
		if etag != "" {
			req.Header.Set("If-Range", etag)
		} else if lastModified != "" {
			req.Header.Set("If-Range", lastModified)
		}
	}

	fmt.Fprintf(os.Stdout, "Downloading blob: %s\n", blobURL)
	resp, err := c.client.Do(ctx, req)
	if err != nil {
		return "", fmt.Errorf("send blob request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	if resumeOffset > 0 && resp.StatusCode == http.StatusOK {
		resumeOffset = 0
		os.Remove(tempPath)
		os.Remove(metaPath)
	}

	totalSize := resp.ContentLength
	if resumeOffset > 0 && resp.Header.Get("Content-Range") != "" {
		if rangeParts := strings.Split(resp.Header.Get("Content-Range"), "/"); len(rangeParts) == 2 {
			if size, err := strconv.ParseInt(rangeParts[1], 10, 64); err == nil {
				totalSize = size
			}
		}
	}

	if opts.ProgressChan != nil {
		opts.ProgressChan <- DownloadProgress{State: DownloadPreparing, Total: totalSize}
	}

	var file *os.File
	if resumeOffset > 0 {
		file, err = os.OpenFile(tempPath, os.O_APPEND|os.O_WRONLY, 0644)
	} else {
		file, err = os.Create(tempPath)
	}
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	hash := blake3.New()
	if resumeOffset > 0 {
		if _, err := hashFile(tempPath, resumeOffset, hash); err != nil {
			return "", fmt.Errorf("hash existing file: %w", err)
		}
	}

	writer := io.MultiWriter(file, hash)
	if opts.ProgressChan != nil {
		writer = io.MultiWriter(writer, &progressWriter{
			ch:      opts.ProgressChan,
			total:   totalSize,
			written: resumeOffset,
		})
	}

	_, err = io.Copy(writer, resp.Body)
	if err != nil {
		if opts.ProgressChan != nil {
			opts.ProgressChan <- DownloadProgress{State: DownloadError}
		}
		return "", fmt.Errorf("copy response: %w", err)
	}

	if err := writeMetadata(metaPath, Metadata{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Offset:       resumeOffset,
	}); err != nil {
		return "", fmt.Errorf("write metadata: %w", err)
	}

	if err := os.Rename(tempPath, outputPath); err != nil {
		return "", fmt.Errorf("rename file: %w", err)
	}
	os.Remove(metaPath)

	if isELF(outputPath) {
		if err := os.Chmod(outputPath, 0755); err != nil {
			return "", fmt.Errorf("set permissions: %w", err)
		}
	}

	if opts.ExtractArchive {
		extractDir := opts.ExtractDir
		if extractDir == "" {
			extractDir = filepath.Dir(outputPath)
		}
		if err := extractArchive(outputPath, extractDir); err != nil {
			return "", fmt.Errorf("extract archive: %w", err)
		}
	}

	if opts.ProgressChan != nil {
		opts.ProgressChan <- DownloadProgress{State: DownloadComplete}
	}

	return outputPath, nil
}

func FilterLayers(manifest OCIManifest, opts FilterOptions) ([]OCILayer, error) {
	var layers []OCILayer
	for _, layer := range manifest.Layers {
		if title, ok := layer.Annotations["org.opencontainers.image.title"]; ok && matchesPatterns(title, opts) {
			layers = append(layers, layer)
		}
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("no matching layers found")
	}
	return layers, nil
}
