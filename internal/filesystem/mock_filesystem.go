// --- START OF FILE internal/filesystem/mock_filesystem.go ---
package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing" // Keep testify dependency for Assert methods
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockFileSystem implements the FileSystem interface for testing purposes.
// It uses testify/mock to allow setting expectations and asserting calls.
type MockFileSystem struct {
	mock.Mock
	mu               sync.RWMutex
	files            map[string][]byte // file path -> content
	fileInfos        map[string]fs.FileInfo
	readErrorPaths   map[string]error // paths that should error on read
	statErrorPaths   map[string]error // paths that should error on stat
	writeErrorPaths  map[string]error // paths that should error on write
	renameErrorPaths map[string]error // paths that should error on rename
	removeErrorPaths map[string]error // paths that should error on remove
	mkdirErrorPaths  map[string]error // paths that should error on mkdir
	walkError        error            // Global error for WalkDir simulation

	// Tracking calls for assertions (Recommendation B.2)
	writeCalls  map[string]int
	removeCalls map[string]int
	renameCalls map[string]int
	mkdirCalls  map[string]int
}

// NewMockFileSystem creates a new instance of MockFileSystem, ready for use.
func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		files:            make(map[string][]byte),
		fileInfos:        make(map[string]fs.FileInfo),
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

// --- MockFileInfo (Helper, kept internal for now, can be exported if needed) ---
type mockFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (mfi *mockFileInfo) Name() string       { return mfi.name }
func (mfi *mockFileInfo) Size() int64        { return mfi.size }
func (mfi *mockFileInfo) Mode() os.FileMode  { return mfi.mode }
func (mfi *mockFileInfo) ModTime() time.Time { return mfi.modTime }
func (mfi *mockFileInfo) IsDir() bool        { return mfi.isDir }
func (mfi *mockFileInfo) Sys() interface{}   { return nil }

// mockDirEntry to satisfy fs.DirEntry
type mockDirEntry struct {
	fs.FileInfo
}

func (m *mockDirEntry) Type() fs.FileMode {
	return m.Mode().Type()
}

func (m *mockDirEntry) Info() (fs.FileInfo, error) {
	return m.FileInfo, nil
}

// --- Helper methods for setting up the mock state ---

// AddFile adds a file with content and modification time to the mock filesystem.
func (mfs *MockFileSystem) AddFile(path string, content []byte, modTime time.Time) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.files[absPath] = content
	mfs.fileInfos[absPath] = &mockFileInfo{
		name:    filepath.Base(absPath),
		size:    int64(len(content)),
		modTime: modTime,
		mode:    0644,
		isDir:   false,
	}
}

// AddDir adds a directory entry to the mock filesystem.
func (mfs *MockFileSystem) AddDir(path string, modTime time.Time) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	// Check if it already exists to avoid overwriting potentially existing file info mode
	if _, exists := mfs.fileInfos[absPath]; !exists {
		mfs.fileInfos[absPath] = &mockFileInfo{
			name:    filepath.Base(absPath),
			size:    0,
			modTime: modTime,
			mode:    0755 | os.ModeDir,
			isDir:   true,
		}
	}
}

// SetWalkError sets a global error to be returned by the WalkDir simulation.
func (mfs *MockFileSystem) SetWalkError(err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	mfs.walkError = err
}

// --- Helper methods for simulating errors ---

func (mfs *MockFileSystem) SimulateReadError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)
	mfs.readErrorPaths[absPath] = err
}
func (mfs *MockFileSystem) SimulateStatError(path string, err error) {
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
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

// --- Assert helpers (Using testify for convenience) ---

func (mfs *MockFileSystem) AssertWriteCalled(t *testing.T, path string) {
	t.Helper()
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Greater(t, mfs.writeCalls[absPath], 0, "WriteFile was not called for %s", path)
}

func (mfs *MockFileSystem) AssertWriteNotCalled(t *testing.T, path string) {
	t.Helper()
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Equal(t, 0, mfs.writeCalls[absPath], "WriteFile should not have been called for %s", path)
}

func (mfs *MockFileSystem) AssertRemoveCalled(t *testing.T, path string) {
	t.Helper()
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Greater(t, mfs.removeCalls[absPath], 0, "Remove was not called for %s", path)
}

func (mfs *MockFileSystem) AssertRemoveNotCalled(t *testing.T, path string) {
	t.Helper()
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(path)
	assert.Equal(t, 0, mfs.removeCalls[absPath], "Remove should not have been called for %s", path)
}

func (mfs *MockFileSystem) AssertRenameCalled(t *testing.T, oldpath string) {
	t.Helper()
	mfs.mu.RLock()
	defer mfs.mu.RUnlock()
	absPath, _ := filepath.Abs(oldpath)
	assert.Greater(t, mfs.renameCalls[absPath], 0, "Rename was not called for %s", oldpath)
}

// --- Implement FileSystem interface methods ---

// ReadFile simulates reading a file from the mock filesystem.
func (mfs *MockFileSystem) ReadFile(name string) ([]byte, error) {
	// Record the call using testify/mock
	args := mfs.Called(name)

	// Check for simulated errors first
	mfs.mu.RLock()
	absPath, _ := filepath.Abs(name)
	if err, ok := mfs.readErrorPaths[absPath]; ok {
		mfs.mu.RUnlock()
		// Allow mock setup to override simulated error if needed
		mockErr := args.Error(1)
		if mockErr != nil && mockErr != err { // Be careful not to return the same error twice
			return nil, mockErr
		}
		return nil, err
	}

	// Check in-memory files
	content, exists := mfs.files[absPath]
	mfs.mu.RUnlock()

	if !exists {
		// If no in-memory file and no simulated error, return mock setup or default
		mockErr := args.Error(1)
		if mockErr != nil {
			return nil, mockErr
		}
		return nil, os.ErrNotExist
	}

	// Return in-memory content and potential error from mock setup
	return content, args.Error(1)
}

// WriteFile simulates writing a file to the mock filesystem.
func (mfs *MockFileSystem) WriteFile(name string, data []byte, perm fs.FileMode) error {
	// Record the call
	args := mfs.Called(name, data, perm)

	// Use lock for modifying internal state
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(name)

	// Track calls
	mfs.writeCalls[absPath]++

	// Check for simulated errors
	if err, ok := mfs.writeErrorPaths[absPath]; ok {
		// Allow mock setup to override
		mockErr := args.Error(0)
		if mockErr != nil && mockErr != err {
			return mockErr
		}
		return err
	}

	// Perform the simulated write
	mfs.files[absPath] = data
	mfs.fileInfos[absPath] = &mockFileInfo{
		name:    filepath.Base(absPath),
		size:    int64(len(data)),
		modTime: time.Now(), // Simulate update time
		mode:    perm,
		isDir:   false,
	}

	// Return error from mock setup
	return args.Error(0)
}

// Stat simulates getting file info from the mock filesystem.
func (mfs *MockFileSystem) Stat(name string) (fs.FileInfo, error) {
	// Record the call
	args := mfs.Called(name)

	// Check for simulated errors
	mfs.mu.RLock()
	absPath, _ := filepath.Abs(name)
	if err, ok := mfs.statErrorPaths[absPath]; ok {
		mfs.mu.RUnlock()
		mockErr := args.Error(1)
		if mockErr != nil && mockErr != err {
			return nil, mockErr
		}
		return nil, err
	}

	// Check in-memory fileInfos
	info, exists := mfs.fileInfos[absPath]
	mfs.mu.RUnlock()

	if !exists {
		mockErr := args.Error(1)
		if mockErr != nil {
			return nil, mockErr
		}
		return nil, os.ErrNotExist
	}

	// Return in-memory info and potential error from mock setup
	return info, args.Error(1)
}

// MkdirAll simulates creating directories in the mock filesystem.
func (mfs *MockFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	// Record the call
	args := mfs.Called(path, perm)

	// Use lock for modifying internal state
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(path)

	// Track calls
	mfs.mkdirCalls[absPath]++

	// Check for simulated errors
	if err, ok := mfs.mkdirErrorPaths[absPath]; ok {
		mockErr := args.Error(0)
		if mockErr != nil && mockErr != err {
			return mockErr
		}
		return err
	}

	// Simulate creating the directory and necessary parents
	// This is a simplified simulation; a real MkdirAll handles existing files vs dirs.
	if info, exists := mfs.fileInfos[absPath]; exists && !info.IsDir() {
		// If something exists at the path but isn't a directory, return an error
		// Allow mock setup to override
		mockErr := args.Error(0)
		if mockErr != nil {
			return mockErr
		}
		return fmt.Errorf("mkdir %s: file exists but is not a directory", path) // Or syscall.ENOTDIR
	}

	// Add directories from parent down to path if they don't exist
	current := ""
	parts := strings.Split(absPath, string(filepath.Separator))
	isAbs := filepath.IsAbs(absPath)

	for i, part := range parts {
		if part == "" {
			if i == 0 && isAbs { // Handle root "/"
				current = string(filepath.Separator)
			}
			continue
		}
		if current == "" && !isAbs { // Start relative path
			current = part
		} else {
			current = filepath.Join(current, part)
		}

		if _, exists := mfs.fileInfos[current]; !exists {
			mfs.fileInfos[current] = &mockFileInfo{
				name:    part,
				size:    0,
				modTime: time.Now(),
				mode:    perm | os.ModeDir,
				isDir:   true,
			}
		} else if !mfs.fileInfos[current].IsDir() {
			// If a component exists but isn't a dir, MkdirAll should fail here
			mockErr := args.Error(0)
			if mockErr != nil {
				return mockErr
			}
			// Simulating Linux "Not a directory" error
			return fmt.Errorf("mkdir %s: not a directory", current) // Or syscall.ENOTDIR
		}
	}

	// Return error from mock setup
	return args.Error(0)
}

// WalkDir simulates walking the directory structure in the mock filesystem.
// This is a basic simulation suitable for many tests.
func (mfs *MockFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	// Record the call
	args := mfs.Called(root, fn) // Note: Mocking functions passed as args is complex

	mfs.mu.RLock()
	walkErr := mfs.walkError // Check for global simulated walk error
	pathsToWalk := make(map[string]fs.FileInfo)
	for p, fi := range mfs.fileInfos {
		pathsToWalk[p] = fi // Copy under read lock
	}
	mfs.mu.RUnlock()

	if walkErr != nil {
		return walkErr
	}
	// Return error from mock setup if walkErr is nil
	if err := args.Error(0); err != nil {
		return err
	}

	absRoot, _ := filepath.Abs(root)
	rootInfo, rootExists := pathsToWalk[absRoot]
	if !rootExists {
		return fmt.Errorf("root path %s not found in mock file system: %w", root, os.ErrNotExist)
	}

	visited := make(map[string]bool) // Avoid infinite loops with symlinks if simulated

	// Recursive helper function
	var walk func(string, fs.FileInfo) error
	walk = func(currentPath string, info fs.FileInfo) error {
		if visited[currentPath] {
			return nil
		}
		visited[currentPath] = true

		// Call the user-provided function
		// Note: This uses the *actual* function passed in, not a mocked one.
		err := fn(currentPath, &mockDirEntry{info}, nil)
		if err != nil {
			if errors.Is(err, fs.SkipDir) && info.IsDir() {
				return fs.SkipDir // Signal to calling walk to skip dir contents
			}
			return err // Propagate other errors (e.g., context cancellation)
		}

		if !info.IsDir() {
			return nil // Nothing more to do for files
		}

		// Find direct children (simplified approach)
		children := []struct {
			path string
			info fs.FileInfo
		}{}
		for path, fi := range pathsToWalk {
			rel, err := filepath.Rel(currentPath, path)
			if err == nil && rel != "." && !strings.Contains(rel, string(filepath.Separator)) {
				children = append(children, struct {
					path string
					info fs.FileInfo
				}{path, fi})
			}
		}

		// Walk children
		for _, child := range children {
			if err := walk(child.path, child.info); err != nil {
				if errors.Is(err, fs.SkipDir) {
					// Skip processing remaining children of the *current* directory
					// if SkipDir was returned for one of its children.
					break
				}
				return err // Propagate other critical errors
			}
		}
		return nil
	}

	// Start walking from the root
	return walk(absRoot, rootInfo)
}

// Remove simulates removing a file or directory from the mock filesystem.
func (mfs *MockFileSystem) Remove(name string) error {
	// Record the call
	args := mfs.Called(name)

	// Use lock for modifying internal state
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(name)

	// Track calls
	mfs.removeCalls[absPath]++

	// Check for simulated errors
	if err, ok := mfs.removeErrorPaths[absPath]; ok {
		mockErr := args.Error(0)
		if mockErr != nil && mockErr != err {
			return mockErr
		}
		return err
	}

	// Perform the simulated remove
	_, fileExists := mfs.files[absPath]
	_, infoExists := mfs.fileInfos[absPath]

	if !fileExists && !infoExists {
		mockErr := args.Error(0)
		if mockErr != nil {
			return mockErr
		}
		return os.ErrNotExist
	}

	if fileExists {
		delete(mfs.files, absPath)
	}
	if infoExists {
		// If it's a directory, check if it's empty (basic simulation)
		if mfs.fileInfos[absPath].IsDir() {
			for path := range mfs.fileInfos {
				rel, err := filepath.Rel(absPath, path)
				// Check if 'path' is a direct child of 'absPath'
				if err == nil && rel != "." && !strings.Contains(rel, string(filepath.Separator)) {
					// Found a child, simulate "directory not empty" error
					mockErr := args.Error(0)
					if mockErr != nil {
						return mockErr
					}
					// Simulating Linux "directory not empty" error
					return fmt.Errorf("remove %s: directory not empty", name) // Or syscall.ENOTEMPTY
				}
			}
		}
		// If it's a file or an empty directory, remove the info
		delete(mfs.fileInfos, absPath)
	}

	// Return error from mock setup
	return args.Error(0)
}

// Rename simulates renaming (moving) a file or directory.
func (mfs *MockFileSystem) Rename(oldpath, newpath string) error {
	// Record the call
	args := mfs.Called(oldpath, newpath)

	// Use lock for modifying internal state
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absOld, _ := filepath.Abs(oldpath)
	absNew, _ := filepath.Abs(newpath)

	// Track calls
	mfs.renameCalls[absOld]++

	// Check for simulated errors (keyed by oldpath for consistency)
	if err, ok := mfs.renameErrorPaths[absOld]; ok {
		mockErr := args.Error(0)
		if mockErr != nil && mockErr != err {
			return mockErr
		}
		return err
	}

	// Check if old path exists
	content, fileExists := mfs.files[absOld]
	info, infoExists := mfs.fileInfos[absOld]

	if !fileExists && !infoExists {
		mockErr := args.Error(0)
		if mockErr != nil {
			return mockErr
		}
		// Simulate LinkError for rename on non-existent source
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: os.ErrNotExist}
	}

	// Handle overwrite of newpath - simulate removing it first
	// Note: Real os.Rename might have OS-specific restrictions here (e.g., cannot rename file over dir)
	if _, newExists := mfs.files[absNew]; newExists {
		delete(mfs.files, absNew)
	}
	if _, newInfoExists := mfs.fileInfos[absNew]; newInfoExists {
		delete(mfs.fileInfos, absNew)
	}

	// Move the data/info
	if fileExists {
		mfs.files[absNew] = content
		delete(mfs.files, absOld)
	}
	if infoExists {
		// Update name within the info struct for the new path
		if mi, ok := info.(*mockFileInfo); ok {
			mi.name = filepath.Base(absNew) // Update the name field
			mfs.fileInfos[absNew] = mi      // Assign updated info to new path
		} else {
			mfs.fileInfos[absNew] = info // Assign original info if not mockFileInfo type
		}
		delete(mfs.fileInfos, absOld)
	}

	// Return error from mock setup
	return args.Error(0)
}

// Chtimes simulates changing modification times.
func (mfs *MockFileSystem) Chtimes(name string, atime time.Time, mtime time.Time) error {
	// Record the call
	args := mfs.Called(name, atime, mtime)

	// Use lock for modifying internal state
	mfs.mu.Lock()
	defer mfs.mu.Unlock()
	absPath, _ := filepath.Abs(name)

	info, exists := mfs.fileInfos[absPath]
	if !exists {
		mockErr := args.Error(0)
		if mockErr != nil {
			return mockErr
		}
		return os.ErrNotExist
	}

	// Try to update the ModTime in the mock FileInfo
	if mockInfo, ok := info.(*mockFileInfo); ok {
		mockInfo.modTime = mtime
		// mfs.fileInfos[absPath] = mockInfo // No need to reassign map value for pointer
	} else {
		// Handle cases where info might not be *mockFileInfo, though unlikely in tests
		// We can't reliably update ModTime here, maybe return an error?
		fmt.Printf("Warning: Could not update ModTime for non-mockFileInfo at %s\n", name)
	}

	// Return error from mock setup
	return args.Error(0)
}

// --- END OF FILE internal/filesystem/mock_filesystem.go ---
