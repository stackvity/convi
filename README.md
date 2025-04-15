# convi

[![Go Report Card](https://goreportcard.com/badge/github.com/stackvity/convi)](https://goreportcard.com/report/github.com/stackvity/convi)
[![Go Reference](https://pkg.go.dev/badge/github.com/stackvity/convi.svg)](https://pkg.go.dev/github.com/stackvity/convi)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![CI Status](https://github.com/stackvity/convi/actions/workflows/ci.yml/badge.svg)](https://github.com/stackvity/convi/actions/workflows/ci.yml)

<!-- [![codecov](https://codecov.io/gh/stackvity/convi/branch/main/graph/badge.svg)](https://codecov.io/gh/stackvity/convi) -->
<!-- [![Release](https://img.shields.io/github/v/release/stackvity/convi)](https://github.com/stackvity/convi/releases/latest) -->

**Convi** is a fast, reliable, and straightforward open-source CLI tool designed for the automated, recursive generation of structured Markdown documentation directly from local source code directories.

It focuses on core conversion functionality, excellent performance through concurrency and robust caching, predictable behavior through defined configuration and error handling, and ease of use via a simple CLI.

## Features

- **Recursive Conversion:** Scans input directories and creates corresponding `.md` files.
- **Structure Preservation:** Mirrors the input directory structure in the output directory.
- **Markdown Generation:** Wraps source code in fenced code blocks (```) with detected language tags.
- **Language Detection:** Identifies languages based on file extensions (with overrides and optional content fallback).
- **Parallel Processing:** Utilizes multiple CPU cores for faster conversion (`--concurrency`).
- **Robust Caching:** Skips unchanged files based on metadata and content hashes (source, template, config). Cache control flags (`--cache`, `--no-cache`, `--clear-cache`) provided.
- **Informative Output:** Displays progress (TTY) and a detailed summary report (counts, errors, duration). Structured verbose logging (`-v`).
- **Configuration:** Layered config (Flags > Env > File > Defaults) via `viper`. Supports `convi.yaml` files.
- **Ignoring Files:** Uses `.gitignore` syntax via `.conviignore` files, config (`ignore` list), and `--ignore` flags.
- **File Handling Modes:** Configurable behavior for binary (`--binary-mode`) and large files (`--large-file-mode`).
- **Customization:** Supports custom Go templates (`--template`) and optional YAML/TOML front matter generation (`frontMatter` config).
- **Watch Mode:** Continuously monitors for file changes and triggers incremental rebuilds (`--watch`).
- **Safety:** Prompts before overwriting non-empty output directories (`--force` to bypass).

## Installation

### 1. Binary Download (Recommended)

1.  Go to the [Latest Release](https://github.com/stackvity/convi/releases/latest) page.
2.  Download the appropriate archive (`.tar.gz` or `.zip`) for your operating system and architecture (e.g., `convi-linux-amd64.tar.gz`, `convi-windows-amd64.zip`).
3.  Extract the archive.
4.  Place the `convi` (or `convi.exe`) binary in a directory included in your system's `PATH`.
5.  Verify installation: `convi --version`

### 2. Using `go install`

Requires Go (>= 1.19 recommended) to be installed.

```bash
go install github.com/stackvity/convi@latest
```

Ensure your Go binary path (`$GOPATH/bin` or `$HOME/go/bin`) is in your system `PATH`.

### 3. Build from Source

Requires Go (>= 1.19 recommended) and Git.

```bash
git clone https://github.com/stackvity/convi.git
cd convi
# Optional: Check out a specific version tag
# git checkout vX.Y.Z
go mod tidy
go build -ldflags="-s -w" -o convi .
# Optionally move the 'convi' binary to your PATH
./convi --version
```

## Usage

### Basic Syntax

```bash
convi -i <source_directory> -o <destination_directory> [flags]
```

### Examples

1.  **Simple Conversion:** Convert files from `./src` to Markdown in `./docs`.

    ```bash
    convi -i ./src -o ./docs
    ```

2.  **Watch Mode:** Monitor `./app` for changes, output to `./build/docs`, use 4 workers.

    ```bash
    convi -i ./app -o ./build/docs --watch --concurrency 4
    ```

3.  **Force Rebuild & Verbose Logging:** Process current directory (`.`) to `./dist/docs`, ignore cache, force overwrite, show debug logs.

    ```bash
    convi -i . -o ./dist/docs --force --no-cache -v
    ```

4.  **Custom Template & Ignores:** Use a custom template, ignore `node_modules` and `.log` files.

    ```bash
    convi -i . -o ./output --template ./mytemplate.md --ignore "**/node_modules/**" --ignore "*.log"
    ```

5.  **Configuration File:** Use settings defined in `myconfig.yaml`.

    ```bash
    convi -i ./src -o ./docs --config myconfig.yaml
    ```

6.  **Clear Cache:** Delete the cache file before running.
    ```bash
    convi -i ./src -o ./docs --clear-cache
    ```

### Key Flags

Run `convi --help` for a full list of flags, descriptions, default values, and corresponding environment variables.

- `-i, --input DIR`: **Required.** Source directory. (Env: `CONVI_INPUT`)
- `-o, --output DIR`: **Required.** Output directory. (Env: `CONVI_OUTPUT`)
- `--config FILE`: Path to config file (default: `convi.yaml`, `.convi.yaml`).
- `--ignore PATTERN`: Glob pattern for files/directories to ignore (can be repeated).
- `--concurrency N`: Number of parallel workers (0 = auto-detect CPU cores). (Env: `CONVI_CONCURRENCY`)
- `--cache / --no-cache`: Enable/disable reading the cache (default: enabled).
- `--clear-cache`: Clear the cache file before running.
- `--template FILE`: Path to a custom Go template file for Markdown output.
- `--large-file-threshold MB`: Size threshold in MB to consider a file large (default: 100).
- `--large-file-mode MODE`: How to handle large files: `skip` or `error` (default: `skip`).
- `--binary-mode MODE`: How to handle binary files: `skip` or `placeholder` (default: `skip`).
- `--watch`: Enable watch mode for continuous updates.
- `-v, --verbose`: Enable verbose debug logging.
- `-f, --force`: Skip safety prompt when output directory is not empty.
- `--help`: Show help message.
- `--version`: Show application version.

## Configuration

`convi` can be configured via a file (e.g., `convi.yaml`), environment variables, and command-line flags. Precedence: Flags > Env Vars > Config File > Defaults.

### Configuration File (`convi.yaml`)

Place a `convi.yaml`, `.convi.yaml`, `convi.json`, or `convi.toml` file in the directory where you run `convi`, or specify a path using `--config`.

**Example (`convi.yaml`):**

```yaml
# --- Convi Configuration ---

# Number of parallel workers (0 for auto-detect CPU cores)
concurrency: 0

# Use file cache (true/false). Disable reads with --no-cache flag.
cache: true

# Large file handling
largeFileThresholdMB: 100 # Size limit in MB
largeFileMode: "skip" # "skip" or "error"

# Binary file handling
binaryMode: "skip" # "skip" or "placeholder"

# Path to a custom Go template file
templateFile: ""

# Language detection overrides (map[".ext"] = "language-name")
languageMappings:
  ".myext": "xml"
  # ".abc": "prolog"

# Front Matter Generation
frontMatter:
  enabled: false
  format: "yaml" # "yaml" or "toml"
  static:
    generator: "Convi" # Example static field
    # another_static_field: value
  include: # List of dynamic fields to include
    - "FilePath"
    - "DetectedLanguage"

# Glob patterns for files/directories to ignore (uses .gitignore syntax)
ignore:
  - ".git/"
  - "node_modules/"
  - "dist/"
  - "*.tmp"
  - "coverage.out"
  - ".convi.cache"

# Watch mode settings
watch:
  debounce: "300ms" # Time to wait after last change before rebuilding

# Skip hidden files/directories (starting with '.')
skipHiddenFiles: true
```

### `.conviignore` File

Similar to `.gitignore`, place a `.conviignore` file in your input directory or any parent directory. `convi` will search upwards and load patterns from all found files. Patterns use standard `.gitignore` syntax.

```gitignore
# Example .conviignore file

# Ignore build artifacts
build/
*.o
*.exe

# Ignore log files
*.log

# Ignore specific directories
vendor/
```

## Troubleshooting

- **Incorrect Output:** Use `--clear-cache` to rule out cache issues. Use `-v` to see detailed logs about skipping, processing steps, and cache decisions.
- **Errors:** Check the summary report for specific file errors. Check logs (`-v`) for more context. Ensure correct file permissions.
- **Performance:** Use `--concurrency` to adjust parallelism. Ensure caching is enabled (default).
- **Watch Mode Issues:** Check `fsnotify` limitations for your OS/filesystem. Ensure adequate debounce time (`watch.debounce` in config).

## Contributing

Contributions are welcome! Please see the [CONTRIBUTING.md](CONTRIBUTING.md) file for guidelines.

## License

`convi` is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
