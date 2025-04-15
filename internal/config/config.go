package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Defines the valid dynamic keys allowed in the frontMatter.include configuration.
// Using a constant ensures consistency and simplifies maintenance if keys are added/removed.
var validFrontMatterIncludeKeys = map[string]bool{
	"FilePath":         true,
	"DetectedLanguage": true,
}

// FrontMatterConfig holds configuration for front matter generation.
type FrontMatterConfig struct {
	Enabled bool                   `mapstructure:"enabled"`
	Format  string                 `mapstructure:"format"` // "yaml" or "toml"
	Static  map[string]interface{} `mapstructure:"static"`
	Include []string               `mapstructure:"include"` // List of dynamic fields like "FilePath", "DetectedLanguage"
}

// WatchConfig holds configuration specific to watch mode.
type WatchConfig struct {
	Debounce time.Duration `mapstructure:"debounce"`
}

// Options holds all the configuration settings for the convi application.
// Tags are used by Viper for unmarshalling from config files, env vars, and flags.
type Options struct {
	// Input/Output
	Input  string `mapstructure:"input"`
	Output string `mapstructure:"output"`

	// Behavior Control
	Force     bool `mapstructure:"force"`
	Verbose   bool `mapstructure:"verbose"`
	WatchMode bool `mapstructure:"watch"` // The flag controlling if watch mode is enabled.

	// Ignoring Files
	Ignore          []string `mapstructure:"ignore"`          // Patterns from config file/flags
	SkipHiddenFiles bool     `mapstructure:"skipHiddenFiles"` // Added via TASK-CONVI-WWA

	// Performance
	Concurrency int  `mapstructure:"concurrency"`
	UseCache    bool `mapstructure:"cache"` // Renamed from 'Cache' to avoid ambiguity with cache control flags
	// Note: ClearCache is primarily a CLI action flag, not usually in config file.
	ClearCache bool `mapstructure:"clearCache"` // Added for completeness, though primarily CLI driven

	// File Handling Modes
	LargeFileThresholdMB int    `mapstructure:"largeFileThresholdMB"`
	LargeFileMode        string `mapstructure:"largeFileMode"` // "skip" or "error"
	BinaryMode           string `mapstructure:"binaryMode"`    // "skip" or "placeholder"

	// Customization
	TemplateFile     string            `mapstructure:"templateFile"`
	LanguageMappings map[string]string `mapstructure:"languageMappings"` // map[".ext"] = "language-name"
	FrontMatter      FrontMatterConfig `mapstructure:"frontMatter"`

	// Watch Specific Settings
	Watch WatchConfig `mapstructure:"watchConfig"` // *** UPDATED TAG HERE *** Nested struct for watch-specific options like debounce.

	// Internal - Not typically set by user directly
	ConfigFile string `mapstructure:"config"` // Path to the config file used
}

// Default values for Options can be set during Viper initialization.

// ValidateConfig checks the loaded configuration options for validity.
// TASK-CONVI-019: Implement basic validation (Input/Output required).
// TASK-CONVI-076: Update validation for advanced features (template path, front matter, watch debounce).
func (opts *Options) ValidateConfig() error {
	var errs []string

	// Basic Required Fields
	if strings.TrimSpace(opts.Input) == "" {
		errs = append(errs, "input path cannot be empty")
	} else {
		// Check if input path exists and is readable (basic check)
		info, err := os.Stat(opts.Input)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Sprintf("input path '%s' does not exist", opts.Input))
			} else {
				errs = append(errs, fmt.Sprintf("cannot access input path '%s': %v", opts.Input, err))
			}
		} else if !info.IsDir() {
			errs = append(errs, fmt.Sprintf("input path '%s' is not a directory", opts.Input))
		}
		// Note: Deeper permission checks might happen during execution
	}

	if strings.TrimSpace(opts.Output) == "" {
		errs = append(errs, "output path cannot be empty")
	} else {
		// Check if output path's parent directory exists and is writable (basic check)
		// This ensures convi can create the output directory itself if it doesn't exist.
		parentDir := filepath.Dir(opts.Output)
		info, err := os.Stat(parentDir)
		if err != nil {
			// Allow creation if parent is root or current dir, otherwise parent must exist.
			// Special case: If output is "." or a relative path in the current dir, Stat(".") is fine.
			absOut, _ := filepath.Abs(opts.Output) // Ignore error for validation purpose
			absParent := filepath.Dir(absOut)
			cwd, _ := os.Getwd()
			if parentDir != "." && parentDir != "/" && parentDir != "\\" && absParent != cwd { // Check more thoroughly
				if errors.Is(err, os.ErrNotExist) {
					errs = append(errs, fmt.Sprintf("parent directory '%s' for output path '%s' must exist to create the output directory", parentDir, opts.Output))
				} else {
					errs = append(errs, fmt.Sprintf("cannot access parent directory '%s' for output path: %v", parentDir, err))
				}
			} // else: parent exists or is implicitly creatable (like ".")
		} else if !info.IsDir() {
			// If parent path exists but isn't a directory, it's an error.
			errs = append(errs, fmt.Sprintf("parent path '%s' for output path '%s' is not a directory", parentDir, opts.Output))
		}
		// Note: Actual write permission check happens during file writing.
	}

	// Performance related validation
	if opts.Concurrency < 0 {
		errs = append(errs, "concurrency must be non-negative (0 for auto)")
	}

	// File Handling Modes validation
	if opts.LargeFileThresholdMB <= 0 {
		errs = append(errs, "largeFileThresholdMB must be positive")
	}
	if opts.LargeFileMode != "skip" && opts.LargeFileMode != "error" {
		errs = append(errs, "largeFileMode must be 'skip' or 'error'")
	}
	if opts.BinaryMode != "skip" && opts.BinaryMode != "placeholder" {
		errs = append(errs, "binaryMode must be 'skip' or 'placeholder'")
	}

	// Customization validation (TASK-CONVI-076)
	if opts.TemplateFile != "" {
		info, err := os.Stat(opts.TemplateFile)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Sprintf("templateFile '%s' does not exist", opts.TemplateFile))
			} else {
				errs = append(errs, fmt.Sprintf("cannot access templateFile '%s': %v", opts.TemplateFile, err))
			}
		} else if info.IsDir() {
			errs = append(errs, fmt.Sprintf("templateFile '%s' is a directory, not a file", opts.TemplateFile))
		}
	}

	if opts.FrontMatter.Enabled {
		if opts.FrontMatter.Format != "yaml" && opts.FrontMatter.Format != "toml" {
			errs = append(errs, "frontMatter.format must be 'yaml' or 'toml' when enabled")
		}
		// Validate included keys against the predefined allowed set.
		for _, key := range opts.FrontMatter.Include {
			if !validFrontMatterIncludeKeys[key] {
				// Build a string of valid keys for the error message
				validKeys := make([]string, 0, len(validFrontMatterIncludeKeys))
				for k := range validFrontMatterIncludeKeys {
					validKeys = append(validKeys, k)
				}
				errs = append(errs, fmt.Sprintf("frontMatter.include contains invalid key: '%s' (valid keys: %s)", key, strings.Join(validKeys, ", ")))
			}
		}
	}

	// Watch mode validation (TASK-CONVI-076)
	// Note: opts.WatchMode (the boolean) doesn't need validation itself.
	// Validate settings within the nested WatchConfig struct only if WatchMode is potentially enabled.
	if opts.WatchMode { // Or maybe validate debounce always if set? Simpler to validate if watch mode is active.
		if opts.Watch.Debounce < 0 {
			// Allow 0 (which might indicate a default later), but not negative.
			errs = append(errs, "watch.debounce duration must be non-negative")
		}
	}

	// Assemble final error
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}

	return nil
}
