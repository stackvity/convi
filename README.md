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
- **Configuration:** Layered config (Flags > Env > File > Defaults) via `viper`. Supports `convi.yaml` / `.convi.yaml` files.
- **Ignoring Files:** Uses `.gitignore` syntax via `.conviignore` files, config (`ignore` list), and `--ignore` flags.
- **File Handling Modes:** Configurable behavior for binary (`--binary-mode`) and large files (`--large-file-mode`).
- **Customization:** Supports custom Go templates (`--template`) and optional YAML/TOML front matter generation (`frontMatter` config).
- **Watch Mode:** Continuously monitors for file changes and triggers incremental rebuilds (`--watch`).
- **Safety:** Prompts before overwriting non-empty output directories (`--force` to bypass).

## Installation

Choose the method that best suits your needs:

### 1. Binary Download (Recommended for Users)

1.  Go to the [Latest Release](https://github.com/stackvity/convi/releases/latest) page.
2.  Download the appropriate archive (`.tar.gz` or `.zip`) for your operating system and architecture (e.g., `convi-linux-amd64.tar.gz`, `convi-windows-amd64.zip`).
3.  Extract the archive. You will find a `convi` (or `convi.exe`) executable file.
4.  **Important:** Place the extracted `convi` (or `convi.exe`) binary in a directory included in your system's `PATH` environment variable. This allows you to run `convi` from any directory. Common locations include `/usr/local/bin` (Linux/macOS) or a custom directory you add to your PATH.
5.  Verify installation by opening a _new_ terminal window and running:
    ```bash
    convi --version
    ```

### 2. Using `go install` (for Go Developers)

Requires Go (>= 1.21 recommended, check `go.mod` for exact version) to be installed.

1.  Run the command:
    ```bash
    go install github.com/stackvity/convi@latest
    ```
2.  **Important:** Ensure your Go binary path (`$(go env GOPATH)/bin` or `$HOME/go/bin` by default) is included in your system's `PATH` environment variable. If not, add it to your shell profile (`.bashrc`, `.zshrc`, `.profile`, etc.) and restart your shell or source the profile.
3.  Verify installation by opening a _new_ terminal window and running:
    ```bash
    convi --version
    ```

### 3. Build from Source (for Developers/Contributors)

Requires Go (>= 1.21 recommended, check `go.mod`) and Git.

1.  Clone the repository:
    ```bash
    git clone https://github.com/stackvity/convi.git
    ```
2.  Navigate into the project directory:
    ```bash
    cd convi
    ```
3.  _(Optional: Check out a specific version tag using `git checkout vX.Y.Z`)_
4.  Ensure dependencies are up-to-date:
    ```bash
    go mod tidy
    ```
5.  Build the executable:
    ```bash
    go build .
    ```
    This creates an executable file named `convi` (or `convi.exe` on Windows) **in the current directory (`./`)**.
    _(Note: Official release builds use specific flags like `-ldflags="-s -w"` for optimization and version embedding)._
6.  **Run the local build:** Since the current directory might not be in your system's `PATH`, execute the binary using its relative path:
    ```bash
    ./convi --version
    ```
    Or, to run it with arguments:
    ```bash
    ./convi -i <input_dir> -o <output_dir>
    ```
7.  _(Optional)_ Move the compiled `./convi` binary to a directory in your system's `PATH` if you want to run it from anywhere using just `convi`.

## Usage

### Basic Syntax

```bash
# If installed to PATH:
convi -i <source_directory> -o <destination_directory> [flags]

# If running a local build from source directory:
./convi -i <source_directory> -o <destination_directory> [flags]
```

**Note:** `-i` and `-o` are required flags.

### Examples

_(Assuming `convi` is installed in your PATH)_

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

5.  **Configuration File:** Use settings defined in `myconfig.yaml` located in the current directory.

    ```bash
    convi -i ./src -o ./docs --config myconfig.yaml
    ```

6.  **Clear Cache:** Delete the cache file before running.
    ```bash
    convi -i ./src -o ./docs --clear-cache
    ```

### Key Flags

Run `convi --help` for a full list of flags, descriptions, default values, and corresponding environment variables.

- `-i, --input DIR`: **Required.** Source directory containing code files. (Env: `CONVI_INPUT`)
- `-o, --output DIR`: **Required.** Output directory for generated Markdown. (Env: `CONVI_OUTPUT`)
- `--config FILE`: Path to config file (default searches for `convi.yaml` or `.convi.yaml` in the current directory).
- `--ignore PATTERN`: Glob pattern for files/directories to ignore (can be repeated).
- `--concurrency N`: Number of parallel workers (0 = auto-detect CPU cores). (Env: `CONVI_CONCURRENCY`)
- `--cache / --no-cache`: Enable/disable reading the cache (default: enabled). (Env: `CONVI_CACHE` true/false)
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

Place a `convi.yaml`, `.convi.yaml`, `convi.json`, or `convi.toml` file in the directory where you run `convi`. `convi` searches the current directory by default. You can specify a different path using `--config FILEPATH`.

**Example (`convi.yaml`):**

```yaml
# --- Convi Configuration ---

# Number of parallel workers (0 for auto-detect CPU cores)
concurrency: 0 # Default: 0 (auto)

# Use file cache (true/false). Disable reads with --no-cache flag.
cache: true # Default: true

# Large file handling
largeFileThresholdMB: 100 # Default: 100
largeFileMode: "skip" # Default: "skip". Options: "skip", "error"

# Binary file handling
binaryMode: "skip" # Default: "skip". Options: "skip", "placeholder"

# Path to a custom Go template file
templateFile: "" # Default: "" (uses built-in generation)

# Language detection overrides (map[".ext"] = "language-name")
languageMappings: # Default: empty map
  ".myext": "xml"
  # ".abc": "prolog"

# Front Matter Generation
frontMatter: # Default: disabled
  enabled: false
  format: "yaml" # Default: "yaml". Options: "yaml", "toml"
  static:
    generator: "Convi" # Example static field
    # another_static_field: value
  include: # List of dynamic fields to include
    - "FilePath"
    - "DetectedLanguage"

# Glob patterns for files/directories to ignore (uses .gitignore syntax)
ignore: # Default: empty list
  - ".git/"
  - "node_modules/"
  - "dist/"
  - "*.tmp"
  - "coverage.out"
  - ".convi.cache"

# Watch mode settings
watchConfig: # Default: debounce 300ms
  debounce: "300ms" # Time to wait after last change before rebuilding

# Skip hidden files/directories (starting with '.')
skipHiddenFiles: true # Default: true
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

- **`convi: command not found`**:
  - If installed via **binary download** or **`go install`**: Ensure the directory containing the `convi` executable is in your system's `PATH` environment variable. Open a _new_ terminal after modifying your PATH.
  - If installed via **build from source**: Run the command using the relative path: `./convi [args...]` from within the project directory where you built it.
- **Incorrect Output:** Use `--clear-cache` to rule out cache issues. Use `-v` to see detailed logs about skipping, processing steps, and cache decisions.
- **Errors During Run:**
  - **Configuration Errors (e.g., `Error unmarshalling configuration...`, `Error reading config file...`):**
    - Check your `convi.yaml` (or other config file) for valid YAML/JSON/TOML syntax.
    - Ensure configuration values match expected types and constraints (see `convi --help` or the example `convi.yaml`).
    - If you don't intend to use a config file, ensure there isn't a file named `convi` (without extension) or `.convi` in your current directory that might be conflicting with the executable name (especially if running `./convi` locally). You can create an empty `convi.yaml` or use `--config /dev/null` (Linux/macOS) to prevent accidental loading.
    - Check file permissions if using `--config` with a specific file.
  - **File Processing Errors (Exit Code 1):** Check the "Errors Encountered" list in the summary report for specific file paths and error messages. Check file permissions, encoding, or potential issues with large/binary file settings. Use `-v` for more detailed logs about the specific file failure.
- **Performance:** Use `--concurrency N` to adjust parallelism. Ensure caching is enabled (`--cache` or default). Use `--no-cache` to diagnose if caching is causing unexpected behavior.
- **Watch Mode Issues:** Check `fsnotify` limitations for your OS/filesystem (especially network drives). Ensure adequate debounce time (`watchConfig.debounce` in config). Use `-v` to see file events being detected.

## Contributing

Contributions are welcome! Please see the [CONTRIBUTING.md](CONTRIBUTING.md) file for guidelines on reporting bugs, proposing features, and submitting pull requests.

## License

`convi` is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
