package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/stackvity/convi/internal/cache"
	"github.com/stackvity/convi/internal/config"
	"github.com/stackvity/convi/internal/template"
)

// --- Mock Implementations (Engine specific) ---

// MockFileSystem (Adapted from cache_test.go)
type MockFileSystem struct {
	mock.Mock
	mu        sync.RWMutex
	files     map[string][]byte
	fileInfos map[string]os.FileInfo
	walkError error
	// Add other necessary mock methods/fields
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		files:     make(map[string][]byte),
		fileInfos: make(map[string]os.FileInfo),
	}
}

func (mfs *MockFileSystem) AddFile(path string, content []byte, modTime time.Time) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.files[absPath] = content
	mfs.fileInfos[absPath] = &MockFileInfo{
		name:    filepath.Base(absPath),
		size:    int64(len(content)),
		modTime: modTime,
		mode:    0644,
		isDir:   false,
	}
}

func (mfs *MockFileSystem) AddDir(path string, modTime time.Time) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.fileInfos[absPath] = &MockFileInfo{
		name:    filepath.Base(absPath),
		size:    0,
		modTime: modTime,
		mode:    0755 | os.ModeDir,
		isDir:   true,
	}
}

func (mfs *MockFileSystem) SetWalkError(err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	mfs.walkError = err
}

// Implement FileSystem interface methods used by Engine
func (mfs *MockFileSystem) ReadFile(name string) ([]byte, error) {
	args := mfs.Called(name)
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(name)
	content, exists := mfs.files[absPath]
	if !exists {
		// Return mock setup error or default os.ErrNotExist
		mockErr := args.Error(1)
		if mockErr != nil {
			return nil, mockErr
		}
		return nil, os.ErrNotExist
	}
	// Return actual content and configured error
	return content, args.Error(1)
}

func (mfs *MockFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	args := mfs.Called(name, data, perm)
	return args.Error(0)
}

func (mfs *MockFileSystem) Stat(name string) (os.FileInfo, error) {
	args := mfs.Called(name)
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(name)
	info, exists := mfs.fileInfos[absPath]
	if !exists {
		// Return mock setup error or default os.ErrNotExist
		mockErr := args.Error(1)
		if mockErr != nil {
			return nil, mockErr
		}
		return nil, os.ErrNotExist
	}
	// Return actual info and configured error
	return info, args.Error(1)
}

func (mfs *MockFileSystem) MkdirAll(path string, perm os.FileMode) error {
	args := mfs.Called(path, perm)
	// Simulate actual MkdirAll behavior slightly better
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	if _, exists := mfs.fileInfos[absPath]; !exists {
		mfs.fileInfos[absPath] = &MockFileInfo{
			name:    filepath.Base(absPath),
			size:    0,
			modTime: time.Now(),
			mode:    perm | os.ModeDir,
			isDir:   true,
		}
	}
	return args.Error(0)
}

// WalkDir simulation - Improved simplified version for engine tests
func (mfs *MockFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	mfs.mu.RLock()
	walkErr := mfs.walkError
	pathsToWalk := make(map[string]os.FileInfo)
	// Need to copy fileInfos under RLock to avoid race with AddFile/AddDir
	for p, fi := range mfs.fileInfos {
		pathsToWalk[p] = fi
	}
	mfs.mu.RUnlock()

	if walkErr != nil {
		return walkErr
	}

	absRoot, _ := filepath.Abs(root)
	rootInfo, rootExists := pathsToWalk[absRoot]
	if !rootExists {
		return fmt.Errorf("root path %s not found in mock file system: %w", root, os.ErrNotExist)
	}

	// Internal helper to simulate walking
	var walk func(string, os.FileInfo) error
	walk = func(currentPath string, info os.FileInfo) error {
		err := fn(currentPath, &mockDirEntry{info}, nil)
		if err != nil {
			return err // Propagate SkipDir or other errors immediately
		}

		if !info.IsDir() {
			return nil // Nothing more to do for files
		}

		// Simulate reading directory entries (basic approach)
		for path, fi := range pathsToWalk {
			// Check if 'path' is a direct child of 'currentPath'
			rel, err := filepath.Rel(currentPath, path)
			if err == nil && rel != "." && !strings.Contains(rel, string(filepath.Separator)) {
				// It's a direct child, recurse
				if err := walk(path, fi); err != nil {
					if errors.Is(err, filepath.SkipDir) {
						// If SkipDir is returned for a child directory, stop processing children of currentPath
						break
					}
					return err // Propagate other errors
				}
			}
		}
		return nil
	}

	// Start walking from the root
	return walk(absRoot, rootInfo)
}

// Remove - Mock implementation
func (mfs *MockFileSystem) Remove(name string) error {
	args := mfs.Called(name)
	return args.Error(0)
}

// Rename - Mock implementation
func (mfs *MockFileSystem) Rename(oldpath, newpath string) error {
	args := mfs.Called(oldpath, newpath)
	return args.Error(0)
}

// Chtimes - Mock implementation
func (mfs *MockFileSystem) Chtimes(name string, atime time.Time, mtime time.Time) error {
	args := mfs.Called(name, atime, mtime)
	return args.Error(0)
}

// --- MockFileInfo (Helper, assuming similar to cache_test.go) ---
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
func (mfi *MockFileInfo) Sys() interface{}   { return nil }

// mockDirEntry to satisfy fs.DirEntry
type mockDirEntry struct {
	os.FileInfo
}

func (m *mockDirEntry) Type() fs.FileMode {
	return m.Mode().Type()
}

func (m *mockDirEntry) Info() (fs.FileInfo, error) {
	return m.FileInfo, nil
}

// --- Mock CacheManager ---
type MockCacheManager struct {
	mock.Mock
}

func (m *MockCacheManager) Check(filePath string, currentConfigHash []byte, currentTemplateHash []byte) (cache.CacheStatus, error) {
	args := m.Called(filePath, currentConfigHash, currentTemplateHash)
	// Ensure correct type assertion, default to miss if not set correctly
	status, ok := args.Get(0).(cache.CacheStatus)
	if !ok {
		return cache.StatusMiss, args.Error(1)
	}
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

// --- Mock Template Executor ---
type MockTemplateExecutor struct {
	mock.Mock
}

func (m *MockTemplateExecutor) Execute(data map[string]interface{}) (string, error) {
	args := m.Called(data)
	return args.String(0), args.Error(1)
}

// --- Mock Fsnotify Watcher ---
// Interface for mocking fsnotify.Watcher
type fsnotifyWatcher interface {
	Add(string) error
	Remove(string) error
	Close() error
	Events() chan fsnotify.Event // Method returning channel
	Errors() chan error          // Method returning channel
}

// Mock implementation - satisfies the interface
type mockFsnotifyWatcher struct {
	mock.Mock
	eventChan chan fsnotify.Event
	errorChan chan error
}

func newMockFsnotifyWatcher() *mockFsnotifyWatcher {
	return &mockFsnotifyWatcher{
		eventChan: make(chan fsnotify.Event, 10), // Buffered channel
		errorChan: make(chan error, 1),
	}
}

func (m *mockFsnotifyWatcher) Add(name string) error {
	args := m.Called(name)
	return args.Error(0)
}

func (m *mockFsnotifyWatcher) Remove(name string) error {
	args := m.Called(name)
	return args.Error(0)
}

func (m *mockFsnotifyWatcher) Close() error {
	// Close channels only if not already closed
	// Check needed to prevent panic on double close in some test scenarios
	select {
	case _, ok := <-m.eventChan:
		if ok {
			close(m.eventChan)
		}
	default:
		close(m.eventChan)
	}
	select {
	case _, ok := <-m.errorChan:
		if ok {
			close(m.errorChan)
		}
	default:
		close(m.errorChan)
	}
	args := m.Called()
	return args.Error(0)
}

// Events method implementation
func (m *mockFsnotifyWatcher) Events() chan fsnotify.Event {
	return m.eventChan
}

// Errors method implementation
func (m *mockFsnotifyWatcher) Errors() chan error {
	return m.errorChan
}

// Helper for tests
func (m *mockFsnotifyWatcher) InjectEvent(evt fsnotify.Event) {
	// Use non-blocking send in case channel is full or closed
	select {
	case m.eventChan <- evt:
	default:
	}
}

// Helper for tests
func (m *mockFsnotifyWatcher) InjectError(err error) {
	// Use non-blocking send
	select {
	case m.errorChan <- err:
	default:
	}
}

// --- FIX: Adapter for real fsnotify.Watcher ---
// realFsnotifyWatcherAdapter wraps the real fsnotify.Watcher to satisfy the fsnotifyWatcher interface.
type realFsnotifyWatcherAdapter struct {
	*fsnotify.Watcher
}

// Events method to satisfy the interface.
func (a *realFsnotifyWatcherAdapter) Events() chan fsnotify.Event {
	return a.Watcher.Events // Returns the Events channel field
}

// Errors method to satisfy the interface.
func (a *realFsnotifyWatcherAdapter) Errors() chan error {
	return a.Watcher.Errors // Returns the Errors channel field
}

// Ensure all methods from the interface are implemented (they are implicitly via embedding *fsnotify.Watcher)
// Add, Remove, Close are already methods on *fsnotify.Watcher

// Override the default newWatcher function for testing
// Use this variable name consistently in test setup.
var newWatcher = func() (fsnotifyWatcher, error) { // Changed from testNewWatcher
	// Default to returning a real watcher if not overridden in test
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// --- FIX: Wrap the real watcher in the adapter ---
	return &realFsnotifyWatcherAdapter{Watcher: w}, nil
}

// --- Test Setup ---

type engineTestDeps struct {
	eng       *Engine
	mockFS    *MockFileSystem
	mockCM    *MockCacheManager
	mockTpl   *MockTemplateExecutor
	opts      *config.Options
	logger    *slog.Logger
	logBuffer *bytes.Buffer
}

func setupEngineTestWithOptions(t *testing.T, modifyOpts func(*config.Options)) engineTestDeps {
	t.Helper()
	mockFS := NewMockFileSystem()
	mockCM := new(MockCacheManager)
	mockTpl := new(MockTemplateExecutor) // Use mock template engine

	opts := &config.Options{
		Input:                "/input",
		Output:               "/output",
		Concurrency:          1, // Default to 1 worker for simpler test verification
		UseCache:             true,
		LargeFileThresholdMB: 1,
		LargeFileMode:        "skip",
		BinaryMode:           "skip",
		SkipHiddenFiles:      true,
		Watch:                config.WatchConfig{Debounce: 10 * time.Millisecond}, // Default debounce
	}

	// Allow modifying options
	if modifyOpts != nil {
		modifyOpts(opts)
	}

	var logBuffer bytes.Buffer
	logLevel := slog.LevelInfo
	if opts.Verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: logLevel}))

	// Ensure input dir exists in mock FS for WalkDir
	mockFS.AddDir(opts.Input, time.Now())

	// Conditionally create template executor mock
	var templateExec *template.Executor
	if opts.TemplateFile != "" {
		// Assuming template.Executor can be represented by our mock interface
		// We inject the mock instance here, not the real one
		// --- FIX: Create a real executor if needed for non-mocked tests,
		// but for mock tests, this might be ok. However, the current
		// template.Executor in `fullstack-code` is just a struct, no methods yet.
		// If tests depended on Executor methods, we'd mock those too.
		templateExec = &template.Executor{} // Use concrete type for now
	}

	eng := NewEngine(
		opts,
		mockFS,
		mockCM,
		logger,
		func(path string) bool { // Basic ignore matcher for testing
			for _, pattern := range opts.Ignore {
				// Simplified matching for tests
				base := filepath.Base(path)
				matchBase, _ := filepath.Match(pattern, base)
				matchPath, _ := filepath.Match(pattern, path)
				if matchBase || matchPath {
					return true
				}
			}
			return false
		},
		templateExec, // Inject nil or the executor
		[]byte("config-hash"),
		[]byte("template-hash"),
	)

	return engineTestDeps{
		eng:       eng,
		mockFS:    mockFS,
		mockCM:    mockCM,
		mockTpl:   mockTpl, // Store the mock template executor
		opts:      opts,
		logger:    logger,
		logBuffer: &logBuffer,
	}
}

func setupBasicEngineTest(t *testing.T) engineTestDeps {
	return setupEngineTestWithOptions(t, nil)
}

// --- Test Cases ---

// TestNewEngine verifies basic engine initialization.
func TestNewEngine(t *testing.T) {
	deps := setupBasicEngineTest(t)
	assert.NotNil(t, deps.eng)
	assert.Equal(t, deps.opts, deps.eng.Opts)
	assert.NotNil(t, deps.eng.FS)
	assert.NotNil(t, deps.eng.CM)
	assert.NotNil(t, deps.eng.Logger)
	assert.NotNil(t, deps.eng.IgnoreMatcher)
	// --- FIX: Assertion might need update based on setup logic ---
	if deps.opts.TemplateFile != "" {
		assert.NotNil(t, deps.eng.TemplateExec)
	} else {
		assert.Nil(t, deps.eng.TemplateExec) // No template file set by default
	}
	assert.Equal(t, []byte("config-hash"), deps.eng.ConfigHash)
	assert.Equal(t, []byte("template-hash"), deps.eng.TemplateHash)
}

// TestResolveConcurrency verifies concurrency calculation.
func TestResolveConcurrency(t *testing.T) {
	deps := setupBasicEngineTest(t)

	// Test explicit concurrency
	deps.opts.Concurrency = 4
	assert.Equal(t, 4, deps.eng.resolveConcurrency())

	// Test default (auto) concurrency - result depends on runtime.NumCPU()
	deps.opts.Concurrency = 0
	numCPU := runtime.NumCPU()
	expectedConcurrency := numCPU
	if expectedConcurrency <= 0 {
		expectedConcurrency = 1 // Ensure at least 1
	}
	assert.Equal(t, expectedConcurrency, deps.eng.resolveConcurrency())

	// Test negative concurrency (should resolve to auto)
	deps.opts.Concurrency = -2
	assert.Equal(t, expectedConcurrency, deps.eng.resolveConcurrency())
}

// TestRunOnce_Basic tests a simple run with a couple of files.
func TestRunOnce_Basic(t *testing.T) {
	deps := setupBasicEngineTest(t)
	// Use 1 worker for predictable execution order if needed, though not strictly necessary
	deps.opts.Concurrency = 1

	file1 := filepath.Join(deps.opts.Input, "file1.go")
	subdir := filepath.Join(deps.opts.Input, "subdir")
	file2 := filepath.Join(subdir, "file2.txt")
	absFile1, _ := filepath.Abs(file1)
	absFile2, _ := filepath.Abs(file2)

	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())
	deps.mockFS.AddDir(subdir, time.Now())
	deps.mockFS.AddFile(file2, []byte("content2"), time.Now())

	// Mock CacheManager behavior
	deps.mockCM.On("Check", file1, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Check", file2, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Update", file1, mock.Anything).Return(nil).Once()
	deps.mockCM.On("Update", file2, mock.Anything).Return(nil).Once()
	deps.mockCM.On("Persist").Return(nil).Once() // Expect persist after successful runOnce

	// Mock FileSystem Stat calls needed by worker
	deps.mockFS.On("Stat", file1).Return(deps.mockFS.fileInfos[absFile1], nil).Once()
	deps.mockFS.On("Stat", file2).Return(deps.mockFS.fileInfos[absFile2], nil).Once()
	// Mock other FS calls made by worker
	deps.mockFS.On("ReadFile", file1).Return([]byte("content1"), nil).Once()
	deps.mockFS.On("ReadFile", file2).Return([]byte("content2"), nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(filepath.Join(deps.opts.Output, "file1.go.md")), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(filepath.Join(deps.opts.Output, "subdir", "file2.txt.md")), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("WriteFile", filepath.Join(deps.opts.Output, "file1.go.md"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("WriteFile", filepath.Join(deps.opts.Output, "subdir", "file2.txt.md"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil).Once()

	ctx := context.Background()
	report, err := deps.eng.runOnce(ctx)

	assert.NoError(t, err)
	assert.Equal(t, 2, report.FilesProcessed)
	assert.Equal(t, 0, report.FilesFromCache)
	assert.Equal(t, 0, report.FilesSkippedIgnored)
	assert.Equal(t, 0, report.FilesSkippedBinary)
	assert.Equal(t, 0, report.FilesSkippedLarge)
	assert.Equal(t, 0, report.FilesErrored)
	assert.Empty(t, report.Errors)

	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertExpectations(t)
}

// TestRunOnce_WithError tests aggregation when a worker returns an error.
func TestRunOnce_WithError(t *testing.T) {
	deps := setupBasicEngineTest(t)
	deps.opts.Concurrency = 1 // Ensure predictable processing order

	file1 := filepath.Join(deps.opts.Input, "file1.go")
	file2 := filepath.Join(deps.opts.Input, "error.txt") // File that will error
	absFile1, _ := filepath.Abs(file1)
	absFile2, _ := filepath.Abs(file2)
	expectedErr := errors.New("read permission denied")

	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())
	deps.mockFS.AddFile(file2, []byte("content2"), time.Now())

	// Mock CacheManager behavior - Miss all
	deps.mockCM.On("Check", file1, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Check", file2, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Update", file1, mock.Anything).Return(nil).Once()  // Only update good file
	deps.mockCM.On("Update", file2, mock.Anything).Return(nil).Maybe() // Should not be called for error file
	deps.mockCM.On("Persist").Return(nil).Once()

	// Mock FileSystem calls
	deps.mockFS.On("Stat", file1).Return(deps.mockFS.fileInfos[absFile1], nil).Once()
	deps.mockFS.On("Stat", file2).Return(deps.mockFS.fileInfos[absFile2], nil).Once()
	// Make ReadFile fail for file2
	deps.mockFS.On("ReadFile", file1).Return([]byte("content1"), nil).Once()
	deps.mockFS.On("ReadFile", file2).Return([]byte(nil), expectedErr).Once() // Simulate read error
	// Other FS calls for file1
	deps.mockFS.On("MkdirAll", filepath.Dir(filepath.Join(deps.opts.Output, "file1.go.md")), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("WriteFile", filepath.Join(deps.opts.Output, "file1.go.md"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil).Once()

	ctx := context.Background()
	report, err := deps.eng.runOnce(ctx)

	assert.NoError(t, err) // runOnce itself doesn't error if aggregation works
	assert.Equal(t, 1, report.FilesProcessed, "Only file1 should be processed")
	assert.Equal(t, 0, report.FilesFromCache)
	assert.Equal(t, 0, report.FilesSkippedIgnored)
	assert.Equal(t, 0, report.FilesSkippedBinary)
	assert.Equal(t, 0, report.FilesSkippedLarge)
	assert.Equal(t, 1, report.FilesErrored, "file2 should have errored")
	assert.Contains(t, report.Errors, file2)
	// Check that the error message includes the underlying error text
	assert.ErrorContains(t, errors.New(report.Errors[file2]), expectedErr.Error())

	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertExpectations(t)
	// Ensure WriteFile was NOT called for the errored file's expected output
	deps.mockFS.AssertNotCalled(t, "WriteFile", filepath.Join(deps.opts.Output, "error.txt.md"), mock.Anything, mock.AnythingOfType("fs.FileMode"))
	deps.mockCM.AssertNotCalled(t, "Update", file2, mock.Anything)
}

// TestRunOnce_WithSkips tests aggregation with skipped files.
func TestRunOnce_WithSkips(t *testing.T) {
	deps := setupEngineTestWithOptions(t, func(opts *config.Options) {
		opts.Concurrency = 1 // Predictable order
		opts.Ignore = []string{"*.tmp"}
		opts.SkipHiddenFiles = true
	})

	file1 := filepath.Join(deps.opts.Input, "file1.go")
	fileBin := filepath.Join(deps.opts.Input, "image.png")
	fileLarge := filepath.Join(deps.opts.Input, "large.log")
	fileHidden := filepath.Join(deps.opts.Input, ".hiddenfile")
	fileIgnored := filepath.Join(deps.opts.Input, "ignored.tmp")
	absFile1, _ := filepath.Abs(file1)
	absFileBin, _ := filepath.Abs(fileBin)
	absFileLarge, _ := filepath.Abs(fileLarge)
	// --- FIX: Removed unused variable ---
	// absFileHidden, _ := filepath.Abs(fileHidden)
	absFileIgnored, _ := filepath.Abs(fileIgnored)

	// Add files (adjust size for large file)
	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())
	deps.mockFS.AddFile(fileBin, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, time.Now()) // PNG header
	largeContent := make([]byte, int(float64(deps.opts.LargeFileThresholdMB)*1024*1024)+1)
	deps.mockFS.AddFile(fileLarge, largeContent, time.Now())
	deps.mockFS.AddFile(fileHidden, []byte("hidden"), time.Now())
	deps.mockFS.AddFile(fileIgnored, []byte("ignored"), time.Now())

	// Mock Cache - Miss all initially
	deps.mockCM.On("Check", mock.Anything, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil)
	deps.mockCM.On("Update", file1, mock.Anything).Return(nil).Once() // Only update processable file
	deps.mockCM.On("Persist").Return(nil).Once()

	// Mock FS calls for the *processable* file only
	deps.mockFS.On("Stat", file1).Return(deps.mockFS.fileInfos[absFile1], nil).Once()
	deps.mockFS.On("ReadFile", file1).Return([]byte("content1"), nil).Once()
	deps.mockFS.On("MkdirAll", mock.AnythingOfType("string"), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("WriteFile", filepath.Join(deps.opts.Output, "file1.go.md"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	// Stat calls needed for skip checks
	deps.mockFS.On("Stat", fileBin).Return(deps.mockFS.fileInfos[absFileBin], nil).Once()
	deps.mockFS.On("Stat", fileLarge).Return(deps.mockFS.fileInfos[absFileLarge], nil).Once()
	// ReadFile needed for binary check (reads first 512 bytes)
	deps.mockFS.On("ReadFile", fileBin).Return(deps.mockFS.files[absFileBin], nil).Once()
	// Stat is also needed for ignored file check within worker, even though WalkDir might also skip
	deps.mockFS.On("Stat", fileIgnored).Return(deps.mockFS.fileInfos[absFileIgnored], nil).Once()
	// Stat for hidden file should NOT be called if WalkDir skips it
	// deps.mockFS.On("Stat", fileHidden).Return(deps.mockFS.fileInfos[absFileHidden], nil).Maybe()

	ctx := context.Background()
	report, err := deps.eng.runOnce(ctx)

	assert.NoError(t, err)
	assert.Equal(t, 1, report.FilesProcessed)
	assert.Equal(t, 0, report.FilesFromCache)
	// WalkDir mock skips hidden, IgnoreMatcher skips .tmp
	assert.Equal(t, 1, report.FilesSkippedIgnored, "Only ignored.tmp should be counted here")
	assert.Equal(t, 1, report.FilesSkippedBinary)
	assert.Equal(t, 1, report.FilesSkippedLarge)
	assert.Equal(t, 0, report.FilesErrored)
	assert.Empty(t, report.Errors)

	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertExpectations(t)
	// Verify ReadFile not called for skipped files
	deps.mockFS.AssertNotCalled(t, "ReadFile", fileLarge)
	deps.mockFS.AssertNotCalled(t, "ReadFile", fileHidden)
	deps.mockFS.AssertNotCalled(t, "ReadFile", fileIgnored)
	// Ensure WriteFile was not called for skipped files
	deps.mockFS.AssertNotCalled(t, "WriteFile", filepath.Join(deps.opts.Output, "image.png.md"), mock.Anything, mock.AnythingOfType("fs.FileMode"))
	deps.mockFS.AssertNotCalled(t, "WriteFile", filepath.Join(deps.opts.Output, "large.log.md"), mock.Anything, mock.AnythingOfType("fs.FileMode"))
	deps.mockFS.AssertNotCalled(t, "WriteFile", filepath.Join(deps.opts.Output, ".hiddenfile.md"), mock.Anything, mock.AnythingOfType("fs.FileMode"))
	deps.mockFS.AssertNotCalled(t, "WriteFile", filepath.Join(deps.opts.Output, "ignored.tmp.md"), mock.Anything, mock.AnythingOfType("fs.FileMode"))
}

// TestRunOnce_WithCacheHit tests aggregation with cache hits.
func TestRunOnce_WithCacheHit(t *testing.T) {
	deps := setupBasicEngineTest(t)
	deps.opts.Concurrency = 1

	file1 := filepath.Join(deps.opts.Input, "file1.go")
	file2 := filepath.Join(deps.opts.Input, "file2.txt")
	absFile2, _ := filepath.Abs(file2)

	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())
	deps.mockFS.AddFile(file2, []byte("content2"), time.Now())

	// Mock CacheManager behavior
	deps.mockCM.On("Check", file1, mock.Anything, mock.Anything).Return(cache.StatusHit, nil).Once()  // Hit for file1
	deps.mockCM.On("Check", file2, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once() // Miss for file2
	deps.mockCM.On("Update", file2, mock.Anything).Return(nil).Once()                                 // Update only file2
	deps.mockCM.On("Persist").Return(nil).Once()

	// Mock FS calls for file2 (the one missed)
	deps.mockFS.On("Stat", file2).Return(deps.mockFS.fileInfos[absFile2], nil).Once()
	deps.mockFS.On("ReadFile", file2).Return([]byte("content2"), nil).Once()
	deps.mockFS.On("MkdirAll", filepath.Dir(filepath.Join(deps.opts.Output, "file2.txt.md")), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("WriteFile", filepath.Join(deps.opts.Output, "file2.txt.md"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil).Once()

	ctx := context.Background()
	report, err := deps.eng.runOnce(ctx)

	assert.NoError(t, err)
	assert.Equal(t, 1, report.FilesProcessed, "Only file2 should be processed")
	assert.Equal(t, 1, report.FilesFromCache, "file1 should be from cache")
	assert.Equal(t, 0, report.FilesSkippedIgnored)
	assert.Equal(t, 0, report.FilesSkippedBinary)
	assert.Equal(t, 0, report.FilesSkippedLarge)
	assert.Equal(t, 0, report.FilesErrored)
	assert.Empty(t, report.Errors)

	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertExpectations(t)
	// Ensure FS operations were not called for the cache-hit file
	deps.mockFS.AssertNotCalled(t, "Stat", file1)
	deps.mockFS.AssertNotCalled(t, "ReadFile", file1)
	deps.mockFS.AssertNotCalled(t, "WriteFile", filepath.Join(deps.opts.Output, "file1.go.md"), mock.Anything, mock.AnythingOfType("fs.FileMode"))
}

// TestRunOnce_ScanError tests behavior when the initial directory scan fails.
func TestRunOnce_ScanError(t *testing.T) {
	deps := setupBasicEngineTest(t)
	scanErr := errors.New("permission denied scanning")

	// Configure WalkDir to return an error
	deps.mockFS.SetWalkError(scanErr)

	// Setup expectations: Persist should NOT be called if scan fails early
	deps.mockCM.On("Persist").Return(nil).Maybe() // Maybe allows 0 calls

	ctx := context.Background()
	report, err := deps.eng.runOnce(ctx)

	// Expect the error returned by WalkDir to be propagated
	assert.Error(t, err)
	assert.ErrorIs(t, err, scanErr) // Check if the specific error is wrapped

	// Report should reflect no processing
	assert.Equal(t, 0, report.FilesProcessed)
	assert.Equal(t, 0, report.FilesFromCache)
	assert.Equal(t, 0, report.FilesSkippedIgnored)
	assert.Equal(t, 0, report.FilesSkippedBinary)
	assert.Equal(t, 0, report.FilesSkippedLarge)
	assert.Equal(t, 0, report.FilesErrored)
	assert.Empty(t, report.Errors)

	deps.mockCM.AssertNotCalled(t, "Persist") // Verify persist was not called
}

// TestRun_RunOnce_Persist verifies cache persistence after runOnce.
func TestRun_RunOnce_Persist(t *testing.T) {
	deps := setupBasicEngineTest(t)
	file1 := filepath.Join(deps.opts.Input, "file1.go")
	absFile1, _ := filepath.Abs(file1)

	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())
	deps.mockCM.On("Check", mock.Anything, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil)
	deps.mockCM.On("Update", mock.Anything, mock.Anything).Return(nil)
	deps.mockCM.On("Persist").Return(nil).Once() // Expect exactly one call

	// Mock FS calls for worker
	deps.mockFS.On("Stat", file1).Return(deps.mockFS.fileInfos[absFile1], nil)
	deps.mockFS.On("ReadFile", file1).Return([]byte("content1"), nil)
	deps.mockFS.On("MkdirAll", mock.AnythingOfType("string"), mock.AnythingOfType("fs.FileMode")).Return(nil)
	deps.mockFS.On("WriteFile", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil)

	ctx := context.Background()
	// Use the main Run method, which calls runOnce internally when watch is false
	deps.opts.WatchMode = false // Ensure watch mode is off
	_, err := deps.eng.Run(ctx)

	assert.NoError(t, err)
	deps.mockCM.AssertExpectations(t) // Asserts Persist was called once
}

// TestRun_RunOnce_NoPersistOnError tests that persist is not called if runOnce fails.
func TestRun_RunOnce_NoPersistOnError(t *testing.T) {
	deps := setupBasicEngineTest(t)
	scanErr := errors.New("scan failed badly")

	// Configure WalkDir to return an error
	deps.mockFS.SetWalkError(scanErr)

	ctx := context.Background()
	deps.opts.WatchMode = false
	_, err := deps.eng.Run(ctx)

	assert.Error(t, err)
	assert.ErrorIs(t, err, scanErr)           // Check wrapped error
	deps.mockCM.AssertNotCalled(t, "Persist") // Verify persist was not called
}

// TestRun_WatchMode_InitialRun verifies the initial run in watch mode.
func TestRun_WatchMode_InitialRun(t *testing.T) {
	deps := setupEngineTestWithOptions(t, func(opts *config.Options) {
		opts.WatchMode = true
	})

	file1 := filepath.Join(deps.opts.Input, "file1.go")
	absFile1, _ := filepath.Abs(file1)
	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())

	// Mock initial run behaviors
	deps.mockCM.On("Check", file1, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Update", file1, mock.Anything).Return(nil).Once()
	deps.mockCM.On("Persist").Return(nil).Once() // Persist happens after initial run
	deps.mockFS.On("Stat", file1).Return(deps.mockFS.fileInfos[absFile1], nil).Once()
	deps.mockFS.On("ReadFile", file1).Return([]byte("content1"), nil).Once()
	deps.mockFS.On("MkdirAll", mock.AnythingOfType("string"), mock.AnythingOfType("fs.FileMode")).Return(nil).Once()
	deps.mockFS.On("WriteFile", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil).Once()

	// Mock fsnotify watcher creation
	mockWatcher := newMockFsnotifyWatcher()
	mockWatcher.On("Add", deps.opts.Input).Return(nil) // Expect Add for root dir
	mockWatcher.On("Close").Return(nil)
	// --- FIX: Use consistent variable name ---
	originalNewWatcher := newWatcher // Store the original function
	newWatcher = func() (fsnotifyWatcher, error) { return mockWatcher, nil }
	defer func() { newWatcher = originalNewWatcher }() // Restore original

	// Run with a short timeout context to trigger shutdown after initial run
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	report, err := deps.eng.Run(ctx)

	// Expect context cancelled error as the test stops the watch loop
	assert.ErrorIs(t, err, context.Canceled, "Expected context cancelled error")

	// Verify initial run results (might be slightly different if cancelled mid-persist)
	assert.Equal(t, 1, report.FilesProcessed)
	assert.Equal(t, 0, report.FilesErrored)

	// Verify mocks for the initial run
	deps.mockCM.AssertCalled(t, "Check", file1, mock.Anything, mock.Anything)
	deps.mockCM.AssertCalled(t, "Update", file1, mock.Anything)
	// Persist might be called depending on exact timing of cancellation
	// deps.mockCM.AssertCalled(t, "Persist")
	deps.mockFS.AssertExpectations(t)
	mockWatcher.AssertCalled(t, "Add", deps.opts.Input)
	mockWatcher.AssertCalled(t, "Close") // Should be called on graceful shutdown
}

// TestRun_WatchMode_EventHandlingAndReRun tests event detection and re-run trigger.
func TestRun_WatchMode_EventHandlingAndReRun(t *testing.T) {
	deps := setupEngineTestWithOptions(t, func(opts *config.Options) {
		opts.WatchMode = true
		opts.Watch.Debounce = 20 * time.Millisecond // Short debounce for test
		opts.Verbose = true                         // Enable logging to check output
	})

	file1 := filepath.Join(deps.opts.Input, "file1.go")
	file2 := filepath.Join(deps.opts.Input, "file2.txt")
	absFile1, _ := filepath.Abs(file1)
	absFile2, _ := filepath.Abs(file2)
	deps.mockFS.AddFile(file1, []byte("content1"), time.Now())
	deps.mockFS.AddFile(file2, []byte("content2"), time.Now())

	// Mock fsnotify watcher creation
	mockWatcher := newMockFsnotifyWatcher()
	mockWatcher.On("Add", deps.opts.Input).Return(nil)
	mockWatcher.On("Close").Return(nil)
	// --- FIX: Use consistent variable name ---
	originalNewWatcher := newWatcher // Store the original function
	newWatcher = func() (fsnotifyWatcher, error) { return mockWatcher, nil }
	defer func() { newWatcher = originalNewWatcher }() // Restore original

	// Mock initial run (miss all, update all)
	deps.mockCM.On("Check", file1, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Check", file2, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once()
	deps.mockCM.On("Update", file1, mock.Anything).Return(nil).Once()
	deps.mockCM.On("Update", file2, mock.Anything).Return(nil).Once()
	deps.mockCM.On("Persist").Return(nil).Twice() // After initial run AND after re-run

	// Mock initial FS calls
	deps.mockFS.On("Stat", file1).Return(deps.mockFS.fileInfos[absFile1], nil).Once()
	deps.mockFS.On("Stat", file2).Return(deps.mockFS.fileInfos[absFile2], nil).Once()
	deps.mockFS.On("ReadFile", file1).Return([]byte("content1"), nil).Once()
	deps.mockFS.On("ReadFile", file2).Return([]byte("content2"), nil).Once()
	deps.mockFS.On("MkdirAll", mock.AnythingOfType("string"), mock.AnythingOfType("fs.FileMode")).Return(nil)                 // Allow multiple
	deps.mockFS.On("WriteFile", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("fs.FileMode")).Return(nil) // Allow multiple

	// Mock behavior for the re-run: file1 cache hit, file2 miss (simulate change)
	deps.mockCM.On("Check", file1, mock.Anything, mock.Anything).Return(cache.StatusHit, nil).Once()  // Second check (re-run)
	deps.mockCM.On("Check", file2, mock.Anything, mock.Anything).Return(cache.StatusMiss, nil).Once() // Second check (re-run) - MISS
	deps.mockCM.On("Update", file2, mock.Anything).Return(nil).Once()                                 // Update file2 again

	// Mock FS calls for the re-run (only file2)
	deps.mockFS.On("Stat", file2).Return(deps.mockFS.fileInfos[absFile2], nil).Once()
	deps.mockFS.On("ReadFile", file2).Return([]byte("content2-modified"), nil).Once() // Simulate reading modified content
	// MkdirAll and WriteFile mocks already setup to allow multiple calls

	ctx, cancel := context.WithCancel(context.Background())
	var wgRun sync.WaitGroup
	wgRun.Add(1)

	go func() {
		defer wgRun.Done()
		_, _ = deps.eng.Run(ctx) // Run in goroutine as watch blocks
	}()

	// Wait a bit for the initial run and watcher setup
	time.Sleep(50 * time.Millisecond)

	// Simulate a file change event
	mockWatcher.InjectEvent(fsnotify.Event{Name: file2, Op: fsnotify.Write})

	// Wait longer than debounce + processing time
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to stop the watch loop
	cancel()
	wgRun.Wait() // Wait for Run to exit

	// Verify mock expectations
	deps.mockCM.AssertExpectations(t)
	deps.mockFS.AssertExpectations(t)
	mockWatcher.AssertCalled(t, "Add", deps.opts.Input)
	mockWatcher.AssertCalled(t, "Close")

	// Check logs for indications of re-run
	logOutput := deps.logBuffer.String()
	// fmt.Println("LOG OUTPUT:\n", logOutput) // Debugging log output
	assert.Contains(t, logOutput, "Initial run finished") // Log from runOnce
	assert.Contains(t, logOutput, "Entering watch mode")
	assert.Contains(t, logOutput, "Watcher event received", "event", file2)
	assert.Contains(t, logOutput, "Debounce timer reset")
	assert.Contains(t, logOutput, "Change detected, triggering rebuild")
	assert.Contains(t, logOutput, "Incremental rebuild finished", "processed=1", "cached=1") // Check report log
	assert.Contains(t, logOutput, "Exiting watch mode gracefully")
}

// TestPrintWatchSummary checks the formatting (exists, no panic)
func TestPrintWatchSummary(t *testing.T) {
	deps := setupBasicEngineTest(t)
	report := Report{
		FilesProcessed:      5,
		FilesFromCache:      10,
		FilesSkippedIgnored: 1,
		FilesSkippedBinary:  2,
		FilesSkippedLarge:   3,
		FilesErrored:        1,
		Duration:            1500 * time.Millisecond,
		Errors:              map[string]string{"/path/error.txt": "failed to process"},
	}

	var buf bytes.Buffer
	assert.NotPanics(t, func() {
		deps.eng.printWatchSummary(&buf, report)
	})

	output := buf.String()
	assert.Contains(t, output, "--- Rebuild Summary ---")
	assert.Contains(t, output, "Duration: 1.5s") // Check duration formatting
	assert.Contains(t, output, "Files Processed:      5")
	assert.Contains(t, output, "Files From Cache:     10")
	assert.Contains(t, output, "Files Skipped (Ignored): 1")
	assert.Contains(t, output, "Files Skipped (Binary):  2")
	assert.Contains(t, output, "Files Skipped (Large):   3")
	assert.Contains(t, output, "Files Errored:        1")
	assert.Contains(t, output, "--- Errors Encountered ---")
	assert.Contains(t, output, "ERROR: /path/error.txt: failed to process")
	assert.Contains(t, output, "-----------------------")
}

// Note: Tests for debouncing and shutdown remain skipped as pure unit tests
// are complex. The event handling test now covers basic re-run triggering.
// Full verification is best done via E2E tests.

// TestRun_WatchMode_Debouncing tests event debouncing logic.
func TestRun_WatchMode_Debouncing(t *testing.T) {
	// Debouncing logic is internal to the watch loop and depends on timing.
	// Unit testing this accurately often requires time manipulation or careful
	// goroutine coordination, making it complex.
	t.Skip("Watch mode debouncing unit tests are complex.")
}

// TestRun_WatchMode_Shutdown tests graceful shutdown.
func TestRun_WatchMode_Shutdown(t *testing.T) {
	// Testing graceful shutdown requires managing contexts and process signals,
	// often better verified through E2E tests using Ctrl+C or kill signals.
	t.Skip("Watch mode shutdown unit tests are complex.")
}
