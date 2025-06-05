package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"eget2/pkg"

	"github.com/hedzr/progressbar"
	"github.com/hedzr/progressbar/cursor"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
	"net/url"
)

func main() {
	app := &cli.Command{
		Name:    "eget2",
		Usage:   "Download files from GitHub, GitLab, GHCR, or direct URLs",
		Version: "1.0.0",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "github",
				Aliases: []string{"gh"},
				Usage:   "GitHub project in owner/repo format",
			},
			&cli.StringSliceFlag{
				Name:    "gitlab",
				Aliases: []string{"gl"},
				Usage:   "GitLab project in owner/repo format or project ID",
			},
			&cli.StringSliceFlag{
				Name:  "ghcr",
				Usage: "GHCR image or blob reference",
			},
			&cli.StringSliceFlag{
				Name:    "regex",
				Aliases: []string{"r"},
				Usage:   "Regex patterns to select assets",
			},
			&cli.StringSliceFlag{
				Name:    "match",
				Aliases: []string{"m"},
				Usage:   "Keywords to match in asset names",
			},
			&cli.StringSliceFlag{
				Name:    "exclude",
				Aliases: []string{"e"},
				Usage:   "Keywords to exclude from asset names",
			},
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "Skip prompts and select first asset",
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Output file or directory path",
			},
			&cli.IntFlag{
				Name:    "concurrency",
				Aliases: []string{"c"},
				Usage:   "Concurrency limit for downloads",
				Value:   30,
			},
			&cli.StringFlag{
				Name:  "ghcr-api",
				Usage: "GHCR API base URL",
			},
			&cli.BoolFlag{
				Name:  "exact-case",
				Usage: "Use exact case matching for keywords",
			},
			&cli.BoolFlag{
				Name:  "extract",
				Usage: "Extract archives after download",
			},
			&cli.StringFlag{
				Name:  "extract-dir",
				Usage: "Directory to extract archives",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Force overwrite existing files",
			},
			&cli.BoolFlag{
				Name:  "skip-existing",
				Usage: "Skip downloading if file exists",
			},
		},
		Action: run,
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(_ context.Context, c *cli.Command) error {
	if len(os.Args) == 1 {
		return fmt.Errorf("no arguments provided, try with --help")
	}

	cursor.Hide()
	defer cursor.Show()

	ctx := context.Background()
	var wg sync.WaitGroup
	var errors []string
	var errorsMu sync.Mutex

	var bar progressbar.MultiPB
	var tasks *progressbar.Tasks
	termWidth := getTerminalWidth()
	if termWidth > 0 {
		bar = progressbar.New()
		tasks = progressbar.NewTasks(bar)
		defer tasks.Close()
	}

	fileMode := eget2.FileModePromptOverwrite
	if c.Bool("force") {
		fileMode = eget2.FileModeForceOverwrite
	} else if c.Bool("skip-existing") {
		fileMode = eget2.FileModeSkipExisting
	}

	filterOpts := eget2.FilterOptions{
		Regexes:         c.StringSlice("regex"),
		MatchKeywords:   c.StringSlice("match"),
		ExcludeKeywords: c.StringSlice("exclude"),
		ExactCase:       c.Bool("exact-case"),
	}

	downloadOpts := eget2.DownloadOptions{
		OutputPath:     c.String("output"),
		ExtractArchive: c.Bool("extract"),
		ExtractDir:     c.String("extract-dir"),
		FileMode:       fileMode,
		Concurrency:    int64(c.Int("concurrency")),
		GHCRAPI:        c.String("ghcr-api"),
	}

	if githubProjects := c.StringSlice("github"); len(githubProjects) > 0 {
		ghClient, err := eget2.NewGitHubClient(eget2.GitHubConfig{
			APIKey:    os.Getenv("GITHUB_TOKEN"),
			RateLimit: 10,
		})
		if err != nil {
			return fmt.Errorf("create GitHub client: %w", err)
		}
		defer ghClient.Close()

		for _, project := range githubProjects {
			tag := ""
			if parts := strings.Split(project, "@"); len(parts) == 2 {
				project, tag = parts[0], parts[1]
			}
			filterOpts.Tag = tag

			wg.Add(1)
			go func(project string) {
				defer wg.Done()
				if err := downloadGitHub(ctx, ghClient, project, filterOpts, downloadOpts, tasks, termWidth, c.Bool("yes")); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("GitHub %s: %v", project, err))
					errorsMu.Unlock()
				}
			}(project)
		}
	}

	if gitlabProjects := c.StringSlice("gitlab"); len(gitlabProjects) > 0 {
		glClient, err := eget2.NewGitLabClient(eget2.GitLabConfig{
			APIKey:    os.Getenv("GITLAB_TOKEN"),
			RateLimit: 10,
		})
		if err != nil {
			return fmt.Errorf("create GitLab client: %w", err)
		}
		defer glClient.Close()

		for _, project := range gitlabProjects {
			tag := ""
			if parts := strings.Split(project, "@"); len(parts) == 2 {
				project, tag = parts[0], parts[1]
			}
			filterOpts.Tag = tag

			wg.Add(1)
			go func(project string) {
				defer wg.Done()
				if err := downloadGitLab(ctx, glClient, project, filterOpts, downloadOpts, tasks, termWidth, c.Bool("yes")); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("GitLab %s: %v", project, err))
					errorsMu.Unlock()
				}
			}(project)
		}
	}

	if ghcrRefs := c.StringSlice("ghcr"); len(ghcrRefs) > 0 {
		for _, ref := range ghcrRefs {
			wg.Add(1)
			go func(ref string) {
				defer wg.Done()
				if err := downloadGHCR(ctx, ref, filterOpts, downloadOpts, tasks, termWidth, c.Bool("yes")); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("GHCR %s: %v", ref, err))
					errorsMu.Unlock()
				}
			}(ref)
		}
	}

	if links := c.Args().Slice(); len(links) > 0 {
		client, err := eget2.NewClient(eget2.ClientConfig{
			UserAgent: "eget2/1.0",
			Timeout:   30 * time.Second,
		})
		if err != nil {
			return fmt.Errorf("create client: %w", err)
		}
		defer client.Close()

		downloader := eget2.NewDownloader(client)
		for _, link := range links {
			urlType, err := parseURL(link)
			if err != nil {
				errorsMu.Lock()
				errors = append(errors, fmt.Sprintf("parse URL %s: %v", link, err))
				errorsMu.Unlock()
				continue
			}

			switch urlType.(type) {
			case URLGitHub:
				ghClient, err := eget2.NewGitHubClient(eget2.GitHubConfig{APIKey: os.Getenv("GITHUB_TOKEN")})
				if err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("create GitHub client: %v", err))
					errorsMu.Unlock()
					continue
				}
				defer ghClient.Close()
				project := urlType.(URLGitHub).Project
				if err := downloadGitHub(ctx, ghClient, project, filterOpts, downloadOpts, tasks, termWidth, c.Bool("yes")); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("GitHub %s: %v", project, err))
					errorsMu.Unlock()
				}
			case URLGitLab:
				glClient, err := eget2.NewGitLabClient(eget2.GitLabConfig{APIKey: os.Getenv("GITLAB_TOKEN")})
				if err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("create GitLab client: %v", err))
					errorsMu.Unlock()
					continue
				}
				defer glClient.Close()
				project := urlType.(URLGitLab).Project
				if err := downloadGitLab(ctx, glClient, project, filterOpts, downloadOpts, tasks, termWidth, c.Bool("yes")); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("GitLab %s: %v", project, err))
					errorsMu.Unlock()
				}
			case URLGHCR:
				ref := urlType.(URLGHCR).Reference
				if err := downloadGHCR(ctx, ref, filterOpts, downloadOpts, tasks, termWidth, c.Bool("yes")); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Sprintf("GHCR %s: %v", ref, err))
					errorsMu.Unlock()
				}
			case URLDirect:
				wg.Add(1)
				go func(link string) {
					defer wg.Done()
					if err := downloadDirect(ctx, downloader, link, downloadOpts, tasks, termWidth); err != nil {
						errorsMu.Lock()
						errors = append(errors, fmt.Sprintf("direct URL %s: %v", link, err))
						errorsMu.Unlock()
					}
				}(link)
			}
		}
	}

	wg.Wait()

	if len(errors) > 0 {
		for i, err := range errors {
			fmt.Fprintf(os.Stderr, "%d. %s\n", i+1, err)
		}
		return fmt.Errorf("completed with errors")
	}

	return nil
}

func downloadDirect(ctx context.Context, downloader *eget2.Downloader, link string, opts eget2.DownloadOptions, tasks *progressbar.Tasks, termWidth int) error {
	progressChan := make(chan eget2.DownloadProgress)
	opts.URL = link
	opts.ProgressChan = progressChan

	barTitle := fmt.Sprintf("Downloading %s", filepath.Base(link))
	pbarOpts := []progressbar.Opt{
		progressbar.WithBarResumeable(true),
	}
	if termWidth < 120 {
		barTitle = filepath.Base(link)
		pbarOpts = append(
			pbarOpts,
			progressbar.WithBarTextSchema(`{{.Bar}} {{.Percent}} | <font color="green">{{.Title}}</font>`),
			progressbar.WithBarWidth(termWidth-30),
		)
	}

	var bar progressbar.PB
	var lastDownloaded int64
	tasks.Add(
		progressbar.WithTaskAddBarTitle(barTitle),
		progressbar.WithTaskAddBarOptions(pbarOpts...),
		progressbar.WithTaskAddOnTaskProgressing(func(pbar progressbar.PB, _ <-chan struct{}) (stop bool) {
			bar = pbar
			for p := range progressChan {
				switch p.State {
				case eget2.DownloadPreparing:
					bar.UpdateRange(0, p.Total)
					lastDownloaded = 0
				case eget2.DownloadInProgress:
					bar.Step(p.Downloaded - lastDownloaded)
					lastDownloaded = p.Downloaded
				case eget2.DownloadComplete:
					return true
				case eget2.DownloadError, eget2.DownloadAborted:
					return true
				}
			}
			return true
		}),
	)

	_, err := downloader.Download(ctx, opts)
	return err
}

func downloadGitHub(ctx context.Context, client *eget2.GitHubClient, project string, filterOpts eget2.FilterOptions, downloadOpts eget2.DownloadOptions, tasks *progressbar.Tasks, termWidth int, autoSelect bool) error {
	releases, err := client.FetchReleases(ctx, project, filterOpts.Tag)
	if err != nil {
		return fmt.Errorf("fetch releases: %w", err)
	}

	assets, err := client.FilterAssets(releases, filterOpts)
	if err != nil {
		return fmt.Errorf("filter assets: %w", err)
	}

	var asset eget2.GitHubAsset
	if len(assets) == 1 || autoSelect {
		asset = assets[0]
	} else {
		fmt.Fprintf(os.Stdout, "\nAvailable assets:\n")
		for i, a := range assets {
			fmt.Fprintf(os.Stdout, "%d. %s (%s)\n", i+1, a.Name, humanBytes(a.Size))
		}
		fmt.Fprintf(os.Stdout, "\nSelect an asset (1-%d): ", len(assets))
		var choice int
		fmt.Scan(&choice)
		if choice < 1 || choice > len(assets) {
			return fmt.Errorf("invalid asset selection")
		}
		asset = assets[choice-1]
	}

	progressChan := make(chan eget2.DownloadProgress)
	downloadOpts.URL = asset.BrowserDownloadURL
	downloadOpts.ProgressChan = progressChan

	barTitle := fmt.Sprintf("Downloading %s", asset.Name)
	pbarOpts := []progressbar.Opt{
		progressbar.WithBarResumeable(true),
	}
	if termWidth < 120 {
		barTitle = asset.Name
		pbarOpts = append(
			pbarOpts,
			progressbar.WithBarTextSchema(`{{.Bar}} {{.Percent}} | <font color="green">{{.Title}}</font>`),
			progressbar.WithBarWidth(termWidth-30),
		)
	}

	var bar progressbar.PB
	var lastDownloaded int64
	tasks.Add(
		progressbar.WithTaskAddBarTitle(barTitle),
		progressbar.WithTaskAddBarOptions(pbarOpts...),
		progressbar.WithTaskAddOnTaskProgressing(func(pbar progressbar.PB, _ <-chan struct{}) (stop bool) {
			bar = pbar
			for p := range progressChan {
				switch p.State {
				case eget2.DownloadPreparing:
					bar.UpdateRange(0, p.Total)
					lastDownloaded = 0
				case eget2.DownloadInProgress:
					bar.Step(p.Downloaded - lastDownloaded)
					lastDownloaded = p.Downloaded
				case eget2.DownloadComplete:
					return true
				case eget2.DownloadError, eget2.DownloadAborted:
					return true
				}
			}
			return true
		}),
	)

	downloader := eget2.NewDownloader(client.GetHTTPClient())
	_, err = downloader.Download(ctx, downloadOpts)
	return err
}

func downloadGitLab(ctx context.Context, client *eget2.GitLabClient, project string, filterOpts eget2.FilterOptions, downloadOpts eget2.DownloadOptions, tasks *progressbar.Tasks, termWidth int, autoSelect bool) error {
	releases, err := client.FetchReleases(ctx, project, filterOpts.Tag)
	if err != nil {
		return fmt.Errorf("fetch releases: %w", err)
	}

	assets, err := client.FilterAssets(releases, filterOpts)
	if err != nil {
		return fmt.Errorf("filter assets: %w", err)
	}

	var asset eget2.GitLabAsset
	if len(assets) == 1 || autoSelect {
		asset = assets[0]
	} else {
		fmt.Fprintf(os.Stdout, "\nAvailable assets:\n")
		for i, a := range assets {
			fmt.Fprintf(os.Stdout, "%d. %s\n", i+1, a.Name)
		}
		fmt.Fprintf(os.Stdout, "\nSelect an asset (1-%d): ", len(assets))
		var choice int
		fmt.Scan(&choice)
		if choice < 1 || choice > len(assets) {
			return fmt.Errorf("invalid asset selection")
		}
		asset = assets[choice-1]
	}

	progressChan := make(chan eget2.DownloadProgress)
	downloadOpts.URL = asset.DirectAssetURL
	downloadOpts.ProgressChan = progressChan

	barTitle := fmt.Sprintf("Downloading %s", asset.Name)
	pbarOpts := []progressbar.Opt{
		progressbar.WithBarResumeable(true),
	}
	if termWidth < 120 {
		barTitle = asset.Name
		pbarOpts = append(
			pbarOpts,
			progressbar.WithBarTextSchema(`{{.Bar}} {{.Percent}} | <font color="green">{{.Title}}</font>`),
			progressbar.WithBarWidth(termWidth-30),
		)
	}

	var bar progressbar.PB
	var lastDownloaded int64
	tasks.Add(
		progressbar.WithTaskAddBarTitle(barTitle),
		progressbar.WithTaskAddBarOptions(pbarOpts...),
		progressbar.WithTaskAddOnTaskProgressing(func(pbar progressbar.PB, _ <-chan struct{}) (stop bool) {
			bar = pbar
			for p := range progressChan {
				switch p.State {
				case eget2.DownloadPreparing:
					bar.UpdateRange(0, p.Total)
					lastDownloaded = 0
				case eget2.DownloadInProgress:
					bar.Step(p.Downloaded - lastDownloaded)
					lastDownloaded = p.Downloaded
				case eget2.DownloadComplete:
					return true
				case eget2.DownloadError, eget2.DownloadAborted:
					return true
				}
			}
			return true
		}),
	)

	downloader := eget2.NewDownloader(client.GetHTTPClient())
	_, err = downloader.Download(ctx, downloadOpts)
	return err
}

func downloadGHCR(ctx context.Context, ref string, filterOpts eget2.FilterOptions, downloadOpts eget2.DownloadOptions, tasks *progressbar.Tasks, termWidth int, autoSelect bool) error {
	ociRef := eget2.ParseOCIReference(ref)
	client, err := eget2.NewOCIClient(eget2.OCIClientConfig{
		APIKey:  os.Getenv("GHCR_TOKEN"),
		APIBase: downloadOpts.GHCRAPI,
	})
	if err != nil {
		return fmt.Errorf("create OCI client: %v", err)
	}
	defer client.Close()

	if strings.HasPrefix(ociRef.Tag, "sha256:") {
		progressChan := make(chan eget2.DownloadProgress)
		downloadOpts.ProgressChan = progressChan

		barTitle := fmt.Sprintf("Downloading %s", filepath.Base(ociRef.Package))
		pbarOpts := []progressbar.Opt{
			progressbar.WithBarResumeable(true),
		}
		if termWidth < 120 {
			barTitle = filepath.Base(ociRef.Package)
			pbarOpts = append(
				pbarOpts,
				progressbar.WithBarTextSchema(`{{.Bar}} {{.Percent}}`),
				progressbar.WithBarWidth(termWidth),
			)
		}

		var bar progressbar.PB
		var lastDownloaded int64
		tasks.Add(
			progressbar.WithTaskAddBarTitle(barTitle),
			progressbar.WithTaskAddBarOptions(pbarOpts...),
			progressbar.WithTaskAddOnTaskProgressing(func(pbar progressbar.PB, _ <-chan struct{}) (stop bool) {
				bar = pbar
				for p := range progressChan {
					switch p.State {
					case eget2.DownloadPreparing:
						bar.UpdateRange(0, p.Total)
						lastDownloaded = 0
					case eget2.DownloadInProgress:
						bar.Step(p.Downloaded - lastDownloaded)
						lastDownloaded = p.Downloaded
					case eget2.DownloadComplete:
						return true
					case eget2.DownloadError, eget2.DownloadAborted:
						return true
					}
				}
				return true
			}),
		)

		layer := eget2.OCILayer{
			Digest: ociRef.Tag,
			Annotations: map[string]string{
				"org.opencontainers.image.title": filepath.Base(ociRef.Package),
			},
		}
		_, err := client.DownloadLayer(ctx, ociRef, layer, downloadOpts)
		return err
	}

	manifest, err := client.FetchManifest(ctx, ociRef)
	if err != nil {
		return fmt.Errorf("fetch manifest: %v", err)
	}

	layers, err := eget2.FilterLayers(manifest, filterOpts)
	if err != nil {
		return fmt.Errorf("filter layers: %v", err)
	}

	var layer eget2.OCILayer
	if len(layers) == 1 || autoSelect {
		layer = layers[0]
	} else {
		fmt.Fprintf(os.Stdout, "\nAvailable layers:\n")
		for i, l := range layers {
			title := l.Annotations["org.opencontainers.image.title"]
			fmt.Fprintf(os.Stdout, "%d. %s (%s)\n", i+1, title, humanBytes(l.Size))
		}
		fmt.Fprintf(os.Stdout, "\nSelect a layer (1-%d): ", len(layers))
		var choice int
		fmt.Scan(&choice)
		if choice < 1 || choice > len(layers) {
			return fmt.Errorf("invalid choice")
		}
		layer = layers[choice-1]
	}

	progressChan := make(chan eget2.DownloadProgress)
	downloadOpts.ProgressChan = progressChan

	barTitle := fmt.Sprintf("Downloading %s", layer.Annotations["org.opencontainers.image.title"])
	pbarOpts := []progressbar.Opt{
		progressbar.WithBarResumeable(true),
	}
	if termWidth < 120 {
		barTitle = layer.Annotations["org.opencontainers.image.title"]
		pbarOpts = append(
			pbarOpts,
			progressbar.WithBarTextSchema(`{{.Bar}} {{.Percent}}`),
			progressbar.WithBarWidth(termWidth),
		)
	}

	var bar progressbar.PB
	var lastDownloaded int64
	tasks.Add(
		progressbar.WithTaskAddBarTitle(barTitle),
		progressbar.WithTaskAddBarOptions(pbarOpts...),
		progressbar.WithTaskAddOnTaskProgressing(func(pbar progressbar.PB, _ <-chan struct{}) (stop bool) {
			bar = pbar
			for p := range progressChan {
				switch p.State {
				case eget2.DownloadPreparing:
					bar.UpdateRange(0, p.Total)
					lastDownloaded = 0
				case eget2.DownloadInProgress:
					bar.Step(p.Downloaded - lastDownloaded)
					lastDownloaded = p.Downloaded
				case eget2.DownloadComplete:
					return true
				case eget2.DownloadError, eget2.DownloadAborted:
					return true
				}
			}
			return true
		}),
	)

	_, err = client.DownloadLayer(ctx, ociRef, layer, downloadOpts)
	return err
}

func parseURL(link string) (interface{}, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	switch {
	case strings.Contains(u.Host, "github.com"):
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			return URLGitHub{Project: fmt.Sprintf("%s/%s", parts[0], parts[1])}, nil
		}
	case strings.Contains(u.Host, "gitlab.com"):
		return URLGitLab{Project: strings.Trim(u.Path, "/")}, nil
	case strings.Contains(u.Host, "ghcr.io"):
		return URLGHCR{Reference: strings.TrimPrefix(link, "ghcr.io/")}, nil
	default:
		return URLDirect{URL: link}, nil
	}
	return nil, fmt.Errorf("unrecognized URL: %s", link)
}

type URLGitHub struct {
	Project string
}

type URLGitLab struct {
	Project string
}

type URLGHCR struct {
	Reference string
}

type URLDirect struct {
	URL string
}

func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func getTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80
	}
	return width
}
