package worker

import (
	"bytes" // Import bytes for logger buffer if needed
	"context"
	"errors"
	"fmt"
	"io" // Import io for io.Discard
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings" // Import strings for TestWorker_ProcessFile_WithFrontMatter assertions
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	// "github.com/stretchr/testify/require" // 'require' is not used, commented out

	"github.com/stackvity/convi/internal/cache"
	"github.com/stackvity/convi/internal/config"
	"github.com/stackvity/convi/internal/template"
)

// --- Mock Implementations (Worker Test Specific) ---

// MockFileSystem (Re-using pattern from cache_test.go)
type MockFileSystem struct {
	mock.Mock
}

// Implement required FileSystem methods for worker tests
func (mfs *MockFileSystem) ReadFile(name string) ([]byte, error) {
	args := mfs.Called(name)
	content := args.Get(0)
	if content == nil {
		// Return nil slice if configured error is nil, otherwise return nil slice and error
		err := args.Error(1)
		if err != nil {
			return nil, err
		}
		// If Get(0) was nil and Error(1) was nil, return empty slice and nil error
		// Or return specific error like os.ErrNotExist if desired for nil case
		return []byte{}, nil // Default to empty content if no error specified
	}
	return content.([]byte), args.Error(1)
}

func (mfs *MockFileSystem) WriteFile(name string, data []byte, perm fs.FileMode) error {
	args := mfs.Called(name, data, perm)
	return args.Error(0)
}

func (mfs *MockFileSystem) Stat(name string) (fs.FileInfo, error) {
	args := mfs.Called(name)
	info := args.Get(0)
	if info == nil {
		return nil, args.Error(1)
	}
	// Ensure we return fs.FileInfo, not os.FileInfo if the interface requires it
	fileInfo, ok := info.(fs.FileInfo)
	if !ok {
		// Handle case where the mock was setup with the wrong type, perhaps return an error
		return nil, fmt.Errorf("mock for Stat returned incorrect type: %T", info)
	}
	return fileInfo, args.Error(1)
}

func (mfs *MockFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	args := mfs.Called(path, perm)
	return args.Error(0)
}

// Implement other FileSystem methods if needed by worker logic under test
func (mfs *MockFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	args := mfs.Called(root, fn)
	return args.Error(0)
}

func (mfs *MockFileSystem) Remove(name string) error {
	args := mfs.Called(name)
	return args.Error(0)
}

func (mfs *MockFileSystem) Rename(oldpath, newpath string) error {
	args := mfs.Called(oldpath, newpath)
	return args.Error(0)
}

func (mfs *MockFileSystem) Chtimes(name string, atime time.Time, mtime time.Time) error {
	args := mfs.Called(name, atime, mtime)
	return args.Error(0)
}

// MockFileInfo (Re-using from cache_test.go)
type MockFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (mfi *MockFileInfo) Name() string       { return mfi.name }
func (mfi *MockFileInfo) Size() int64        { return mfi.size }
func (mfi *MockFileInfo) Mode() os.FileMode  { return mfi.mode }
func (mfi *MockFileInfo) ModTime() time.Time { return mfi.modTime }
func (mfi *MockFileInfo) IsDir() bool        { return mfi.isDir }
func (mfi *MockFileInfo) Sys() interface{}   { return nil } // Return nil for Sys() unless specific sys data is needed

// Mock CacheManager (Re-using pattern)
type MockCacheManager struct {
	mock.Mock
}

func (m *MockCacheManager) Check(filePath string, currentConfigHash []byte, currentTemplateHash []byte) (cache.CacheStatus, error) {
	args := m.Called(filePath, currentConfigHash, currentTemplateHash)
	status, _ := args.Get(0).(cache.CacheStatus) // Handle potential nil safely
	return status, args.Error(1)
}

func (m *MockCacheManager) Update(filePath string, entry cache.CacheEntry) error {
	args := m.Called(filePath, entry)
	return args.Error(0)
}
func (m *MockCacheManager) Persist() error {
	args := m.Called()
	return args.Error(0)
}
func (m *MockCacheManager) Clear() error {
	args := m.Called()
	return args.Error(0)
}

// Mock Template Executor
type MockTemplateExecutor struct {
	mock.Mock
}

func (m *MockTemplateExecutor) Execute(data map[string]interface{}) (string, error) {
	args := m.Called(data)
	return args.String(0), args.Error(1)
}

// --- Test Setup ---

type workerTestDeps struct {
	worker  *Worker
	mockFS  *MockFileSystem
	mockCM  *MockCacheManager
	mockTpl *MockTemplateExecutor // Use mock template executor instance
	opts    *config.Options
	logger  *slog.Logger
	logBuf  *bytes.Buffer // Capture log output if needed
}

func setupWorkerTest(t *testing.T, modifyOpts func(*config.Options)) workerTestDeps {
	t.Helper()
	mockFS := new(MockFileSystem)
	mockCM := new(MockCacheManager)
	mockTpl := new(MockTemplateExecutor) // Store the mock template executor

	opts := &config.Options{
		Input:                "/input", // Use absolute paths for predictability in tests
		Output:               "/output",
		Concurrency:          1,
		UseCache:             true,
		LargeFileThresholdMB: 1,
		LargeFileMode:        "skip", // default
		BinaryMode:           "skip", // default
		SkipHiddenFiles:      true,
		LanguageMappings:     make(map[string]string),
		FrontMatter: config.FrontMatterConfig{
			Enabled: false,
			Format:  "yaml", // Default to yaml if enabled later
			Static:  make(map[string]interface{}),
			Include: []string{},
		},
		Watch: config.WatchConfig{Debounce: 300 * time.Millisecond},
	}

	if modifyOpts != nil {
		modifyOpts(opts)
	}

	// Apply Recommendation B.3: Default logger to discard output at INFO level
	var logBuf bytes.Buffer
	logLevel := slog.LevelInfo
	logOutput := io.Discard // Default to discard

	// Optional: uncomment below to capture DEBUG logs to stdout for local debugging
	// logOutput = os.Stdout
	// logLevel = slog.LevelDebug

	logger := slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: logLevel}))

	// Apply Recommendation B.2 (Clarification):
	// Determine if a template executor *struct* should be passed to the worker.
	// The actual *mock behavior* is defined on mockTpl instance.
	var templateExec *template.Executor
	if opts.TemplateFile != "" {
		// Create an empty struct instance. The worker's logic should check if this
		// is non-nil and then, conceptually, call the *mocked* Execute method.
		// This setup works as long as the worker doesn't call methods on the
		// passed *template.Executor struct itself (which currently has none).
		templateExec = &template.Executor{}
	}

	worker := NewWorker(
		opts,
		mockFS,
		mockCM,
		logger,
		func(path string) bool { return false }, // Simple default ignore matcher for tests
		templateExec,                            // Pass the struct instance (or nil)
		[]byte("config-hash"),
		[]byte("template-hash"),
	)

	return workerTestDeps{
		worker:  worker,
		mockFS:  mockFS,
		mockCM:  mockCM,
		mockTpl: mockTpl, // Return the actual mock object for setting expectations
		opts:    opts,
		logger:  logger,
		logBuf:  &logBuf, // Return buffer in case log capture is needed
	}
}

// Use default setup for most tests
func setupBasicWorkerTest(t *testing.T) workerTestDeps {
	return setupWorkerTest(t, nil)
}

// Helper to create a simple file info mock
func createMockFileInfo(name string, size int64, modTime time.Time) fs.FileInfo {
	return &MockFileInfo{
		name:    name,
		size:    size,
		modTime: modTime,
		mode:    0644, // Default file mode
		isDir:   false,
	}
}

// --- Test Cases ---

// TestWorker_ProcessFile_Success tests the happy path for a standard text file.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_Success(t *testing.T) {
	deps := setupBasicWorkerTest(t)
	filePath := filepath.Join(deps.opts.Input, "code.go")
	outputFilePath := filepath.Join(deps.opts.Output, "code.go.md")
	fileContent := []byte("package main\nfunc main(){}")
	modTime := time.Now().Add(-1 * time.Hour)
	mockInfo := createMockFileInfo("code.go", int64(len(fileContent)), modTime)

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(fileContent, nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), fs.FileMode(0755)).Return(nil).Once()
	expectedOutputContent := "```go\npackage main\nfunc main(){}\n```\n"
	deps.mockFS.On("WriteFile", outputFilePath, []byte(expectedOutputContent), fs.FileMode(0644)).Return(nil).Once()
	deps.mockCM.On("Update", filePath, mock.MatchedBy(func(entry cache.CacheEntry) bool {
		assert.WithinDuration(t, modTime, entry.ModTime, time.Second) // Use tolerance for time
		assert.Equal(t, mockInfo.Size(), entry.Size)
		assert.Equal(t, deps.worker.ConfigHash, entry.ConfigHash)
		assert.Equal(t, deps.worker.TemplateHash, entry.TemplateHash)
		assert.Equal(t, outputFilePath, entry.OutputPath)
		// assert.Nil(t, entry.SourceHash) // Source hash calculation not tested here
		return true
	})).Return(nil).Once()

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusProcessed, result.Status)
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
}

// TestWorker_ProcessFile_SkipHidden tests skipping hidden files.
// Relates to TASK-CONVI-WWC
func TestWorker_ProcessFile_SkipHidden(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.SkipHiddenFiles = true // Explicitly set, though default
	})
	filePath := filepath.Join(deps.opts.Input, ".hiddenfile")

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusSkipped, result.Status)
	assert.Equal(t, "Hidden", result.Reason)
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertNotCalled(t, "Stat", mock.Anything)
	deps.mockCM.AssertNotCalled(t, "Check", mock.Anything, mock.Anything, mock.Anything)
}

// TestWorker_ProcessFile_SkipIgnored tests skipping ignored files.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_SkipIgnored(t *testing.T) {
	deps := setupBasicWorkerTest(t)
	// Modify worker's matcher for this test
	deps.worker.IgnoreMatcher = func(path string) bool {
		return filepath.Base(path) == "ignore_me.log"
	}
	filePath := filepath.Join(deps.opts.Input, "ignore_me.log")

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusSkipped, result.Status)
	assert.Equal(t, "Ignored", result.Reason)
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertNotCalled(t, "Stat", mock.Anything)
	deps.mockCM.AssertNotCalled(t, "Check", mock.Anything, mock.Anything, mock.Anything)
}

// TestWorker_ProcessFile_SkipLarge tests skipping large files.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_SkipLarge(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.LargeFileThresholdMB = 1
		opts.LargeFileMode = "skip"
	})
	filePath := filepath.Join(deps.opts.Input, "large.bin")
	fileSize := int64(1.5 * 1024 * 1024) // 1.5 MB
	modTime := time.Now()
	mockInfo := createMockFileInfo("large.bin", fileSize, modTime)

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusSkipped, result.Status)
	assert.Contains(t, result.Reason, "Large") // Check reason indicates large file
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertNotCalled(t, "Check", mock.Anything, mock.Anything, mock.Anything)
	deps.mockFS.AssertNotCalled(t, "ReadFile", mock.Anything)
}

// TestWorker_ProcessFile_ErrorLarge tests erroring on large files.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_ErrorLarge(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.LargeFileThresholdMB = 1
		opts.LargeFileMode = "error"
	})
	filePath := filepath.Join(deps.opts.Input, "large.bin")
	fileSize := int64(1.5 * 1024 * 1024) // 1.5 MB
	modTime := time.Now()
	mockInfo := createMockFileInfo("large.bin", fileSize, modTime)

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusError, result.Status)
	assert.Contains(t, result.Reason, "Large") // Check reason indicates large file
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "exceeds size threshold")
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertNotCalled(t, "Check", mock.Anything, mock.Anything, mock.Anything)
	deps.mockFS.AssertNotCalled(t, "ReadFile", mock.Anything)
}

// TestWorker_ProcessFile_SkipBinary tests skipping binary files.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_SkipBinary(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.BinaryMode = "skip"
	})
	filePath := filepath.Join(deps.opts.Input, "image.png")
	binaryContent := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	modTime := time.Now()
	mockInfo := createMockFileInfo("image.png", int64(len(binaryContent)), modTime)

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(binaryContent, nil).Once() // ReadFile is called for binary check

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusSkipped, result.Status)
	assert.Equal(t, "Binary", result.Reason)
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertNotCalled(t, "WriteFile", mock.Anything, mock.Anything, mock.Anything)
	deps.mockCM.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

// TestWorker_ProcessFile_PlaceholderBinary tests placeholder generation for binary files.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_PlaceholderBinary(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.BinaryMode = "placeholder"
	})
	filePath := filepath.Join(deps.opts.Input, "image.png")
	outputFilePath := filepath.Join(deps.opts.Output, "image.png.md")
	binaryContent := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	modTime := time.Now()
	mockInfo := createMockFileInfo("image.png", int64(len(binaryContent)), modTime)

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(binaryContent, nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), fs.FileMode(0755)).Return(nil).Once()
	expectedOutputContent := fmt.Sprintf("**Note:** File `%s` is a binary file and its content is not displayed.", filePath)
	deps.mockFS.On("WriteFile", outputFilePath, []byte(expectedOutputContent), fs.FileMode(0644)).Return(nil).Once()

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusProcessed, result.Status) // Placeholders are considered "processed"
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
	deps.mockCM.AssertNotCalled(t, "Update", filePath, mock.Anything) // Verify cache NOT updated
}

// TestWorker_ProcessFile_CacheHit tests the cache hit scenario.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_CacheHit(t *testing.T) {
	deps := setupBasicWorkerTest(t)
	filePath := filepath.Join(deps.opts.Input, "cached.txt")
	modTime := time.Now()
	mockInfo := createMockFileInfo("cached.txt", 100, modTime)

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusHit, nil).Once() // Simulate Cache Hit

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusCached, result.Status)
	assert.Equal(t, filePath, result.FilePath)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertNotCalled(t, "ReadFile", mock.Anything)
	deps.mockFS.AssertNotCalled(t, "WriteFile", mock.Anything, mock.Anything, mock.Anything)
	deps.mockCM.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

// TestWorker_ProcessFile_ErrorRead tests handling of file read errors.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_ErrorRead(t *testing.T) {
	deps := setupBasicWorkerTest(t)
	filePath := filepath.Join(deps.opts.Input, "unreadable.txt")
	modTime := time.Now()
	mockInfo := createMockFileInfo("unreadable.txt", 50, modTime)
	readErr := errors.New("permission denied")

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(nil, readErr).Once() // Simulate Read Error

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusError, result.Status)
	assert.Equal(t, "Read failed", result.Reason)
	assert.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, readErr) // Check underlying error is wrapped
	assert.Contains(t, result.Error.Error(), "failed to read file content")
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertNotCalled(t, "WriteFile", mock.Anything, mock.Anything, mock.Anything)
	deps.mockCM.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

// TestWorker_ProcessFile_ErrorWrite tests handling of file write errors.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_ErrorWrite(t *testing.T) {
	deps := setupBasicWorkerTest(t)
	filePath := filepath.Join(deps.opts.Input, "code.go")
	outputFilePath := filepath.Join(deps.opts.Output, "code.go.md")
	fileContent := []byte("package main")
	modTime := time.Now()
	mockInfo := createMockFileInfo("code.go", int64(len(fileContent)), modTime)
	writeErr := errors.New("disk full")

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(fileContent, nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), fs.FileMode(0755)).Return(nil).Once()
	deps.mockFS.On("WriteFile", outputFilePath, mock.Anything, mock.Anything).Return(writeErr).Once() // Simulate Write Error

	// Execute
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusError, result.Status)
	assert.Equal(t, "Output file write failed", result.Reason)
	assert.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, writeErr)
	assert.Contains(t, result.Error.Error(), "failed to write output file")
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
	deps.mockCM.AssertNotCalled(t, "Update", mock.Anything, mock.Anything) // No update if write fails
}

// TestWorker_ProcessFile_DetectLanguage tests basic language detection.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_DetectLanguage(t *testing.T) {
	deps := setupBasicWorkerTest(t)
	filePath := filepath.Join(deps.opts.Input, "script.py") // Expect 'python'
	outputFilePath := filepath.Join(deps.opts.Output, "script.py.md")
	fileContent := []byte("print('hello')")
	modTime := time.Now()
	mockInfo := createMockFileInfo("script.py", int64(len(fileContent)), modTime)

	// Mock behavior
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(fileContent, nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	expectedOutputContent := "```python\nprint('hello')\n```\n"
	deps.mockFS.On("WriteFile", outputFilePath, []byte(expectedOutputContent), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockCM.On("Update", filePath, mock.AnythingOfType("cache.CacheEntry")).Return(nil).Once()

	result := deps.worker.ProcessFile(context.Background(), filePath)

	assert.Equal(t, StatusProcessed, result.Status)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t) // Ensures WriteFile was called with correct content
	deps.mockCM.AssertExpectations(t)
}

// TestWorker_ProcessFile_DetectLanguageOverride tests language overrides.
// Relates to TASK-CONVI-047
func TestWorker_ProcessFile_DetectLanguageOverride(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.LanguageMappings[".mypy"] = "python" // Override .mypy to be treated as python
	})
	filePath := filepath.Join(deps.opts.Input, "custom.mypy")
	outputFilePath := filepath.Join(deps.opts.Output, "custom.mypy.md")
	fileContent := []byte("# python code")
	modTime := time.Now()
	mockInfo := createMockFileInfo("custom.mypy", int64(len(fileContent)), modTime)

	// Mock behavior
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(fileContent, nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	expectedOutputContent := "```python\n# python code\n```\n"
	deps.mockFS.On("WriteFile", outputFilePath, []byte(expectedOutputContent), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockCM.On("Update", filePath, mock.AnythingOfType("cache.CacheEntry")).Return(nil).Once()

	result := deps.worker.ProcessFile(context.Background(), filePath)

	assert.Equal(t, StatusProcessed, result.Status)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
}

// TestWorker_ProcessFile_WithTemplate tests using a custom template.
// Relates to TASK-CONVI-082
func TestWorker_ProcessFile_WithTemplate(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.TemplateFile = "/path/to/template.tmpl" // Indicate a template is used
	})
	filePath := filepath.Join(deps.opts.Input, "data.json")
	outputFilePath := filepath.Join(deps.opts.Output, "data.json.md")
	fileContent := []byte(`{"key": "value"}`)
	modTime := time.Now()
	mockInfo := createMockFileInfo("data.json", int64(len(fileContent)), modTime)
	renderedTemplateContent := "Template Output: json - /input/data.json - {\"key\": \"value\"}"

	// Mock Expectations
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(fileContent, nil).Once()
	// Expect Template Executor mock to be called
	deps.mockTpl.On("Execute", mock.MatchedBy(func(data map[string]interface{}) bool {
		assert.Equal(t, filePath, data["FilePath"])
		assert.Equal(t, string(fileContent), data["Content"])
		assert.Equal(t, "json", data["DetectedLanguage"]) // Assuming basic detection
		return true
	})).Return(renderedTemplateContent, nil).Once() // Mock successful template execution
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), fs.FileMode(0755)).Return(nil).Once()
	deps.mockFS.On("WriteFile", outputFilePath, []byte(renderedTemplateContent), fs.FileMode(0644)).Return(nil).Once()
	deps.mockCM.On("Update", filePath, mock.AnythingOfType("cache.CacheEntry")).Return(nil).Once()

	// Execute
	// Use the mock template executor for expectations, even though the worker holds an empty struct.
	// This relies on the worker's internal logic checking TemplateExec existence and then calling the (mocked) Execute method.
	result := deps.worker.ProcessFile(context.Background(), filePath)

	// Assert
	assert.Equal(t, StatusProcessed, result.Status)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
	deps.mockTpl.AssertExpectations(t) // Verify the template mock was called
}

// TestWorker_ProcessFile_WithFrontMatter tests enabling front matter.
// Relates to TASK-CONVI-082
func TestWorker_ProcessFile_WithFrontMatter(t *testing.T) {
	deps := setupWorkerTest(t, func(opts *config.Options) {
		opts.FrontMatter.Enabled = true
		opts.FrontMatter.Format = "yaml" // Test YAML
		opts.FrontMatter.Static = map[string]interface{}{"author": "tester"}
		opts.FrontMatter.Include = []string{"FilePath", "DetectedLanguage"}
	})
	filePath := filepath.Join(deps.opts.Input, "script.py")
	outputFilePath := filepath.Join(deps.opts.Output, "script.py.md")
	fileContent := []byte("print('fm test')")
	modTime := time.Now()
	mockInfo := createMockFileInfo("script.py", int64(len(fileContent)), modTime)

	// Mock behavior
	deps.mockFS.On("Stat", filePath).Return(mockInfo, nil).Once()
	deps.mockCM.On("Check", filePath, deps.worker.ConfigHash, deps.worker.TemplateHash).Return(cache.StatusMiss, nil).Once()
	deps.mockFS.On("ReadFile", filePath).Return(fileContent, nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(outputFilePath), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()

	// Expect content with YAML front matter
	deps.mockFS.On("WriteFile", outputFilePath, mock.MatchedBy(func(data []byte) bool {
		content := string(data)
		assert.True(t, strings.HasPrefix(content, "---\n"), "Should start with YAML delimiter")
		content = strings.TrimPrefix(content, "---\n")
		assert.Contains(t, content, "\n---\n", "Should contain ending YAML delimiter")
		assert.Contains(t, content, "author: tester\n", "Should contain static field")
		assert.Contains(t, content, fmt.Sprintf("FilePath: %s\n", filePath), "Should contain FilePath")
		assert.Contains(t, content, "DetectedLanguage: python\n", "Should contain DetectedLanguage")
		assert.Contains(t, content, "```python\nprint('fm test')\n```\n", "Should contain code block")
		return true
	}), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockCM.On("Update", filePath, mock.AnythingOfType("cache.CacheEntry")).Return(nil).Once()

	result := deps.worker.ProcessFile(context.Background(), filePath)

	assert.Equal(t, StatusProcessed, result.Status)
	assert.NoError(t, result.Error)
	deps.mockFS.AssertExpectations(t)
	deps.mockCM.AssertExpectations(t)
}

// TestDetectLanguageByExtension verifies the helper function using table-driven tests.
// Relates to TASK-CONVI-047 & Recommendation B.1
func TestDetectLanguageByExtension(t *testing.T) {
	// Apply Recommendation B.1: Refactor to table-driven test
	mappings := map[string]string{
		".custom": "xml", // Override
		".abc":    "abc-lang",
	}

	testCases := []struct {
		name     string
		filePath string
		mappings map[string]string // Test different mapping scenarios
		expected string
	}{
		{name: "Go", filePath: "file.go", mappings: mappings, expected: "go"},
		{name: "Python", filePath: "script.py", mappings: mappings, expected: "python"},
		{name: "YAML", filePath: "config.yaml", mappings: mappings, expected: "yaml"},
		{name: "YML", filePath: "config.yml", mappings: mappings, expected: "yaml"},
		{name: "JavaScript", filePath: "app.js", mappings: mappings, expected: "javascript"},
		{name: "TypeScript", filePath: "module.ts", mappings: mappings, expected: "typescript"},
		{name: "HTML", filePath: "index.html", mappings: mappings, expected: "html"},
		{name: "CSS", filePath: "style.css", mappings: mappings, expected: "css"},
		{name: "Markdown", filePath: "README.md", mappings: mappings, expected: "markdown"},
		{name: "Shell", filePath: "setup.sh", mappings: mappings, expected: "bash"},
		{name: "Docker", filePath: "Dockerfile", mappings: mappings, expected: "dockerfile"},
		{name: "Terraform", filePath: "main.tf", mappings: mappings, expected: "terraform"},
		{name: "HCL", filePath: "variables.hcl", mappings: mappings, expected: "hcl"},
		{name: "Lua", filePath: "plugin.lua", mappings: mappings, expected: "lua"},
		{name: "Perl", filePath: "script.pl", mappings: mappings, expected: "perl"},
		{name: "Scala", filePath: "app.scala", mappings: mappings, expected: "scala"},
		{name: "Kotlin", filePath: "main.kt", mappings: mappings, expected: "kotlin"},
		{name: "C", filePath: "main.c", mappings: mappings, expected: "c"},
		{name: "CPP", filePath: "main.cpp", mappings: mappings, expected: "cpp"},
		{name: "CS", filePath: "Program.cs", mappings: mappings, expected: "csharp"},
		{name: "Java", filePath: "App.java", mappings: mappings, expected: "java"},
		{name: "PHP", filePath: "index.php", mappings: mappings, expected: "php"},
		{name: "Ruby", filePath: "script.rb", mappings: mappings, expected: "ruby"},
		{name: "Rust", filePath: "main.rs", mappings: mappings, expected: "rust"},
		{name: "SQL", filePath: "query.sql", mappings: mappings, expected: "sql"},
		{name: "Swift", filePath: "app.swift", mappings: mappings, expected: "swift"},
		{name: "XML", filePath: "data.xml", mappings: mappings, expected: "xml"},
		{name: "Unknown", filePath: "data.unknown", mappings: mappings, expected: "unknown"},
		{name: "NoExtension", filePath: "Makefile", mappings: mappings, expected: ""},
		{name: "MultipleDots", filePath: "archive.tar.gz", mappings: mappings, expected: "gz"},
		{name: "Override", filePath: "config.custom", mappings: mappings, expected: "xml"},
		{name: "OverrideCaseLower", filePath: "data.custom", mappings: mappings, expected: "xml"},  // Test lowercase matching override
		{name: "OverrideCaseUpper", filePath: "DATA.CUSTOM", mappings: mappings, expected: "xml"},  // Test uppercase matching override
		{name: "NilMap", filePath: "file.go", mappings: nil, expected: "go"},                       // Test case with nil map
		{name: "EmptyMap", filePath: "file.py", mappings: map[string]string{}, expected: "python"}, // Test case with empty map
		{name: "MapWithoutMatch", filePath: "other.ext", mappings: mappings, expected: "ext"},      // Test case where map doesn't have the extension
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := DetectLanguageByExtension(tc.filePath, tc.mappings)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
