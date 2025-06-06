eget2
=====

**eget2** is a feature-complete command-line tool for downloading files from GitHub, GitLab, Codeberg, GHCR (GitHub Container Registry), or direct URLs. It is designed as a replacement for zyedidia/eget, but it was written from scratch, to fix its shortcomings

Installation
------------

To install eget2, you can use the following command if you have Go installed:

```
go install github.com/xplshn/eget2@master
```

Usage
-----

```
eget2 [global options]
```

### Options

```
NAME:
   eget2 - Download files from GitHub, GitLab, Codeberg, GHCR, or direct URLs

USAGE:
   eget2 [global options]

VERSION:
   1.0.1

GLOBAL OPTIONS:
   --github string, --gh string   [ --github string, --gh string ]   GitHub project in owner/repo format
   --gitlab string, --gl string   [ --gitlab string, --gl string ]   GitLab project in owner/repo format or project ID
   --codeberg string, --cb string [ --codeberg string, --cb string ] Codeberg project in owner/repo format
   --ghcr string                  [ --ghcr string ]                  GHCR image or blob reference
   --regex string, -r string      [ --regex string, -r string ]      Regex patterns to select assets
   --match string, -m string      [ --match string, -m string ]      Keywords to match in asset names
   --exclude string, -e string    [ --exclude string, -e string ]    Keywords to exclude from asset names
   --yes, -y                                                         Skip prompts and select first asset (default: false)
   --output string, -o string                                        Output file or directory path
   --concurrency int, -c int                                         Concurrency limit for downloads (default: 30)
   --ghcr-api string                                                 GHCR API base URL
   --exact-case                                                      Use exact case matching for keywords (default: false)
   --extract                                                         Extract archives after download (default: false)
   --extract-dir string                                              Directory to extract archives
   --force                                                           Force overwrite existing files (default: false)
   --skip-existing                                                   Skip downloading if file exists (default: false)
   --help, -h                                                        show help
   --version, -v                                                     print the version
```

### Examples

Below are practical examples to demonstrate how to use eget2:

1.  **Download the latest release asset from a GitHub repository**:

    ```
    eget2 --github owner/repo
    ```

    This fetches the latest release asset from the specified GitHub repository.

2.  **Download a specific asset using a regex pattern**:

    ```
    eget2 --github owner/repo --regex "linux-amd64"
    ```

    This filters assets to download only those matching the linux-amd64 pattern.

3.  **Download from a GitLab repository**:

    ```
    eget2 --gitlab owner/repo
    ```

    This retrieves assets from a specified GitLab project.

4.  **Download from GHCR**:

    ```
    eget2 --ghcr "ghcr.io/pkgforge/bincache/dbin/official/dbin:HEAD-f9f3aa2-250604T111830-x86_64-linux"
    ```
    This downloads dbin from the GitHub Container Registry.

5. **Extract archives after downloading**:
    ```sh
    eget2 --github owner/repo --extract
    ```
    This automatically extracts downloaded archives to the current directory.

6.  **Use a custom output directory**:
    ```
    eget2 --github owner/repo --output /path/to/dir
    ```
    This saves the downloaded files to a specified directory.

Supported archival formats
==========================
- Supports: Github, Gitlab, Codeberg, GHCR, etc
- Filtering based on Regex, and simple matching too
- Concurrency (W.I.P)
- Etc, refer to README to read about each feature in detail
- Supported compression formats
  - brotli (.br)
  - bzip2 (.bz2)
  - flate (.zip)
  - gzip (.gz)
  - lz4 (.lz4)
  - lzip (.lz)
  - minlz (.mz)
  - snappy (.sz) and S2 (.s2)
  - xz (.xz)
  - zlib (.zz)
  - zstandard (.zst)
- Supported archive formats:
  - .zip
  - .tar (including any compressed variants like .tar.gz)
  - .rar
  - .7z

License
-------

eget2 is dual-licensed under the ISC license and the RABRMS license. Pick whichever suits your organization, project, or sense of self

Contributing
------------

Contributions are welcome! To contribute to eget2:

-   Fork the repository.

-   Make your changes.

-   Submit a pull request.


### TODOs
1. Simplify codebase, reduce LOC
2. Build for Android
3. Build for Plan 9
   - Subtasks
     1. Decouple the progressbar library from the "eget2/pkg" code
     2. Add a fetch.go and fetch_noprogressbar.go file, which specifically builds with/without the progressbar library via a go tag as well as based on GOOS
