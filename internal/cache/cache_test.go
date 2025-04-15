package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs" // Added import for fs.WalkDirFunc
	"log/slog"
	"os"
	"path/filepath"
	"strings" // Added for MkdirAll simulation
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/stackvity/convi/internal/config"     // Import config for Options struct
	"github.com/stackvity/convi/internal/filesystem" // Import filesystem interface
)

// --- Mock FileSystem Implementation (Enhanced) ---

// MockFileInfo implements fs.FileInfo for testing.
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

// MockFileSystem implements the FileSystem interface for testing.
type MockFileSystem struct {
	mock.Mock
	mu               sync.RWMutex
	files            map[string][]byte // file path -> content
	fileInfos        map[string]os.FileInfo
	readErrorPaths   map[string]error // paths that should error on read
	statErrorPaths   map[string]error // paths that should error on stat
	writeErrorPaths  map[string]error // paths that should error on write
	renameErrorPaths map[string]error // paths that should error on rename
	removeErrorPaths map[string]error // paths that should error on remove
	mkdirErrorPaths  map[string]error // paths that should error on mkdir

	// Tracking calls for assertions (Recommendation B.2)
	writeCalls  map[string]int
	removeCalls map[string]int
	renameCalls map[string]int
	mkdirCalls  map[string]int
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		files:            make(map[string][]byte),
		fileInfos:        make(map[string]os.FileInfo),
		readErrorPaths:   make(map[string]error),
		statErrorPaths:   make(map[string]error),
		writeErrorPaths:  make(map[string]error),
		renameErrorPaths: make(map[string]error),
		removeErrorPaths: make(map[string]error),
		mkdirErrorPaths:  make(map[string]error),
		writeCalls:       make(map[string]int),
		removeCalls:      make(map[string]int),
		renameCalls:      make(map[string]int),
		mkdirCalls:       make(map[string]int),
	}
}

// Helper to add a file for testing
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

// Helper to simulate errors
func (mfs *MockFileSystem) SimulateReadError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.readErrorPaths[absPath] = err
}
func (mfs *MockFileSystem) SimulateStatError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock() // Corrected from Unlock to Unlock
	absPath, _ := filepath.Abs(path)
	mfs.statErrorPaths[absPath] = err
}
func (mfs *MockFileSystem) SimulateWriteError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.writeErrorPaths[absPath] = err
}
func (mfs *MockFileSystem) SimulateRenameError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.renameErrorPaths[absPath] = err
}
func (mfs *MockFileSystem) SimulateRemoveError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.removeErrorPaths[absPath] = err
}
func (mfs *MockFileSystem) SimulateMkdirError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.mkdirErrorPaths[absPath] = err
}

// Assert helpers (Recommendation B.2)
func (mfs *MockFileSystem) AssertWriteCalled(t *testing.T, path string) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Greater(t, mfs.writeCalls[absPath], 0, "WriteFile was not called for %s", path)
}

func (mfs *MockFileSystem) AssertWriteNotCalled(t *testing.T, path string) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Equal(t, 0, mfs.writeCalls[absPath], "WriteFile should not have been called for %s", path)
}

func (mfs *MockFileSystem) AssertRemoveCalled(t *testing.T, path string) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Greater(t, mfs.removeCalls[absPath], 0, "Remove was not called for %s", path)
}

func (mfs *MockFileSystem) AssertRemoveNotCalled(t *testing.T, path string) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Equal(t, 0, mfs.removeCalls[absPath], "Remove should not have been called for %s", path)
}

func (mfs *MockFileSystem) AssertRenameCalled(t *testing.T, oldpath string) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(oldpath)
	assert.Greater(t, mfs.renameCalls[absPath], 0, "Rename was not called for %s", oldpath)
}

func (mfs *MockFileSystem) ReadFile(name string) ([]byte, error) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(name)
	if err, ok := mfs.readErrorPaths[absPath]; ok {
		return nil, err
	}
	content, exists := mfs.files[absPath]
	if !exists {
		return nil, os.ErrNotExist
	}
	return content, nil
}

func (mfs *MockFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(name)
	// Track calls (Recommendation B.2)
	mfs.writeCalls[absPath]++ // Increment call count for this path
	if err, ok := mfs.writeErrorPaths[absPath]; ok {
		return err
	}
	mfs.files[absPath] = data
	// Update or create FileInfo
	mfs.fileInfos[absPath] = &MockFileInfo{
		name:    filepath.Base(absPath),
		size:    int64(len(data)),
		modTime: time.Now(), // Simulate update time
		mode:    perm,
		isDir:   false,
	}
	return nil
}

func (mfs *MockFileSystem) Stat(name string) (os.FileInfo, error) {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(name)
	if err, ok := mfs.statErrorPaths[absPath]; ok {
		return nil, err
	}
	info, exists := mfs.fileInfos[absPath]
	if !exists {
		// Simulate checking if parent exists for MkdirAll behavior
		parentDir := filepath.Dir(absPath)
		if _, parentExists := mfs.fileInfos[parentDir]; parentExists {
			// If parent exists but file doesn't, return NotExist
			return nil, os.ErrNotExist
		}
		// If parent doesn't exist either, could return a different error or still NotExist
		// For simplicity, stick with NotExist
		return nil, os.ErrNotExist

	}
	return info, nil
}

func (mfs *MockFileSystem) MkdirAll(path string, perm os.FileMode) error {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	// Track calls
	mfs.mkdirCalls[absPath]++
	if err, ok := mfs.mkdirErrorPaths[absPath]; ok {
		return err
	}

	// Check if it already exists
	if info, exists := mfs.fileInfos[absPath]; exists {
		if !info.IsDir() {
			return fmt.Errorf("mkdir %s: file exists but is not a directory", path)
		}
		return nil // Already exists as a directory
	}

	// Simulate creating the directory and necessary parents
	// We need to potentially add multiple entries to fileInfos
	dir := absPath
	// Stop searching for existing parent when reaching the root or an already known directory
	for dir != "." && dir != "/" && dir != "\\" {
		if _, exists := mfs.fileInfos[dir]; exists {
			break // Parent exists
		}
		parent := filepath.Dir(dir)
		if parent == dir { // Check for root case to prevent infinite loop
			break
		}
		dir = parent
	}

	// Add directories from parent down to path
	current := ""
	parts := strings.Split(absPath, string(filepath.Separator))
	// Determine starting point based on existing parent or root
	startIndex := 0
	if dir != "." && dir != "/" && dir != "\\" {
		// Find index corresponding to the existing parent `dir`
		tempPath := ""
		for i, part := range parts {
			if part == "" && i == 0 {
				tempPath = "/"
				continue
			} else if part == "" {
				continue
			}
			tempPath = filepath.Join(tempPath, part)
			if tempPath == dir {
				startIndex = i + 1
				current = dir // Start building from the existing parent
				break
			}
		}
	} else if dir == "/" || dir == "\\" {
		current = dir  // Start building from root
		startIndex = 1 // Skip the root separator part if present
	}

	for i := startIndex; i < len(parts); i++ {
		part := parts[i]
		if part == "" { // Skip empty parts resulting from separators
			continue
		}
		current = filepath.Join(current, part)
		if _, exists := mfs.fileInfos[current]; !exists {
			mfs.fileInfos[current] = &MockFileInfo{
				name:    part,
				size:    0,
				modTime: time.Now(),
				mode:    perm | os.ModeDir,
				isDir:   true,
			}
		}
	}
	return nil
}

// Helper struct to implement fs.DirEntry for the simplified WalkDir mock
type mockDirEntry struct {
	os.FileInfo
}

func (m *mockDirEntry) Type() os.FileMode {
	return m.Mode().Type()
}
func (m *mockDirEntry) Info() (os.FileInfo, error) {
	return m.FileInfo, nil
}

// WalkDir - Basic Mock Implementation (Can be enhanced if needed)
// FIX: Changed fn type from filesystem.WalkDirFunc to fs.WalkDirFunc
func (mfs *MockFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()

	// Need to iterate through mfs.fileInfos and call fn for each item under root
	// This requires careful path prefix matching and handling SkipDir.

	// Let's implement a slightly better version than just root:
	absRoot, _ := filepath.Abs(root)
	pathsToWalk := []string{}

	// Collect paths under root
	for path := range mfs.fileInfos {
		if strings.HasPrefix(path, absRoot) {
			pathsToWalk = append(pathsToWalk, path)
		}
	}
	// Sort paths for consistent order (important for reliable testing)
	// sort.Strings(pathsToWalk) // Requires import "sort"

	for _, path := range pathsToWalk {
		info := mfs.fileInfos[path]
		entry := &mockDirEntry{info}
		err := fn(path, entry, nil) // Pass entry here
		if err != nil {
			if errors.Is(err, filepath.SkipDir) && info.IsDir() {
				// Skip remaining files within this directory
				// This requires more complex logic to track subdirectory contents
				// For now, we'll just stop processing if SkipDir is returned.
				// A full implementation would prune the `pathsToWalk` slice.
				return nil // Stop walking this branch
			}
			return err // Propagate other errors or context cancellation
		}
	}
	return nil
}

func (mfs *MockFileSystem) Remove(name string) error {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(name)
	// Track calls (Recommendation B.2)
	mfs.removeCalls[absPath]++
	if err, ok := mfs.removeErrorPaths[absPath]; ok {
		return err
	}
	deleted := false
	if _, exists := mfs.files[absPath]; exists {
		delete(mfs.files, absPath)
		delete(mfs.fileInfos, absPath)
		deleted = true
	} else if _, exists := mfs.fileInfos[absPath]; exists {
		// Assume it's a directory if not in files map
		delete(mfs.fileInfos, absPath)
		deleted = true
	}

	if !deleted {
		return os.ErrNotExist
	}
	return nil
}

func (mfs *MockFileSystem) Rename(oldpath, newpath string) error {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absOld, _ := filepath.Abs(oldpath)
	absNew, _ := filepath.Abs(newpath)

	// Track calls (Recommendation B.2)
	mfs.renameCalls[absOld]++

	if err, ok := mfs.renameErrorPaths[absOld]; ok {
		return err // Use absOld for error simulation consistent with Persist test
	}

	// Check if old path exists (either file or dir)
	content, fileExists := mfs.files[absOld]
	info, infoExists := mfs.fileInfos[absOld]

	if !fileExists && !infoExists {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: os.ErrNotExist} // Simulate LinkError
	}

	// Handle potential overwrite of newpath - remove if exists
	if _, newExists := mfs.files[absNew]; newExists {
		delete(mfs.files, absNew)
		delete(mfs.fileInfos, absNew)
	} else if _, newInfoExists := mfs.fileInfos[absNew]; newInfoExists {
		delete(mfs.fileInfos, absNew)
	}

	if fileExists {
		mfs.files[absNew] = content
		delete(mfs.files, absOld)
	}
	// Must update the info for the new path
	if infoExists {
		// Update name within the info struct for the new path
		if mockInfo, ok := info.(*MockFileInfo); ok {
			mockInfo.name = filepath.Base(absNew)
			mfs.fileInfos[absNew] = mockInfo
		} else {
			// Fallback if not MockFileInfo (less likely in tests)
			mfs.fileInfos[absNew] = info
		}
		delete(mfs.fileInfos, absOld)
	}

	return nil
}

func (mfs *MockFileSystem) Chtimes(name string, atime time.Time, mtime time.Time) error {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(name)
	info, exists := mfs.fileInfos[absPath]
	if !exists {
		return os.ErrNotExist
	}
	// Update the ModTime in the mock FileInfo
	mockInfo, ok := info.(*MockFileInfo)
	if ok {
		mockInfo.modTime = mtime
		mfs.fileInfos[absPath] = mockInfo // Write back the updated info
		return nil
	}
	return fmt.Errorf("failed to cast FileInfo to MockFileInfo for Chtimes")
}

// --- Test Helpers ---

func setupTest(t *testing.T) (*fileCacheManager, *MockFileSystem, string, *slog.Logger) {
	t.Helper()
	mockFS := NewMockFileSystem()
	tempDir := t.TempDir() // Use testify's TempDir for auto-cleanup
	cacheFilePath := filepath.Join(tempDir, DefaultCacheFileName)
	// Use a NoOp logger for most tests unless specific log output needs verification
	logger := slog.New(slog.NewTextHandler(io.Discard, nil)) // Discard logs by default

	cm, err := NewFileCacheManager(cacheFilePath, mockFS, logger)
	assert.NoError(t, err, "Failed to create cache manager")

	fileCM, ok := cm.(*fileCacheManager)
	assert.True(t, ok, "Cache manager is not of type *fileCacheManager")

	return fileCM, mockFS, cacheFilePath, logger
}

// Helper to calculate realistic hashes
func calculateHashes(t *testing.T, filePath string, fs filesystem.FileSystem, cfg *config.Options, templatePath string) ([]byte, []byte, []byte) {
	t.Helper()
	// Ensure the file exists in the mock FS before hashing source
	if _, err := fs.Stat(filePath); err != nil && errors.Is(err, os.ErrNotExist) {
		// Add dummy content if file doesn't exist for source hash calculation during setup
		if mockFs, ok := fs.(*MockFileSystem); ok {
			mockFs.AddFile(filePath, []byte{}, time.Now())
		} else {
			// If not a mockFS, we can't add the file, so source hash might fail later
			// This is less ideal, but necessary if a real FS were somehow passed.
		}
	}

	confHash, err := CalculateConfigHash(cfg)
	assert.NoError(t, err)

	tplHash, err := CalculateTemplateHash(templatePath, fs)
	assert.NoError(t, err)

	srcHash, err := calculateSourceHash(filePath, fs)
	assert.NoError(t, err)

	return confHash, tplHash, srcHash
}

// Helper to create a basic config for hashing
func createTestConfig() *config.Options {
	return &config.Options{
		LanguageMappings: map[string]string{".go": "golang"},
		FrontMatter: config.FrontMatterConfig{
			Enabled: false,
		},
		BinaryMode:    "skip",
		LargeFileMode: "skip",
	}
}

// --- Test Cases ---

// TestNewFileCacheManager tests creation and loading of the cache file.
// Covers initial setup and potentially parts of P.2.1 (loading).
// Relates to TASK-CONVI-022 (Define CacheManager), TASK-CONVI-026 (Basic test files)
func TestNewFileCacheManager(t *testing.T) {
	t.Run("NewCache_NoFileExists", func(t *testing.T) {
		cm, mockFS, cacheFilePath, _ := setupTest(t)
		assert.NotNil(t, cm)
		assert.Empty(t, cm.cacheData, "Cache should be empty initially")
		assert.True(t, cm.isDirty, "Cache should be marked dirty to create file on first persist")

		// Verify file doesn't exist yet
		_, err := mockFS.Stat(cacheFilePath)
		assert.ErrorIs(t, err, os.ErrNotExist, "Cache file should not exist yet")
	})

	t.Run("LoadCache_FileExists_Valid", func(t *testing.T) {
		mockFS := NewMockFileSystem()
		tempDir := t.TempDir()
		cacheFilePath := filepath.Join(tempDir, DefaultCacheFileName)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil)) // Use discard logger

		// Pre-populate cache data
		modTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second) // Truncate for comparison
		absPath, _ := filepath.Abs("/abs/path/to/file.go")              // Ensure key is absolute
		initialCache := map[string]CacheEntry{
			absPath: {
				ModTime:    modTime,
				Size:       100,
				ConfigHash: []byte("conf1"),
			},
		}
		var buf bytes.Buffer
		encoder := gob.NewEncoder(&buf)
		err := encoder.Encode(initialCache)
		assert.NoError(t, err)

		// Create the cache file in the mock filesystem
		// Need to ensure the parent directory exists for WriteFile if it simulates mkdir needs
		cacheDir := filepath.Dir(cacheFilePath)
		mockFS.fileInfos[cacheDir] = &MockFileInfo{name: filepath.Base(cacheDir), isDir: true, mode: 0755 | os.ModeDir}

		err = mockFS.WriteFile(cacheFilePath, buf.Bytes(), 0644)
		assert.NoError(t, err)
		// Set correct ModTime for the cache file itself
		mockFS.fileInfos[cacheFilePath].(*MockFileInfo).modTime = time.Now()

		// Create the manager, which should load the file
		cm, err := NewFileCacheManager(cacheFilePath, mockFS, logger)
		assert.NoError(t, err)
		fileCM := cm.(*fileCacheManager)

		assert.NotEmpty(t, fileCM.cacheData, "Cache should not be empty after loading")
		assert.Equal(t, 1, len(fileCM.cacheData), "Cache should have one entry")
		entry, exists := fileCM.cacheData[absPath]
		assert.True(t, exists, "Expected cache entry not found")
		assert.Equal(t, int64(100), entry.Size)
		// Use WithinDuration for robust time comparison
		assert.WithinDuration(t, modTime, entry.ModTime, time.Millisecond, "ModTime mismatch")
		assert.Equal(t, []byte("conf1"), entry.ConfigHash)
		assert.False(t, fileCM.isDirty, "Cache should not be dirty after successful load")
	})

	t.Run("LoadCache_FileExists_InvalidGob", func(t *testing.T) {
		mockFS := NewMockFileSystem()
		tempDir := t.TempDir()
		cacheFilePath := filepath.Join(tempDir, DefaultCacheFileName)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil)) // Use discard logger

		// Ensure parent dir exists
		cacheDir := filepath.Dir(cacheFilePath)
		mockFS.fileInfos[cacheDir] = &MockFileInfo{name: filepath.Base(cacheDir), isDir: true, mode: 0755 | os.ModeDir}

		// Create a corrupted cache file
		err := mockFS.WriteFile(cacheFilePath, []byte("this is not gob data"), 0644)
		assert.NoError(t, err)

		// Create the manager, expect loading to fail but not fatal
		cm, err := NewFileCacheManager(cacheFilePath, mockFS, logger)
		assert.NoError(t, err, "NewFileCacheManager should handle load errors gracefully") // Expect no error from constructor itself
		fileCM := cm.(*fileCacheManager)

		assert.Empty(t, fileCM.cacheData, "Cache should be empty after load error")
		assert.True(t, fileCM.isDirty, "Cache should be marked dirty after load error")
	})

	t.Run("LoadCache_FileExists_Empty", func(t *testing.T) {
		mockFS := NewMockFileSystem()
		tempDir := t.TempDir()
		cacheFilePath := filepath.Join(tempDir, DefaultCacheFileName)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))

		// Ensure parent dir exists
		cacheDir := filepath.Dir(cacheFilePath)
		mockFS.fileInfos[cacheDir] = &MockFileInfo{name: filepath.Base(cacheDir), isDir: true, mode: 0755 | os.ModeDir}

		// Create an empty cache file
		err := mockFS.WriteFile(cacheFilePath, []byte{}, 0644)
		assert.NoError(t, err)

		// Create the manager
		cm, err := NewFileCacheManager(cacheFilePath, mockFS, logger)
		assert.NoError(t, err)
		fileCM := cm.(*fileCacheManager)

		assert.Empty(t, fileCM.cacheData, "Cache should be empty when loading an empty file")
		assert.True(t, fileCM.isDirty, "Cache should be marked dirty after loading empty file to save initial state")
	})
}

// TestCacheCheck tests the core cache hit/miss logic.
// Covers P.2.1, including influence of P.2.3 hashes.
// Relates to TASK-CONVI-030, TASK-CONVI-045 (Test Check), TASK-CONVI-074, TASK-CONVI-075, TASK-CONVI-083, TASK-CONVI-XXY (Invalidation)
func TestCacheCheck(t *testing.T) {
	cm, mockFS, _, _ := setupTest(t)
	filePath := "/app/src/main.go" // Use absolute path for consistency
	absFilePath, _ := filepath.Abs(filePath)
	cfg := createTestConfig()
	templatePath := "" // No template initially
	modTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second)

	// Ensure file exists before calculating hashes
	fileContent := []byte("package main")
	mockFS.AddFile(filePath, fileContent, modTime)

	// Calculate expected hashes
	confHash, tplHash, srcHash := calculateHashes(t, filePath, mockFS, cfg, templatePath)

	// Populate cache (after file exists and hashes calculated)
	cacheEntry := CacheEntry{
		ModTime:      modTime,
		Size:         int64(len(fileContent)),
		ConfigHash:   confHash,
		TemplateHash: tplHash,
		SourceHash:   srcHash, // Include source hash
		OutputPath:   "/out/main.go.md",
	}
	cm.cacheData[absFilePath] = cacheEntry

	// Test Cases
	t.Run("CacheHit_ExactMatch", func(t *testing.T) {
		status, err := cm.Check(filePath, confHash, tplHash)
		assert.NoError(t, err)
		assert.Equal(t, StatusHit, status)
	})

	t.Run("CacheMiss_FileNotFoundInCache", func(t *testing.T) {
		status, err := cm.Check("/app/src/other.go", confHash, tplHash)
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status)
	})

	t.Run("CacheMiss_ModTimeMismatch", func(t *testing.T) {
		// Use Chtimes which is now implemented in mock
		err := mockFS.Chtimes(filePath, time.Now(), modTime.Add(time.Second))
		assert.NoError(t, err)

		status, err := cm.Check(filePath, confHash, tplHash)
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status)

		// Restore
		err = mockFS.Chtimes(filePath, time.Now(), modTime)
		assert.NoError(t, err)
	})

	t.Run("CacheMiss_SizeMismatch", func(t *testing.T) {
		newContent := []byte("package main // modified")
		mockFS.AddFile(filePath, newContent, modTime) // Modifies size implicitly via AddFile helper

		status, err := cm.Check(filePath, confHash, tplHash)
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status)

		// Restore
		mockFS.AddFile(filePath, fileContent, modTime)
	})

	t.Run("CacheMiss_ConfigHashMismatch", func(t *testing.T) {
		cfg.BinaryMode = "placeholder"                                               // Change config
		newConfHash, _, _ := calculateHashes(t, filePath, mockFS, cfg, templatePath) // Re-calculate hashes AFTER file exists
		cfg.BinaryMode = "skip"                                                      // Restore

		status, err := cm.Check(filePath, newConfHash, tplHash) // Check against new hash
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status)
	})

	t.Run("CacheMiss_TemplateHashMismatch", func(t *testing.T) {
		templatePath = "/app/tpl.tmpl"
		mockFS.AddFile(templatePath, []byte("{{.Content}} v1"), time.Now())
		_, newTplHash, _ := calculateHashes(t, filePath, mockFS, cfg, templatePath)

		status, err := cm.Check(filePath, confHash, newTplHash) // Check against new template hash
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status)
	})

	t.Run("CacheMiss_SourceHashMismatch", func(t *testing.T) {
		newContent := []byte("package main // comment")
		mockFS.AddFile(filePath, newContent, modTime) // Different content, same modTime

		status, err := cm.Check(filePath, confHash, tplHash)
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status, "Should miss due to source hash mismatch")

		// Restore
		mockFS.AddFile(filePath, fileContent, modTime)
	})

	t.Run("CacheMiss_FileStatError", func(t *testing.T) {
		statErr := errors.New("permission denied")
		mockFS.SimulateStatError(filePath, statErr) // Make Stat fail

		status, err := cm.Check(filePath, confHash, tplHash)
		// Check should handle stat error gracefully and report miss
		assert.NoError(t, err, "Check itself shouldn't error, should report miss")
		assert.Equal(t, StatusMiss, status)

		delete(mockFS.statErrorPaths, absFilePath) // Clear error simulation
	})

	t.Run("Check_AbsolutePathInternal", func(t *testing.T) {
		// Test that Check uses absolute paths internally for map keys
		relPath := "src/main.go" // Relative path
		cwd, _ := os.Getwd()
		absPathForRel := filepath.Join(cwd, relPath) // Calculate absolute path

		// Add file using absolute path for consistency
		mockFS.AddFile(absPathForRel, fileContent, modTime)

		// Recalculate hashes using absolute path
		confHashRel, tplHashRel, srcHashRel := calculateHashes(t, absPathForRel, mockFS, cfg, templatePath)

		// Add cache entry using the absolute path key
		relCacheEntry := CacheEntry{
			ModTime:      modTime,
			Size:         int64(len(fileContent)),
			ConfigHash:   confHashRel,
			TemplateHash: tplHashRel,
			SourceHash:   srcHashRel,
			OutputPath:   "/out/src/main.go.md",
		}
		cm.cacheData[absPathForRel] = relCacheEntry

		// Check using the relative path
		status, err := cm.Check(relPath, confHashRel, tplHashRel)
		assert.NoError(t, err)
		assert.Equal(t, StatusHit, status, "Check should use absolute path internally and find the entry")
	})

}

// TestCacheUpdate tests adding/updating entries in the cache.
// Covers P.2.2. Relates to TASK-CONVI-031, TASK-CONVI-046 (Test Update)
func TestCacheUpdate(t *testing.T) {
	cm, mockFS, _, _ := setupTest(t)
	filePath := "/app/src/update.txt"
	absFilePath, _ := filepath.Abs(filePath)
	modTime := time.Now().Truncate(time.Second)
	fileContentV1 := []byte("content v1")
	mockFS.AddFile(filePath, fileContentV1, modTime)
	cfg := createTestConfig()
	confHash, tplHash, srcHash := calculateHashes(t, filePath, mockFS, cfg, "")

	entryV1 := CacheEntry{
		ModTime:      modTime,
		Size:         int64(len(fileContentV1)), // Use actual size
		ConfigHash:   confHash,
		TemplateHash: tplHash,
		SourceHash:   srcHash,
		OutputPath:   "/out/update.txt.md",
	}

	// 1. Add initial entry
	err := cm.Update(filePath, entryV1)
	assert.NoError(t, err)
	assert.True(t, cm.isDirty, "Cache should be dirty after update")
	assert.Contains(t, cm.cacheData, absFilePath, "Cache should contain the new entry")
	assert.Equal(t, entryV1, cm.cacheData[absFilePath])

	// 2. Update existing entry
	modTimeV2 := modTime.Add(time.Minute)
	fileContentV2 := []byte("content v2 - larger")
	mockFS.AddFile(filePath, fileContentV2, modTimeV2)
	// Recalculate hashes with updated content/metadata
	confHashV2, tplHashV2, srcHashV2 := calculateHashes(t, filePath, mockFS, cfg, "") // Assume config/template unchanged

	entryV2 := CacheEntry{
		ModTime:      modTimeV2,
		Size:         int64(len(fileContentV2)), // Use actual size
		ConfigHash:   confHashV2,
		TemplateHash: tplHashV2,
		SourceHash:   srcHashV2,
		OutputPath:   "/out/update.txt.md",
	}
	err = cm.Update(filePath, entryV2)
	assert.NoError(t, err)
	assert.True(t, cm.isDirty)
	assert.Contains(t, cm.cacheData, absFilePath)
	assert.Equal(t, entryV2, cm.cacheData[absFilePath], "Cache entry should be updated")
	assert.Equal(t, 1, len(cm.cacheData), "Cache should still only have one entry")
}

// TestCachePersist tests saving the cache data to a file atomically.
// Covers P.2.4. Relates to TASK-CONVI-032, TASK-CONVI-046 (Test Persist)
func TestCachePersist(t *testing.T) {
	cm, mockFS, cacheFilePath, _ := setupTest(t)
	filePath := "/app/src/persist.txt"
	absFilePath, _ := filepath.Abs(filePath)
	modTime := time.Now().Truncate(time.Second)
	fileContent := []byte("persist me")
	mockFS.AddFile(filePath, fileContent, modTime)
	cfg := createTestConfig()
	confHash, tplHash, srcHash := calculateHashes(t, filePath, mockFS, cfg, "")

	entry := CacheEntry{ModTime: modTime, Size: int64(len(fileContent)), ConfigHash: confHash, TemplateHash: tplHash, SourceHash: srcHash}

	// Ensure directory exists in mock (MkdirAll should be called by Persist)
	cacheDir := filepath.Dir(cacheFilePath)
	mockFS.fileInfos[cacheDir] = &MockFileInfo{name: filepath.Base(cacheDir), isDir: true, mode: 0755 | os.ModeDir}

	// 1. Update cache to make it dirty
	err := cm.Update(filePath, entry)
	assert.NoError(t, err)
	assert.True(t, cm.isDirty)

	// 2. Persist
	err = cm.Persist()
	assert.NoError(t, err)
	assert.False(t, cm.isDirty, "Cache should not be dirty after persist")

	// 3. Verify file content
	persistedData, err := mockFS.ReadFile(cacheFilePath)
	assert.NoError(t, err, "Cache file should exist after persist")
	assert.NotEmpty(t, persistedData, "Persisted cache file should not be empty")

	// 4. Verify content can be decoded and matches
	var decodedCache map[string]CacheEntry
	decoder := gob.NewDecoder(bytes.NewReader(persistedData))
	err = decoder.Decode(&decodedCache)
	assert.NoError(t, err, "Failed to decode persisted cache data")
	assert.Equal(t, 1, len(decodedCache))
	assert.Contains(t, decodedCache, absFilePath)
	assert.Equal(t, entry, decodedCache[absFilePath])

	// 5. Persist again when not dirty
	// Reset call counts before persisting again
	mockFS.writeCalls = make(map[string]int)
	mockFS.renameCalls = make(map[string]int)
	err = cm.Persist()
	assert.NoError(t, err, "Persisting non-dirty cache should not error")
	mockFS.AssertWriteNotCalled(t, cacheFilePath) // Check specific file was not written (temp file name is tricky)
	// More robustly: Check rename was not called for the final path
	assert.Equal(t, 0, mockFS.renameCalls[cacheFilePath], "Rename should not be called for non-dirty cache")
}

// TestCachePersist_Atomic ensures temporary file and rename are used and cleanup happens.
// Relates to TASK-CONVI-032 atomicity requirement. (Incorporates Recommendation B.2)
func TestCachePersist_Atomic(t *testing.T) {
	cm, mockFS, cacheFilePath, _ := setupTest(t)
	tempFilePath := cacheFilePath + ".tmp" // Approximate temp file path for assertions

	// Add an entry to make cache dirty
	entry := CacheEntry{ModTime: time.Now(), Size: 1}
	_ = cm.Update("/file1", entry) // Ignore error for setup

	// --- Test success case ---
	err := cm.Persist()
	assert.NoError(t, err)
	// Assert temp file was written and final rename occurred
	mockFS.AssertWriteCalled(t, tempFilePath)  // Check write was attempted on temp path
	mockFS.AssertRenameCalled(t, tempFilePath) // Check rename was attempted from temp path

	// Check final file exists
	_, statErr := mockFS.Stat(cacheFilePath)
	assert.NoError(t, statErr, "Final cache file should exist after successful persist")

	// --- Test rename failure ---
	mockFS.writeCalls = make(map[string]int) // Reset counters
	mockFS.renameCalls = make(map[string]int)
	mockFS.removeCalls = make(map[string]int)
	_ = cm.Update("/file2", entry) // Make dirty again
	// Simulate rename failure using the actual target path for the rename op
	mockFS.SimulateRenameError(tempFilePath, fmt.Errorf("disk full")) // Simulate error on the temp file path rename

	err = cm.Persist()
	assert.Error(t, err, "Persist should fail if rename fails")
	assert.True(t, cm.isDirty, "Cache should remain dirty after failed persist")

	// Assert temp file write was attempted, rename was attempted, AND remove (cleanup) was attempted
	mockFS.AssertWriteCalled(t, tempFilePath)
	mockFS.AssertRenameCalled(t, tempFilePath)
	mockFS.AssertRemoveCalled(t, tempFilePath) // Assert cleanup occurred

	// Check final file doesn't exist or is the old version (state is unchanged)
	// We only know the rename failed, so the final file should not have the new content.

	// Clear rename error simulation for next test
	delete(mockFS.renameErrorPaths, tempFilePath)

	// --- Test temp write failure ---
	mockFS.writeCalls = make(map[string]int) // Reset counters
	mockFS.renameCalls = make(map[string]int)
	mockFS.removeCalls = make(map[string]int)
	_ = cm.Update("/file3", entry) // Make dirty again
	// Simulate write failure (will apply to the temp file)
	mockFS.SimulateWriteError(tempFilePath, fmt.Errorf("write permission denied"))

	err = cm.Persist()
	assert.Error(t, err, "Persist should fail if writing temp file fails")
	assert.True(t, cm.isDirty, "Cache should remain dirty after failed persist")

	// Assert write was attempted, rename was NOT attempted, remove WAS attempted
	mockFS.AssertWriteCalled(t, tempFilePath)
	assert.Equal(t, 0, mockFS.renameCalls[tempFilePath], "Rename should not be called if write fails")
	mockFS.AssertRemoveCalled(t, tempFilePath) // Assert cleanup attempted

	_, statErrFinal := mockFS.Stat(cacheFilePath) // Check original file state
	assert.NoError(t, statErrFinal, "Original cache file state should be unchanged after write failure")
	delete(mockFS.writeErrorPaths, tempFilePath) // Cleanup simulation
}

// TestCacheClear tests clearing the cache.
// Covers P.2.5 (related logic). Relates to TASK-CONVI-026, TASK-CONVI-046 (Test Clear)
func TestCacheClear(t *testing.T) {
	cm, mockFS, cacheFilePath, _ := setupTest(t)

	// 1. Add data and persist to ensure file exists
	filePath := "/app/src/clear.txt"
	absFilePath, _ := filepath.Abs(filePath)
	entry := CacheEntry{ModTime: time.Now(), Size: 1}
	// Ensure parent directory exists for persist
	cacheDir := filepath.Dir(cacheFilePath)
	mockFS.fileInfos[cacheDir] = &MockFileInfo{name: filepath.Base(cacheDir), isDir: true, mode: 0755 | os.ModeDir}
	err := cm.Update(filePath, entry)
	assert.NoError(t, err)
	err = cm.Persist()
	assert.NoError(t, err)
	assert.False(t, cm.isDirty)
	assert.Contains(t, cm.cacheData, absFilePath)
	_, statErr := mockFS.Stat(cacheFilePath)
	assert.NoError(t, statErr, "Cache file should exist before clear")
	mockFS.AssertRemoveNotCalled(t, cacheFilePath) // Verify Remove wasn't called yet

	// 2. Clear the cache
	err = cm.Clear()
	assert.NoError(t, err)
	assert.Empty(t, cm.cacheData, "In-memory cache should be empty after clear")
	mockFS.AssertRemoveCalled(t, cacheFilePath) // Verify Remove was called
	// isDirty might be true after Clear to force persist empty state, or false if Remove succeeded.
	// Let's check if the file was removed.
	_, statErr = mockFS.Stat(cacheFilePath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "Cache file should not exist after clear")

	// 3. Clear again when file doesn't exist
	mockFS.removeCalls = make(map[string]int) // Reset counter
	err = cm.Clear()
	assert.NoError(t, err, "Clearing non-existent cache should not error")
	assert.Empty(t, cm.cacheData)
	mockFS.AssertRemoveCalled(t, cacheFilePath) // Verify Remove was still attempted (even if file not found)
}

// TestCacheClear_RemoveError tests clearing when file removal fails.
func TestCacheClear_RemoveError(t *testing.T) {
	cm, mockFS, cacheFilePath, _ := setupTest(t)

	// Add data and persist
	// Ensure parent directory exists for persist
	cacheDir := filepath.Dir(cacheFilePath)
	mockFS.fileInfos[cacheDir] = &MockFileInfo{name: filepath.Base(cacheDir), isDir: true, mode: 0755 | os.ModeDir}
	_ = cm.Update("/app/file", CacheEntry{})
	err := cm.Persist()
	assert.NoError(t, err)
	_, statErr := mockFS.Stat(cacheFilePath)
	assert.NoError(t, statErr)

	// Simulate remove error
	removeErr := fmt.Errorf("permission denied")
	mockFS.SimulateRemoveError(cacheFilePath, removeErr)

	// Clear the cache - should succeed in memory but log warning
	err = cm.Clear()
	// Implementation returns nil even on remove error, memory clear succeeded.
	assert.NoError(t, err, "Clear should succeed in memory even if file remove fails")
	assert.Empty(t, cm.cacheData, "In-memory cache should be empty")
	mockFS.AssertRemoveCalled(t, cacheFilePath) // Verify Remove was attempted

	// Verify file still exists in mock FS due to simulated error
	_, statErr = mockFS.Stat(cacheFilePath)
	assert.NoError(t, statErr, "Cache file should still exist due to simulated remove error")
}

// TestCalculateConfigHash tests the stability and correctness of config hashing.
// Covers P.2.3 aspect. Relates to TASK-CONVI-033, TASK-CONVI-075
func TestCalculateConfigHash(t *testing.T) {
	opts1 := &config.Options{
		LanguageMappings: map[string]string{".a": "langA", ".b": "langB"},
		FrontMatter: config.FrontMatterConfig{
			Enabled: true,
			Format:  "yaml",
			Static:  map[string]interface{}{"author": "test"},
			Include: []string{"FilePath"},
		},
		BinaryMode:    "skip",
		LargeFileMode: "skip",
		// Irrelevant fields
		Input:       "/in",
		Output:      "/out",
		Concurrency: 4,
	}

	opts2 := &config.Options{ // Same relevant fields, different order maps/slices, different irrelevant
		LanguageMappings: map[string]string{".b": "langB", ".a": "langA"}, // Order changed
		FrontMatter: config.FrontMatterConfig{
			Enabled: true,
			Format:  "yaml",
			Static:  map[string]interface{}{"author": "test"},
			Include: []string{"FilePath"}, // Order same here, but test map order above
		},
		BinaryMode:    "skip",
		LargeFileMode: "skip",
		Input:         "/other",
		Output:        "/diff",
		Concurrency:   8,
	}

	opts3 := &config.Options{ // Different relevant field
		LanguageMappings: map[string]string{".a": "langA", ".b": "langB"},
		FrontMatter: config.FrontMatterConfig{
			Enabled: true,
			Format:  "toml", // <-- Changed
			Static:  map[string]interface{}{"author": "test"},
			Include: []string{"FilePath"},
		},
		BinaryMode:    "skip",
		LargeFileMode: "skip",
	}

	hash1, err1 := CalculateConfigHash(opts1)
	hash2, err2 := CalculateConfigHash(opts2)
	hash3, err3 := CalculateConfigHash(opts3)

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)

	assert.NotEmpty(t, hash1)
	assert.NotEmpty(t, hash2)
	assert.NotEmpty(t, hash3)

	assert.Equal(t, hash1, hash2, "Hashes should be the same for same relevant config regardless of map order or irrelevant fields")
	assert.NotEqual(t, hash1, hash3, "Hashes should differ when relevant config changes")
}

// TestCalculateTemplateHash tests hashing of template files.
// Covers P.2.3 aspect. Relates to TASK-CONVI-033, TASK-CONVI-074
func TestCalculateTemplateHash(t *testing.T) {
	mockFS := NewMockFileSystem()
	tempDir := t.TempDir()
	templatePath1 := filepath.Join(tempDir, "template1.tmpl")
	templatePath2 := filepath.Join(tempDir, "template2.tmpl")
	nonExistentPath := filepath.Join(tempDir, "nonexistent.tmpl")
	absTplPath1, _ := filepath.Abs(templatePath1) // For simulation key

	content1 := []byte("Template Content V1")
	content2 := []byte("Template Content V2")

	mockFS.AddFile(templatePath1, content1, time.Now())
	mockFS.AddFile(templatePath2, content2, time.Now())

	// 1. No template file
	hashNil, errNil := CalculateTemplateHash("", mockFS)
	assert.NoError(t, errNil)
	assert.Nil(t, hashNil, "Hash should be nil when no template path is provided")

	// 2. Template 1
	hash1, err1 := CalculateTemplateHash(templatePath1, mockFS)
	assert.NoError(t, err1)
	assert.NotEmpty(t, hash1)

	// 3. Template 1 again (should be same hash)
	hash1Again, err1Again := CalculateTemplateHash(templatePath1, mockFS)
	assert.NoError(t, err1Again)
	assert.Equal(t, hash1, hash1Again, "Hashing the same template content should yield the same hash")

	// 4. Template 2 (should be different hash)
	hash2, err2 := CalculateTemplateHash(templatePath2, mockFS)
	assert.NoError(t, err2)
	assert.NotEmpty(t, hash2)
	assert.NotEqual(t, hash1, hash2, "Different template content should yield different hashes")

	// 5. Non-existent template file
	hashNon, errNon := CalculateTemplateHash(nonExistentPath, mockFS)
	assert.Error(t, errNon, "Should return error for non-existent template file")
	assert.ErrorIs(t, errNon, os.ErrNotExist) // Check specific error if possible
	assert.Nil(t, hashNon)

	// 6. Template file read error simulation
	readErr := fmt.Errorf("permission denied")
	mockFS.SimulateReadError(templatePath1, readErr)
	hashReadErr, errReadErr := CalculateTemplateHash(templatePath1, mockFS)
	assert.Error(t, errReadErr, "Should return error on template read error")
	assert.ErrorIs(t, errReadErr, readErr) // Check underlying error
	assert.Nil(t, hashReadErr)
	delete(mockFS.readErrorPaths, absTplPath1) // cleanup simulation
}

// TestCalculateSourceHash tests hashing of source file content.
// Relates to TASK-CONVI-083
func TestCalculateSourceHash(t *testing.T) {
	mockFS := NewMockFileSystem()
	tempDir := t.TempDir()
	filePath1 := filepath.Join(tempDir, "source1.go")
	filePath2 := filepath.Join(tempDir, "source2.go")
	filePath3 := filepath.Join(tempDir, "source1_copy.go") // Same content as 1
	nonExistentPath := filepath.Join(tempDir, "nonexistent.go")
	absFilePath1, _ := filepath.Abs(filePath1) // For simulation key

	content1 := []byte("package main\nfunc main() {}")
	content2 := []byte("package other\nfunc helper() {}")

	mockFS.AddFile(filePath1, content1, time.Now())
	mockFS.AddFile(filePath2, content2, time.Now())
	mockFS.AddFile(filePath3, content1, time.Now()) // Same content as file 1

	hash1, err1 := calculateSourceHash(filePath1, mockFS)
	assert.NoError(t, err1)
	assert.NotEmpty(t, hash1)

	hash2, err2 := calculateSourceHash(filePath2, mockFS)
	assert.NoError(t, err2)
	assert.NotEmpty(t, hash2)

	hash3, err3 := calculateSourceHash(filePath3, mockFS)
	assert.NoError(t, err3)
	assert.NotEmpty(t, hash3)

	// Verify different content yields different hash
	assert.NotEqual(t, hash1, hash2)

	// Verify same content yields same hash
	assert.Equal(t, hash1, hash3)

	// Test non-existent file
	hashNon, errNon := calculateSourceHash(nonExistentPath, mockFS)
	assert.Error(t, errNon, "Should return error for non-existent source file")
	assert.ErrorIs(t, errNon, os.ErrNotExist)
	assert.Nil(t, hashNon)

	// Test read error
	readErr := fmt.Errorf("read error")
	mockFS.SimulateReadError(filePath1, readErr)
	hashReadErr, errReadErr := calculateSourceHash(filePath1, mockFS)
	assert.Error(t, errReadErr, "Should return error on source read error")
	// Use ErrorContains because the error wraps os.ErrNotExist
	assert.ErrorContains(t, errReadErr, "read error")
	assert.Nil(t, hashReadErr)
	delete(mockFS.readErrorPaths, absFilePath1) // cleanup
}

// TestDefaultCachePath tests the cache path generation.
func TestDefaultCachePath(t *testing.T) {
	assert.Equal(t, filepath.Join("output", ".convi.cache"), DefaultCachePath("output"))
	assert.Equal(t, filepath.Join("output", "dir", ".convi.cache"), DefaultCachePath("output/dir"))
	assert.Equal(t, filepath.Join(".", ".convi.cache"), DefaultCachePath("."))
	// Test cleaning
	assert.Equal(t, filepath.Join("output", ".convi.cache"), DefaultCachePath("output/"))
	assert.Equal(t, filepath.Join("output", ".convi.cache"), DefaultCachePath("./output/./"))
}

// TestNoOpCacheManager verifies the no-op implementation.
// Relates to Recommendation B.1
func TestNoOpCacheManager(t *testing.T) {
	cm := NewNoOpCacheManager()
	assert.NotNil(t, cm)

	entry := CacheEntry{ModTime: time.Now(), Size: 1}
	confHash := simpleHash(t, "config")
	tplHash := simpleHash(t, "tpl")

	// Check should always miss
	status, err := cm.Check("/path/file", confHash, tplHash)
	assert.NoError(t, err)
	assert.Equal(t, StatusMiss, status)

	// Update should do nothing and not error
	err = cm.Update("/path/file", entry)
	assert.NoError(t, err)

	// Persist should do nothing and not error
	err = cm.Persist()
	assert.NoError(t, err)

	// Clear should do nothing and not error
	err = cm.Clear()
	assert.NoError(t, err)
}

// --- Test Main (If needed for setup/teardown) ---
// TestMain remains unchanged if no global setup/teardown is needed.
// func TestMain(m *testing.M) {
// 	// Setup code here (if any)
// 	exitCode := m.Run()
// 	// Teardown code here (if any)
// 	os.Exit(exitCode)
// }

// Helper function to create a simple config hash for tests where exact config doesn't matter
// simpleHash remains unchanged.
func simpleHash(t *testing.T, s string) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// Test Cache Invalidation Thoroughly
// TestCacheInvalidationScenarios remains largely unchanged, but relies on the improved mock FS setup.
func TestCacheInvalidationScenarios(t *testing.T) {
	cm, mockFS, _, _ := setupTest(t)
	filePath := "/app/src/file.go"
	absFilePath, _ := filepath.Abs(filePath)
	templatePath := "/app/template.tmpl"
	cfg := createTestConfig()
	modTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second)

	// Initial state
	fileContentV1 := []byte("content v1")
	templateContentV1 := []byte("template v1: {{.Content}}")
	mockFS.AddFile(filePath, fileContentV1, modTime)
	mockFS.AddFile(templatePath, templateContentV1, modTime)

	cfgHashV1, tplHashV1, srcHashV1 := calculateHashes(t, filePath, mockFS, cfg, templatePath)

	// Populate cache
	entryV1 := CacheEntry{
		ModTime:      modTime,
		Size:         int64(len(fileContentV1)),
		ConfigHash:   cfgHashV1,
		TemplateHash: tplHashV1,
		SourceHash:   srcHashV1,
	}
	cm.cacheData[absFilePath] = entryV1

	// --- Scenario 1: Source Content Change ---
	t.Run("Invalidation_SourceContentChange", func(t *testing.T) {
		// Change content, keep modtime/size same (tests source hash importance)
		fileContentV2 := []byte("content v2")            // Different content
		mockFS.AddFile(filePath, fileContentV2, modTime) // Keep modTime same

		status, err := cm.Check(filePath, cfgHashV1, tplHashV1)
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status, "Cache should miss when source content hash changes")

		// Restore
		mockFS.AddFile(filePath, fileContentV1, modTime)
	})

	// --- Scenario 2: Source ModTime/Size Change ---
	t.Run("Invalidation_SourceMetaChange", func(t *testing.T) {
		// Change mod time using Chtimes
		err := mockFS.Chtimes(filePath, time.Now(), modTime.Add(time.Second))
		assert.NoError(t, err)

		status, err := cm.Check(filePath, cfgHashV1, tplHashV1)
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status, "Cache should miss when source mod time changes")

		// Restore mod time
		err = mockFS.Chtimes(filePath, time.Now(), modTime)
		assert.NoError(t, err)
		// Verify hit after restore
		status, err = cm.Check(filePath, cfgHashV1, tplHashV1)
		assert.NoError(t, err)
		assert.Equal(t, StatusHit, status, "Cache should hit after restoring mod time")
	})

	// --- Scenario 3: Template Content Change ---
	t.Run("Invalidation_TemplateChange", func(t *testing.T) {
		// Change template content
		templateContentV2 := []byte("template v2: {{.Content}}")
		mockFS.AddFile(templatePath, templateContentV2, time.Now())
		_, newTplHash, _ := calculateHashes(t, filePath, mockFS, cfg, templatePath) // Recalculate template hash

		status, err := cm.Check(filePath, cfgHashV1, newTplHash) // Use new template hash
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status, "Cache should miss when template hash changes")

		// Restore template
		mockFS.AddFile(templatePath, templateContentV1, time.Now())
	})

	// --- Scenario 4: Relevant Config Change ---
	t.Run("Invalidation_RelevantConfigChange", func(t *testing.T) {
		// Change relevant config (e.g., languageMappings)
		cfg.LanguageMappings[".go"] = "GoLang"                                      // Change value
		newCfgHash, _, _ := calculateHashes(t, filePath, mockFS, cfg, templatePath) // Recalculate config hash

		status, err := cm.Check(filePath, newCfgHash, tplHashV1) // Use new config hash
		assert.NoError(t, err)
		assert.Equal(t, StatusMiss, status, "Cache should miss when relevant config hash changes")

		// Restore config
		cfg.LanguageMappings[".go"] = "golang"
	})

	// --- Scenario 5: Irrelevant Config Change ---
	t.Run("Invalidation_IrrelevantConfigChange", func(t *testing.T) {
		// Change irrelevant config (e.g., concurrency)
		cfg.Concurrency = 8
		// Config hash should NOT change because CalculateConfigHash ignores Concurrency
		sameCfgHash, err := CalculateConfigHash(cfg)
		assert.NoError(t, err)
		assert.Equal(t, cfgHashV1, sameCfgHash, "Config hash should not change for irrelevant fields")

		status, err := cm.Check(filePath, sameCfgHash, tplHashV1) // Use config hash
		assert.NoError(t, err)
		assert.Equal(t, StatusHit, status, "Cache should hit when only irrelevant config changes")

		// Restore config
		cfg.Concurrency = 0
	})
}

// Helper to marshal config for hash calculation comparison
// marshalConfigForHash remains unchanged.
func marshalConfigForHash(cfg *config.Options) ([]byte, error) {
	cacheRelevantConfig := struct {
		LanguageMappings map[string]string        `json:"languageMappings"`
		FrontMatter      config.FrontMatterConfig `json:"frontMatter"`
		BinaryMode       string                   `json:"binaryMode"`
		LargeFileMode    string                   `json:"largeFileMode"`
	}{
		LanguageMappings: cfg.LanguageMappings,
		FrontMatter:      cfg.FrontMatter,
		BinaryMode:       cfg.BinaryMode,
		LargeFileMode:    cfg.LargeFileMode,
	}
	return json.Marshal(cacheRelevantConfig)
}
