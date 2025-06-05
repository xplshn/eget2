package eget2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/hedzr/progressbar"
	"github.com/zeebo/blake3"
	"golang.org/x/sync/semaphore"
	"net/url"
)

type DownloadState int

const (
	DownloadPreparing DownloadState = iota
	DownloadInProgress
	DownloadComplete
	DownloadError
	DownloadAborted
)

type DownloadProgress struct {
	State      DownloadState
	Downloaded int64
	Total      int64
}

type DownloadOptions struct {
	URL            string
	OutputPath     string
	ExtractArchive bool
	ExtractDir     string
	FileMode       FileMode
	Concurrency    int64
	ProgressChan   chan<- DownloadProgress
	GHCRAPI        string
}

type FileMode int

const (
	FileModeSkipExisting FileMode = iota
	FileModeForceOverwrite
	FileModePromptOverwrite
)

type Downloader struct {
	client *Client
}

func NewDownloader(client *Client) *Downloader {
	return &Downloader{client: client}
}

func (d *Downloader) Download(ctx context.Context, opts DownloadOptions) (string, error) {
	parsedURL, err := url.Parse(opts.URL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	finalPath, err := determineOutputPath(parsedURL, opts.OutputPath)
	if err != nil {
		return "", fmt.Errorf("determine output path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(finalPath), 0755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if opts.FileMode == FileModeSkipExisting && fileExists(finalPath) {
		return finalPath, nil
	}

	if opts.FileMode == FileModePromptOverwrite && fileExists(finalPath) {
		if !promptOverwrite(finalPath) {
			return finalPath, nil
		}
	}

	tempPath := finalPath + ".part"
	metaPath := finalPath + ".part.meta"

	var resumeOffset int64
	var etag, lastModified string
	var attempt int
	const maxAttempts = 3

	for attempt < maxAttempts {
		if meta, err := readMetadata(metaPath); err == nil {
			resumeOffset = meta.Offset
			etag = meta.ETag
			lastModified = meta.LastModified
		}

		req, err := http.NewRequestWithContext(ctx, "GET", opts.URL, nil)
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}

		if resumeOffset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeOffset))
			if etag != "" {
				req.Header.Set("If-Range", etag)
			} else if lastModified != "" {
				req.Header.Set("If-Range", lastModified)
			}
		}

		resp, err := d.client.Do(ctx, req)
		if err != nil {
			return "", fmt.Errorf("send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			if resp.StatusCode == http.StatusUnauthorized && attempt < maxAttempts-1 {
				attempt++
				continue
			}
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

		if err := os.Rename(tempPath, finalPath); err != nil {
			return "", fmt.Errorf("rename file: %w", err)
		}
		os.Remove(metaPath)

		if isELF(finalPath) {
			if err := os.Chmod(finalPath, 0755); err != nil {
				return "", fmt.Errorf("set permissions: %w", err)
			}
		}

		if opts.ExtractArchive {
			extractDir := opts.ExtractDir
			if extractDir == "" {
				extractDir = filepath.Dir(finalPath)
			}
			if err := extractArchive(finalPath, extractDir); err != nil {
				return "", fmt.Errorf("extract archive: %w", err)
			}
		}

		if opts.ProgressChan != nil {
			opts.ProgressChan <- DownloadProgress{State: DownloadComplete}
		}

		return finalPath, nil
	}

	return "", fmt.Errorf("download failed after %d attempts", maxAttempts)
}

func (d *Downloader) DownloadConcurrently(ctx context.Context, opts []DownloadOptions) ([]string, error) {
	sem := semaphore.NewWeighted(opts[0].Concurrency)
	var wg sync.WaitGroup
	results := make([]string, len(opts))
	errs := make([]error, len(opts))

	for i, opt := range opts {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, fmt.Errorf("acquire semaphore: %w", err)
		}
		wg.Add(1)
		go func(i int, opt DownloadOptions) {
			defer wg.Done()
			defer sem.Release(1)
			path, err := d.Download(ctx, opt)
			results[i] = path
			errs[i] = err
		}(i, opt)
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return results, fmt.Errorf("concurrent download: %w", err)
		}
	}

	return results, nil
}

type progressWriter struct {
	ch      chan<- DownloadProgress
	total   int64
	written int64
	mu      sync.Mutex
	bar     progressbar.PB
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	n := len(p)
	pw.written += int64(n)
	pw.ch <- DownloadProgress{State: DownloadInProgress, Downloaded: pw.written, Total: pw.total}
	if pw.bar != nil {
		_, err := pw.bar.Write(p)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func determineOutputPath(parsedURL *url.URL, outputPath string) (string, error) {
	if outputPath == "" {
		filename := filepath.Base(parsedURL.Path)
		if filename == "" || filename == "/" {
			hash := sha256.Sum256([]byte(parsedURL.String()))
			filename = hex.EncodeToString(hash[:])
		}
		return filename, nil
	}

	if strings.HasSuffix(outputPath, "/") {
		filename := filepath.Base(parsedURL.Path)
		if filename == "" || filename == "/" {
			hash := sha256.Sum256([]byte(parsedURL.String()))
			filename = hex.EncodeToString(hash[:])
		}
		return filepath.Join(outputPath, filename), nil
	}

	return outputPath, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func promptOverwrite(path string) bool {
	fmt.Printf("Overwrite %s? [y/N] ", path)
	var input string
	fmt.Scanln(&input)
	return strings.ToLower(input) == "y" || strings.ToLower(input) == "yes"
}

func isELF(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	buf := make([]byte, 4)
	if _, err := file.Read(buf); err != nil {
		return false
	}
	return string(buf) == "\x7fELF"
}

func readMetadata(path string) (Metadata, error) {
	var meta Metadata
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return meta, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, fmt.Errorf("read metadata: %w", err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return meta, nil
}

func writeMetadata(path string, meta Metadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func hashFile(path string, n int64, hash *blake3.Hasher) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	_, err = io.CopyN(hash, file, n)
	if err != nil {
		return 0, fmt.Errorf("hash file: %w", err)
	}
	return n, nil
}
