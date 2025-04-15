package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath" // Keep filepath import
	"runtime"
	"strings"
	"syscall"
	"time"

	// "github.com/gobwas/glob" // Example library if needed for complex globbing
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/stackvity/convi/internal/cache"
	"github.com/stackvity/convi/internal/config"
	"github.com/stackvity/convi/internal/engine"
	"github.com/stackvity/convi/internal/filesystem"

	// Removed: "github.com/stackvity/convi/internal/ignore" // This package does not exist based on folder-structure.md
	"github.com/stackvity/convi/internal/template"
)

// Variables for version embedding via ldflags (TASK-CONVI-062)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Exit codes defined for clarity (TASK-CONVI-051 implication)
const (
	ExitCodeSuccess           = 0
	ExitCodeFileErrors        = 1
	ExitCodeConfigError       = 2
	ExitCodeInterrupt         = 3
	ExitCodeEngineError       = 4
	ExitCodeCacheManagerError = 5
	ExitCodeUnknown           = 10
)

var (
	opts   *config.Options
	logger *slog.Logger
)

// rootCmd represents the base command when called without any subcommands (TASK-CONVI-012)
var rootCmd = &cobra.Command{
	Use:   "convi -i <input> -o <output>",
	Short: "A fast CLI tool to convert source code to Markdown documentation",
	Long: `Convi recursively scans an input directory, converting source code files
into structured Markdown files within an output directory.

It leverages parallelism, caching, and flexible configuration options
to provide an efficient documentation generation workflow.`,
	Version: version, // TASK-CONVI-013
	RunE: func(cmd *cobra.Command, args []string) error {
		// Setup context with signal handling (TASK-CONVI-049)
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		// Configuration Loading & Validation (Orchestration - TASK-CONVI-018, TASK-CONVI-049)
		// initConfig() has already populated Viper. Now unmarshal into opts.
		if err := viper.Unmarshal(&opts); err != nil {
			// Use fmt before logger is initialized
			fmt.Fprintf(os.Stderr, "Error unmarshalling configuration: %v\n", err)
			os.Exit(ExitCodeConfigError) // Exit immediately on critical config error
			return nil                   // Unreachable
		}

		// Apply concurrency default if needed
		if opts.Concurrency == 0 {
			opts.Concurrency = runtime.NumCPU()
		}

		// Validate the loaded configuration
		if err := opts.ValidateConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			os.Exit(ExitCodeConfigError)
			return nil
		}

		// Logging Setup (TASK-CONVI-023)
		logLevel := slog.LevelInfo
		if opts.Verbose {
			logLevel = slog.LevelDebug
		}
		logHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
		logger = slog.New(logHandler)

		logger.Debug("Configuration loaded and validated successfully", "options", *opts)

		// Dependency Injection / Composition (TASK-CONVI-050)
		logger.Debug("Initializing dependencies...")
		fs := filesystem.NewRealFileSystem()
		var err error // Declare err here

		// --- Ignore Matcher ---
		// FIX: Create ignore matcher here based on config, as internal/ignore doesn't exist.
		// This is a simplified implementation using only patterns from opts.Ignore
		// and basic filepath.Match. A full implementation would also read .conviignore
		// and potentially use a more robust gitignore-style matching library.
		// TODO: Implement loading ignores from .conviignore files and use a proper gitignore matcher.
		// TODO: Handle matching relative paths correctly against patterns.
		compiledIgnorePatterns := make([]string, 0, len(opts.Ignore)) // Placeholder for compiled patterns if needed
		for _, pattern := range opts.Ignore {
			// Basic validation or compilation could happen here
			// For filepath.Match, we just use the raw patterns.
			compiledIgnorePatterns = append(compiledIgnorePatterns, pattern)
		}

		ignoreMatcher := func(path string) bool {
			// Try matching against basename first, then full path?
			// Simple approach: check against basename and the full path provided.
			// A real gitignore matcher is more complex (handles anchoring, '**', etc.)
			base := filepath.Base(path)
			for _, pattern := range compiledIgnorePatterns {
				// Check basename
				matchedBase, _ := filepath.Match(pattern, base)
				if matchedBase {
					return true
				}
				// Check full path (or relative path?) - This needs careful handling
				// filepath.Match might not be sufficient for gitignore paths.
				matchedPath, _ := filepath.Match(pattern, path)
				if matchedPath {
					return true
				}
			}
			return false
		}
		logger.Debug("Ignore matcher initialized (basic implementation)", "patterns", opts.Ignore)
		// End FIX for Ignore Matcher

		// --- Cache Manager ---
		// --- FIX: Use cache.DefaultCachePath directly where needed ---
		cacheFilePath := cache.DefaultCachePath(opts.Output) // Get the path once

		if opts.ClearCache {
			// P.2.5 logic (integrated with C.1.1 flag check)
			logger.Info("Clearing cache...", "path", cacheFilePath)
			// --- FIX: Use cacheFilePath variable ---
			if err := fs.Remove(cacheFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				logger.Error("Failed to delete cache file", "path", cacheFilePath, "error", err)
				os.Exit(ExitCodeCacheManagerError)
				return nil
			}
			logger.Info("Cache cleared.")
			// If only clearing cache, maybe exit successfully here? Depends on desired CLI behavior.
			// For now, assume clear-cache can be combined with a run.
		}

		var cm cache.CacheManager = cache.NewNoOpCacheManager() // Default to NoOp
		if opts.UseCache {
			// --- FIX: Simplified directory creation ---
			// Ensure the output directory itself exists, as the cache file will be placed inside it.
			outputDir := filepath.Clean(opts.Output) // Use the validated output path
			if err := fs.MkdirAll(outputDir, 0755); err != nil {
				// Allow ErrExist, fail on others
				if !errors.Is(err, os.ErrExist) {
					logger.Error("Failed to ensure output directory exists", "path", outputDir, "error", err)
					os.Exit(ExitCodeCacheManagerError) // Use appropriate exit code
					return nil
				}
				// If it already exists, ignore the error and continue
				logger.Debug("Output directory already exists or was created", "path", outputDir)
			} else {
				logger.Debug("Ensured output directory exists", "path", outputDir)
			}

			// --- FIX: Use cacheFilePath variable ---
			realCM, err := cache.NewFileCacheManager(cacheFilePath, fs, logger)
			if err != nil {
				logger.Warn("Failed to initialize file cache manager, falling back to no-cache behavior", "error", err)
				// Keep cm as NoOpCacheManager
			} else {
				cm = realCM
				logger.Debug("File cache manager initialized")
			}
		} else {
			logger.Info("Cache is disabled via configuration.") // Handles --no-cache
		}

		// --- Template Executor ---
		var templateExec *template.Executor
		if opts.TemplateFile != "" {
			// CO.8.1 Logic (Initialization part)
			templateExec, err = template.NewExecutor(opts.TemplateFile, fs)
			if err != nil {
				logger.Error("Failed to initialize template executor", "file", opts.TemplateFile, "error", err)
				os.Exit(ExitCodeConfigError) // Config error as template file is specified but invalid
				return nil
			}
			logger.Debug("Custom template executor initialized", "file", opts.TemplateFile)
		} else {
			logger.Debug("Using default template logic")
		}

		// --- Config/Template Hashing (for Cache - P.2.3) ---
		// Calculate hashes using functions potentially moved to internal/cache
		configHash, err := cache.CalculateConfigHash(opts) // Uses func from cache pkg
		if err != nil {
			logger.Error("Failed to calculate configuration hash for cache", "error", err)
			os.Exit(ExitCodeConfigError)
			return nil
		}
		logger.Debug("Calculated config hash for cache")

		templateHash, err := cache.CalculateTemplateHash(opts.TemplateFile, fs) // Uses func from cache pkg
		if err != nil {
			logger.Error("Failed to calculate template hash for cache", "file", opts.TemplateFile, "error", err)
			os.Exit(ExitCodeConfigError)
			return nil
		}
		logger.Debug("Calculated template hash for cache")

		// --- Engine ---
		// P.1.1 (Engine initialization logic)
		eng := engine.NewEngine(
			opts,
			fs,
			cm,
			logger,
			ignoreMatcher, // Pass the locally created matcher
			templateExec,
			configHash,
			templateHash,
		)
		logger.Debug("Engine initialized")

		// Engine Execution (TASK-CONVI-051 / Main Orchestration)
		logger.Info("Starting convi process...")
		startTime := time.Now()
		// P.1.1 (Run starts concurrent processing), WF.1 (Starts watch loop if enabled)
		report, err := eng.Run(ctx)
		duration := time.Since(startTime) // Capture duration immediately after Run returns

		// Report Summary (TASK-CONVI-051 / C.4.2 Display Summary Report)
		fmt.Fprintf(os.Stdout, "\n--- Summary ---\n")
		fmt.Fprintf(os.Stdout, "Duration: %s\n", duration.Round(time.Millisecond))
		fmt.Fprintf(os.Stdout, "Files Processed:      %d\n", report.FilesProcessed)
		fmt.Fprintf(os.Stdout, "Files From Cache:     %d\n", report.FilesFromCache)
		fmt.Fprintf(os.Stdout, "Files Skipped (Ignored): %d\n", report.FilesSkippedIgnored)
		fmt.Fprintf(os.Stdout, "Files Skipped (Binary):  %d\n", report.FilesSkippedBinary)
		fmt.Fprintf(os.Stdout, "Files Skipped (Large):   %d\n", report.FilesSkippedLarge)
		fmt.Fprintf(os.Stdout, "Files Errored:        %d\n", report.FilesErrored)

		if report.FilesErrored > 0 {
			fmt.Fprintf(os.Stdout, "\n--- Errors Encountered ---\n")
			for filePath, errMsg := range report.Errors {
				fmt.Fprintf(os.Stdout, "ERROR: %s: %s\n", filePath, errMsg)
			}
		}
		fmt.Fprintf(os.Stdout, "---------------\n")

		// Exit Code Handling (TASK-CONVI-051 / C.4.2 Exit Code Logic)
		if ctx.Err() != nil {
			// Check context cancellation first, as it overrides other errors
			logger.Info("Process interrupted.")
			os.Exit(ExitCodeInterrupt) // Exit Code 3
			return nil
		}
		if err != nil {
			logger.Error("Engine execution failed", "error", err)
			// Check if the error is context canceled, although ctx.Err() check above should catch it
			if errors.Is(err, context.Canceled) {
				os.Exit(ExitCodeInterrupt) // Exit Code 3
			} else {
				os.Exit(ExitCodeEngineError) // Exit Code 4 for other engine errors
			}
			return nil
		}
		if report.FilesErrored > 0 {
			logger.Warn("Processing completed with file errors.")
			os.Exit(ExitCodeFileErrors) // Exit Code 1
			return nil
		}

		logger.Info("Processing completed successfully.")
		// If we reach here, exit code is 0 (Success)
		// Cobra handles the default exit(0) if RunE returns nil
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// TASK-CONVI-012 (Cobra execute)
	if err := rootCmd.Execute(); err != nil {
		// Cobra often prints its own errors for flag parsing etc.
		// This catches other potential execution setup errors.
		// Using ExitCodeUnknown as Cobra errors usually exit before RunE.
		// Use fmt because logger might not be initialized.
		fmt.Fprintf(os.Stderr, "Error executing command: %v\n", err)
		os.Exit(ExitCodeUnknown)
	}
}

func init() {
	// TASK-CONVI-015 (Initialize Viper)
	// Initialize opts here so initConfig can reference it
	opts = &config.Options{}
	cobra.OnInitialize(initConfig)

	// Define persistent flags (TASK-CONVI-014 / C.1.1)
	// Required flags
	rootCmd.PersistentFlags().StringVarP(&opts.Input, "input", "i", "", "Source directory containing code files (required)")
	rootCmd.PersistentFlags().StringVarP(&opts.Output, "output", "o", "", "Output directory for generated Markdown (required)")

	// Configuration flags
	rootCmd.PersistentFlags().StringVarP(&opts.ConfigFile, "config", "c", "", "Configuration file path (default: .convi.yaml, convi.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Enable verbose debug logging")
	rootCmd.PersistentFlags().BoolVarP(&opts.Force, "force", "f", false, "Skip safety prompt when output directory is not empty") // C.5.1 control

	// Feature flags
	rootCmd.PersistentFlags().StringSliceVar(&opts.Ignore, "ignore", []string{}, "Glob patterns for files/directories to ignore (can be repeated)") // C.3.1 source
	rootCmd.PersistentFlags().IntVar(&opts.Concurrency, "concurrency", 0, "Number of parallel workers (0 for auto-detect CPU cores)")               // P.1.1 control
	rootCmd.PersistentFlags().BoolVar(&opts.UseCache, "cache", true, "Use file cache to speed up subsequent runs (use --no-cache to disable)")      // P.2 control
	rootCmd.PersistentFlags().BoolVar(&opts.ClearCache, "clear-cache", false, "Clear the cache file before running")                                // P.2.5 control
	rootCmd.PersistentFlags().StringVar(&opts.TemplateFile, "template", "", "Path to a custom Go template file for Markdown output")                // CO.8.1 control
	rootCmd.PersistentFlags().IntVar(&opts.LargeFileThresholdMB, "large-file-threshold", 100, "Size threshold in MB to consider a file large")      // CO.7.1 control
	rootCmd.PersistentFlags().StringVar(&opts.LargeFileMode, "large-file-mode", "skip", "How to handle large files: 'skip' or 'error'")             // CO.7.1 control
	rootCmd.PersistentFlags().StringVar(&opts.BinaryMode, "binary-mode", "skip", "How to handle binary files: 'skip' or 'placeholder'")             // CO.6.1 control
	rootCmd.PersistentFlags().BoolVar(&opts.SkipHiddenFiles, "skip-hidden-files", true, "Skip files and directories starting with '.'")             // TASK-CONVI-WWA control

	// Watch Mode Flag **(Corrected Binding)**
	rootCmd.PersistentFlags().BoolVar(&opts.WatchMode, "watch", false, "Enable watch mode for continuous updates") // WF.1 control

	// Watch Debounce setting (now part of nested config)
	// We bind the nested structure later in initConfig or handle it manually from Viper
	// Binding simple flags like duration directly to nested structs with BindPFlags can be tricky.
	// For now, let Viper handle loading it from file/env into the nested struct.

	// Add --no-cache flag as the negation of --cache
	rootCmd.PersistentFlags().Bool("no-cache", false, "Disable reading from the cache (equivalent to --cache=false)") // P.2 control
	// Need to handle the relationship between --cache and --no-cache in initConfig or RunE

	// Setup version flag (TASK-CONVI-013 / C.1.1 / TASK-CONVI-062)
	rootCmd.SetVersionTemplate(fmt.Sprintf("convi version %s (commit: %s, built: %s)\n", version, commit, date))

	// TASK-CONVI-UUA (Help text generated automatically by Cobra from flag descriptions)

	// Mark required flags
	// It's often better to do validation in RunE for more control over error messages
	// and combining config sources, rather than using MarkPersistentFlagRequired.
	// Cobra's required flag check happens very early.
	// _ = rootCmd.MarkPersistentFlagRequired("input")
	// _ = rootCmd.MarkPersistentFlagRequired("output")
}

// initConfig reads in config file and ENV variables if set. (TASK-CONVI-015 / C.2.1)
func initConfig() {
	v := viper.New()

	// 1. Set Defaults (can be done here or using viper.SetDefault)
	// Example defaults matching flag defaults
	v.SetDefault("concurrency", 0)
	v.SetDefault("cache", true)
	v.SetDefault("largeFileThresholdMB", 100)
	v.SetDefault("largeFileMode", "skip")
	v.SetDefault("binaryMode", "skip")
	v.SetDefault("skipHiddenFiles", true)
	v.SetDefault("watch.debounce", "300ms") // Default for nested struct

	// 2. Bind Environment Variables
	v.AutomaticEnv() // Read in environment variables that match
	v.SetEnvPrefix("CONVI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_")) // Replace . and - with _ in env var names

	// 3. Read Config File
	if opts.ConfigFile != "" {
		// Use config file from the flag.
		v.SetConfigFile(opts.ConfigFile)
		if err := v.ReadInConfig(); err != nil {
			// Handle error reading the explicitly specified config file
			fmt.Fprintf(os.Stderr, "Error reading specified config file %s: %v\n", opts.ConfigFile, err)
			os.Exit(ExitCodeConfigError)
		} else {
			// Use fmt before logger is potentially initialized
			if verboseFlag := rootCmd.PersistentFlags().Lookup("verbose"); verboseFlag == nil || !verboseFlag.Changed || !viper.GetBool("verbose") {
				// Only print this if verbose isn't explicitly set or is false, to reduce noise.
				// This check is imperfect as verbose could be set in the config file itself.
				// A cleaner way might be to check viper.GetBool("verbose") *after* loading.
				// For now, keep it simple.
			} else {
				fmt.Fprintf(os.Stderr, "Using config file: %s\n", v.ConfigFileUsed())
			}

		}
	} else {
		// Search for config in current directory with name ".convi" or "convi" (without extension).
		v.AddConfigPath(".")
		v.SetConfigName(".convi") // Add both names Viper will search for
		v.SetConfigName("convi")
		v.SetConfigType("yaml") // Specify type since name might have no extension

		// Attempt to read the config file found in search paths
		if err := v.ReadInConfig(); err == nil {
			// Use fmt before logger is initialized potentially
			if verboseFlag := rootCmd.PersistentFlags().Lookup("verbose"); verboseFlag == nil || !verboseFlag.Changed || !viper.GetBool("verbose") {
				// Similarly, reduce noise if not verbose
			} else {
				fmt.Fprintf(os.Stderr, "Using config file: %s\n", v.ConfigFileUsed())
			}
		} else {
			// Ignore "file not found" errors if no specific file was requested
			// Only error out on other read errors (e.g., permissions, bad format)
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				// Config file was found but another error occurred
				fmt.Fprintf(os.Stderr, "Error reading config file %s: %v\n", v.ConfigFileUsed(), err)
				os.Exit(ExitCodeConfigError) // Treat parsing errors as fatal
			}
			// else: Config file not found, which is fine if not specified via flag.
		}
	}

	// 4. Bind Cobra Flags (Highest Precedence if set)
	// Do this *after* reading config file and setting defaults/env vars
	// so that flags correctly override other sources.
	if err := v.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		fmt.Fprintf(os.Stderr, "Internal error binding flags to viper: %v\n", err)
		os.Exit(ExitCodeConfigError)
	}

	// 5. Handle --no-cache overriding --cache explicitly *after* all loading
	// Viper's bool flag handling can be tricky with negations.
	// Check if the --no-cache flag was explicitly set on the command line.
	if noCacheFlag := rootCmd.PersistentFlags().Lookup("no-cache"); noCacheFlag != nil && noCacheFlag.Changed {
		// If --no-cache was used on command line, it overrides everything, setting UseCache to false.
		v.Set("cache", false) // Use the key matching the field/flag name
	}
	// Note: If --no-cache is not used, the value of 'cache' will be determined by
	// the --cache flag (bound in step 4), then CONVI_CACHE env var, then config file, then the default (true).

	// 6. Merge into the global viper instance (used by RunE's Unmarshal)
	// This ensures the precedence order is maintained: Flags > Env > Config File > Defaults
	if err := viper.MergeConfigMap(v.AllSettings()); err != nil {
		fmt.Fprintf(os.Stderr, "Internal error merging viper settings: %v\n", err)
		os.Exit(ExitCodeConfigError)
	}
}

func main() {
	Execute()
}
