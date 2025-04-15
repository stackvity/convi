package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// Helper function to create a temporary directory for testing
func createTempDir(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", name)
	assert.NoError(t, err, "Failed to create temp dir")
	return dir
}

// Helper function to create a temporary file with content
func createTempFile(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NoError(t, err, "Failed to create temp file")
	return path
}

// TestValidateConfig covers basic validation rules (TASK-CONVI-019)
// and advanced feature validation (TASK-CONVI-076).
func TestValidateConfig(t *testing.T) {
	// --- Test Data Setup ---
	// Create valid input dir
	validInputDir := createTempDir(t, "valid-input")
	defer os.RemoveAll(validInputDir)

	// Create valid output parent dir (output itself doesn't need to exist)
	validOutputParentDir := createTempDir(t, "valid-output-parent")
	defer os.RemoveAll(validOutputParentDir)
	validOutputDir := filepath.Join(validOutputParentDir, "output") // Output dir itself

	// Create valid template file
	validTemplateFile := createTempFile(t, validOutputParentDir, "template.tmpl", "{{ .Content }}")
	// Create an empty file to test "Input Path is a File"
	inputFileAsFile := createTempFile(t, validInputDir, "inputfile", "")

	// --- Test Cases ---
	testCases := []struct {
		name        string
		opts        Options
		expectError bool
		errorSubstr string // More specific error substring expected
	}{
		{
			name: "Valid Basic Config",
			opts: Options{
				Input:                validInputDir,
				Output:               validOutputDir,
				LargeFileThresholdMB: 10,
				LargeFileMode:        "skip",
				BinaryMode:           "skip",
				Concurrency:          4,
				Watch:                WatchConfig{Debounce: 300 * time.Millisecond}, // Initialize nested struct
			},
			expectError: false,
		},
		{
			name: "Missing Input Path",
			opts: Options{
				Output: validOutputDir,
			},
			expectError: true,
			errorSubstr: "input path cannot be empty",
		},
		{
			name: "Missing Output Path",
			opts: Options{
				Input: validInputDir,
			},
			expectError: true,
			errorSubstr: "output path cannot be empty",
		},
		{
			name: "Non-existent Input Path",
			opts: Options{
				Input:  filepath.Join(validInputDir, "nonexistent"),
				Output: validOutputDir,
			},
			expectError: true,
			errorSubstr: "does not exist", // More specific error detail
		},
		{
			name: "Input Path is a File",
			opts: Options{
				Input:  inputFileAsFile, // Use the created file path
				Output: validOutputDir,
			},
			expectError: true,
			errorSubstr: "is not a directory",
		},
		{
			name: "Output Parent Dir Non-existent",
			opts: Options{
				Input:  validInputDir,
				Output: "/nonexistent/parent/output", // Assume this parent doesn't exist
			},
			expectError: true,
			errorSubstr: "parent directory '/nonexistent/parent' for output path", // More specific
		},
		{
			name: "Negative Concurrency",
			opts: Options{
				Input:       validInputDir,
				Output:      validOutputDir,
				Concurrency: -1,
			},
			expectError: true,
			errorSubstr: "concurrency must be non-negative",
		},
		{
			name: "Invalid Large File Mode",
			opts: Options{
				Input:         validInputDir,
				Output:        validOutputDir,
				LargeFileMode: "invalid",
			},
			expectError: true,
			errorSubstr: "largeFileMode must be 'skip' or 'error'",
		},
		{
			name: "Invalid Binary Mode",
			opts: Options{
				Input:      validInputDir,
				Output:     validOutputDir,
				BinaryMode: "invalid",
			},
			expectError: true,
			errorSubstr: "binaryMode must be 'skip' or 'placeholder'",
		},
		{
			name: "Non-existent Template File",
			opts: Options{
				Input:        validInputDir,
				Output:       validOutputDir,
				TemplateFile: "/nonexistent/template.tmpl",
			},
			expectError: true,
			errorSubstr: "templateFile '/nonexistent/template.tmpl' does not exist", // More specific
		},
		{
			name: "Template File is Directory",
			opts: Options{
				Input:        validInputDir,
				Output:       validOutputDir,
				TemplateFile: validInputDir, // Use a directory as template file
			},
			expectError: true,
			errorSubstr: "templateFile", // Original check was sufficient, error message likely contains 'is a directory'
		},
		{
			name: "Invalid Front Matter Format",
			opts: Options{
				Input:  validInputDir,
				Output: validOutputDir,
				FrontMatter: FrontMatterConfig{
					Enabled: true,
					Format:  "json", // Invalid format
				},
			},
			expectError: true,
			errorSubstr: "frontMatter.format must be 'yaml' or 'toml'",
		},
		{
			name: "Invalid Front Matter Include Key",
			opts: Options{
				Input:  validInputDir,
				Output: validOutputDir,
				FrontMatter: FrontMatterConfig{
					Enabled: true,
					Format:  "yaml",
					Include: []string{"FilePath", "InvalidKey"}, // Include an invalid key
				},
			},
			expectError: true,
			errorSubstr: "frontMatter.include contains invalid key: 'InvalidKey'",
		},
		{
			name: "Valid Front Matter Config",
			opts: Options{
				Input:  validInputDir,
				Output: validOutputDir,
				FrontMatter: FrontMatterConfig{
					Enabled: true,
					Format:  "toml",
					Static:  map[string]interface{}{"author": "Test"},
					Include: []string{"FilePath", "DetectedLanguage"},
				},
			},
			expectError: false,
		},
		{
			name: "Valid Template File",
			opts: Options{
				Input:        validInputDir,
				Output:       validOutputDir,
				TemplateFile: validTemplateFile,
			},
			expectError: false,
		},
		{
			name: "Negative Watch Debounce",
			opts: Options{
				Input:     validInputDir,
				Output:    validOutputDir,
				WatchMode: true, // Must be true for debounce validation to trigger
				Watch:     WatchConfig{Debounce: -100 * time.Millisecond},
			},
			expectError: true,
			errorSubstr: "watch.debounce duration must be non-negative",
		},
		{
			name: "Zero Large File Threshold",
			opts: Options{
				Input:                validInputDir,
				Output:               validOutputDir,
				LargeFileThresholdMB: 0, // Invalid threshold
			},
			expectError: true,
			errorSubstr: "largeFileThresholdMB must be positive",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.ValidateConfig()
			if tc.expectError {
				assert.Error(t, err, "Expected an error but got none")
				if tc.errorSubstr != "" {
					assert.True(t, strings.Contains(err.Error(), tc.errorSubstr),
						fmt.Sprintf("Expected error message to contain '%s', but got: %v", tc.errorSubstr, err))
				}
			} else {
				assert.NoError(t, err, "Expected no error but got: %v", err)
			}
		})
	}
}

// TestConfigLoadingPrecedence covers TASK-CONVI-024.
// It verifies that flags override env vars, which override config files, which override defaults.
func TestConfigLoadingPrecedence(t *testing.T) {
	// --- Setup ---
	tempDir := createTempDir(t, "config-precedence")
	defer os.RemoveAll(tempDir)

	// 1. Default Value (Defined implicitly or via viper.SetDefault)
	defaultValue := 4 // Assume a default concurrency of 4 for this test

	// 2. Config File Value
	configFileValue := 8
	configContent := fmt.Sprintf("concurrency: %d\ncache: false\n", configFileValue) // also test bool
	configFile := createTempFile(t, tempDir, "precedence.yaml", configContent)

	// 3. Environment Variable Value
	envVarValue := "12" // Viper reads env vars as strings initially
	envKey := "CONVI_CONCURRENCY"
	envKeyCache := "CONVI_CACHE" // test bool override

	// 4. Flag Value
	flagValue := 16
	flagValueCache := true

	// --- Test Cases ---
	tests := []struct {
		name             string
		setupFunc        func(v *viper.Viper) // Function to set up Viper for the specific precedence test
		expectedConc     int
		expectedCacheVal bool
	}{
		{
			name: "Default Only",
			setupFunc: func(v *viper.Viper) {
				v.SetDefault("concurrency", defaultValue)
				v.SetDefault("cache", true) // Default cache is true
				// No file, env, or flag set
			},
			expectedConc:     defaultValue,
			expectedCacheVal: true,
		},
		{
			name: "File Overrides Default",
			setupFunc: func(v *viper.Viper) {
				v.SetDefault("concurrency", defaultValue)
				v.SetDefault("cache", true)
				v.SetConfigFile(configFile)
				err := v.ReadInConfig()
				assert.NoError(t, err)
			},
			expectedConc:     configFileValue,
			expectedCacheVal: false, // File sets cache to false
		},
		{
			name: "Env Overrides File",
			setupFunc: func(v *viper.Viper) {
				v.SetDefault("concurrency", defaultValue)
				v.SetDefault("cache", true)
				v.SetConfigFile(configFile)
				err := v.ReadInConfig()
				assert.NoError(t, err)
				// Set Env Vars *after* reading file
				t.Setenv(envKey, envVarValue)
				t.Setenv(envKeyCache, "true") // Env sets cache back to true
				v.AutomaticEnv()              // Re-read env vars
			},
			expectedConc:     12, // Should be parsed from string "12"
			expectedCacheVal: true,
		},
		{
			name: "Flag Overrides Env",
			setupFunc: func(v *viper.Viper) {
				v.SetDefault("concurrency", defaultValue)
				v.SetDefault("cache", true)
				v.SetConfigFile(configFile) // File sets cache=false
				err := v.ReadInConfig()
				assert.NoError(t, err)
				t.Setenv(envKey, envVarValue)  // Env sets conc=12
				t.Setenv(envKeyCache, "false") // Env sets cache=false
				v.AutomaticEnv()
				// Simulate setting flags (highest precedence) via viper.Set
				// Note: This tests Viper's precedence logic directly.
				// Testing the full Cobra->Viper binding is better suited for E2E tests.
				v.Set("concurrency", flagValue)
				v.Set("cache", flagValueCache) // Flag sets cache=true
			},
			expectedConc:     flagValue,
			expectedCacheVal: flagValueCache,
		},
		{
			name: "Flag Overrides File (No Env)",
			setupFunc: func(v *viper.Viper) {
				v.SetDefault("concurrency", defaultValue)
				v.SetDefault("cache", true)
				v.SetConfigFile(configFile) // File sets conc=8, cache=false
				err := v.ReadInConfig()
				assert.NoError(t, err)
				// Simulate setting flags via viper.Set
				v.Set("concurrency", flagValue)
				v.Set("cache", flagValueCache) // Flag sets cache=true
			},
			expectedConc:     flagValue,
			expectedCacheVal: flagValueCache,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			v.SetEnvPrefix("CONVI")
			v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

			// Apply the specific setup for this test case
			tt.setupFunc(v)

			// Unmarshal into a target struct
			var opts Options
			err := v.Unmarshal(&opts)
			assert.NoError(t, err, "Failed to unmarshal config")

			// Assert the final values match expectations
			assert.Equal(t, tt.expectedConc, opts.Concurrency, "Concurrency value mismatch")
			assert.Equal(t, tt.expectedCacheVal, opts.UseCache, "Cache value mismatch")
		})
	}
}

// Helper function to clean up environment variables after tests
func TestMain(m *testing.M) {
	// Run tests
	exitCode := m.Run()
	os.Exit(exitCode)
}
