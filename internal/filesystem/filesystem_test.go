package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test Setup ---

// Helper to create a temporary directory for real filesystem tests
func createRealTempDir(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", name)
	require.NoError(t, err, "Failed to create temp dir for real FS test")
	return dir
}

// Helper to create a temporary file with content for real filesystem tests
func createRealTempFile(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err, "Failed to create temp file for real FS test")
	return path
}

// --- RealFileSystem Tests ---

// TestRealFileSystem_ReadFile tests reading an existing and non-existing file.
func TestRealFileSystem_ReadFile(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-readfile-")
	defer os.RemoveAll(tempDir)

	content := "hello world"
	filePath := createRealTempFile(t, tempDir, "test.txt", content)

	t.Run("ReadExisting", func(t *testing.T) {
		data, err := rfs.ReadFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, []byte(content), data)
	})

	t.Run("ReadNonExisting", func(t *testing.T) {
		nonExistentPath := filepath.Join(tempDir, "nonexistent.txt")
		_, err := rfs.ReadFile(nonExistentPath)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, os.ErrNotExist), "Expected os.ErrNotExist")
	})
}

// TestRealFileSystem_WriteFile tests writing a new file and overwriting an existing one.
func TestRealFileSystem_WriteFile(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-writefile-")
	defer os.RemoveAll(tempDir)

	newFilePath := filepath.Join(tempDir, "newfile.txt")
	content1 := []byte("initial content")
	content2 := []byte("overwritten content")
	perm := fs.FileMode(0644)

	t.Run("WriteNewFile", func(t *testing.T) {
		err := rfs.WriteFile(newFilePath, content1, perm)
		assert.NoError(t, err)

		// Verify content
		readData, readErr := os.ReadFile(newFilePath)
		assert.NoError(t, readErr)
		assert.Equal(t, content1, readData)

		// Verify permissions (checking at least user write bit, exact mode depends on umask)
		info, statErr := os.Stat(newFilePath)
		assert.NoError(t, statErr)
		assert.NotZero(t, info.Mode()&0200, "User write permission should be set")
	})

	t.Run("OverwriteExistingFile", func(t *testing.T) {
		// Ensure file exists from previous test part
		require.FileExists(t, newFilePath)

		err := rfs.WriteFile(newFilePath, content2, perm)
		assert.NoError(t, err)

		// Verify overwritten content
		readData, readErr := os.ReadFile(newFilePath)
		assert.NoError(t, readErr)
		assert.Equal(t, content2, readData)
	})
}

// TestRealFileSystem_Stat tests getting info for an existing file and a directory.
func TestRealFileSystem_Stat(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-stat-")
	defer os.RemoveAll(tempDir)

	content := "stat me"
	filePath := createRealTempFile(t, tempDir, "stat_file.txt", content)
	dirPath := filepath.Join(tempDir, "subdir")
	err := os.Mkdir(dirPath, 0755)
	require.NoError(t, err)

	t.Run("StatFile", func(t *testing.T) {
		info, err := rfs.Stat(filePath)
		assert.NoError(t, err)
		assert.NotNil(t, info)
		assert.Equal(t, "stat_file.txt", info.Name())
		assert.Equal(t, int64(len(content)), info.Size())
		assert.False(t, info.IsDir())
	})

	t.Run("StatDirectory", func(t *testing.T) {
		info, err := rfs.Stat(dirPath)
		assert.NoError(t, err)
		assert.NotNil(t, info)
		assert.Equal(t, "subdir", info.Name())
		assert.True(t, info.IsDir())
	})

	t.Run("StatNonExisting", func(t *testing.T) {
		nonExistentPath := filepath.Join(tempDir, "ghost.txt")
		_, err := rfs.Stat(nonExistentPath)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, os.ErrNotExist))
	})
}

// TestRealFileSystem_MkdirAll tests creating nested directories.
func TestRealFileSystem_MkdirAll(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-mkdirall-")
	defer os.RemoveAll(tempDir)

	nestedPath := filepath.Join(tempDir, "a", "b", "c")
	perm := fs.FileMode(0755)

	t.Run("CreateNested", func(t *testing.T) {
		err := rfs.MkdirAll(nestedPath, perm)
		assert.NoError(t, err)

		// Verify directory exists
		info, statErr := os.Stat(nestedPath)
		assert.NoError(t, statErr)
		assert.True(t, info.IsDir())
	})

	t.Run("CreateExisting", func(t *testing.T) {
		// Call again, should succeed without error
		err := rfs.MkdirAll(nestedPath, perm)
		assert.NoError(t, err)
	})

	t.Run("CreateWithFileConflict", func(t *testing.T) {
		// Use a path inside the existing nested structure
		filePath := filepath.Join(nestedPath, "file.txt") // Use nestedPath which exists
		err := os.WriteFile(filePath, []byte("test"), 0644)
		require.NoError(t, err)

		// Attempting to create a directory where a file exists
		err = rfs.MkdirAll(filePath, perm)
		assert.Error(t, err) // Expect an error (e.g., "not a directory")
	})
}

// TestRealFileSystem_WalkDir tests basic directory traversal and SkipDir functionality.
func TestRealFileSystem_WalkDir(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-walkdir-")
	defer os.RemoveAll(tempDir)

	// Define subdirectories for clarity
	dir1 := filepath.Join(tempDir, "dir1")
	dir1Sub1 := filepath.Join(dir1, "sub1")
	ignoredDir := filepath.Join(tempDir, ".ignored_dir")
	dirToSkip := filepath.Join(tempDir, "dir_to_skip")

	// Setup structure
	require.NoError(t, os.MkdirAll(dir1Sub1, 0755)) // Creates dir1 and dir1/sub1
	require.NoError(t, os.MkdirAll(ignoredDir, 0755))
	require.NoError(t, os.MkdirAll(dirToSkip, 0755))

	createRealTempFile(t, tempDir, "file1.txt", "content1")
	// --- FIX: Corrected calls to createRealTempFile ---
	createRealTempFile(t, dir1, "file2.txt", "content2")                   // Pass the correct directory path
	createRealTempFile(t, dir1Sub1, "file3.txt", "content3")               // Pass the correct directory path
	createRealTempFile(t, ignoredDir, "ignored.txt", "ignore_content")     // Pass the correct directory path
	createRealTempFile(t, dirToSkip, "file_inside.txt", "skipped content") // Pass the correct directory path

	visited := make(map[string]bool)
	walkErr := rfs.WalkDir(tempDir, func(path string, d fs.DirEntry, err error) error {
		assert.NoError(t, err, "Error during walk at path: %s", path)
		if err != nil {
			return err
		}
		// Normalize path for consistent map keys
		relPath, relErr := filepath.Rel(tempDir, path)
		assert.NoError(t, relErr)
		if relPath == "." {
			return nil // Skip the root itself in the visited map
		}
		visited[relPath] = true // Mark path as visited

		// Skip based on name
		if d.IsDir() && (d.Name() == ".ignored_dir" || d.Name() == "dir_to_skip") {
			return filepath.SkipDir // Test skipping explicit directories
		}

		return nil
	})

	assert.NoError(t, walkErr)

	// Verify expected paths were visited (excluding skipped ones)
	expectedPaths := []string{
		"file1.txt",
		"dir1",
		filepath.Join("dir1", "file2.txt"),
		filepath.Join("dir1", "sub1"),
		filepath.Join("dir1", "sub1", "file3.txt"),
		"dir_to_skip", // The skipped directory itself is visited
	}
	unexpectedPaths := []string{
		".ignored_dir",
		filepath.Join(".ignored_dir", "ignored.txt"),
		filepath.Join("dir_to_skip", "file_inside.txt"), // File inside explicitly skipped dir
	}

	assert.Equal(t, len(expectedPaths), len(visited), "Incorrect number of paths visited")
	for _, p := range expectedPaths {
		assert.True(t, visited[p], "Expected path not visited: %s", p)
	}
	for _, p := range unexpectedPaths {
		assert.False(t, visited[p], "Unexpected path visited: %s", p)
	}
}

// TestRealFileSystem_Remove tests removing files and directories.
func TestRealFileSystem_Remove(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-remove-")
	defer os.RemoveAll(tempDir)

	filePath := createRealTempFile(t, tempDir, "remove_me.txt", "delete")
	dirPath := filepath.Join(tempDir, "remove_dir")
	err := os.Mkdir(dirPath, 0755)
	require.NoError(t, err)

	t.Run("RemoveFile", func(t *testing.T) {
		err := rfs.Remove(filePath)
		assert.NoError(t, err)
		_, statErr := os.Stat(filePath)
		assert.ErrorIs(t, statErr, os.ErrNotExist, "File should not exist after Remove")
	})

	t.Run("RemoveEmptyDirectory", func(t *testing.T) {
		err := rfs.Remove(dirPath)
		assert.NoError(t, err)
		_, statErr := os.Stat(dirPath)
		assert.ErrorIs(t, statErr, os.ErrNotExist, "Directory should not exist after Remove")
	})

	t.Run("RemoveNonEmptyDirectory", func(t *testing.T) {
		dirNonEmpty := filepath.Join(tempDir, "non_empty")
		err := os.Mkdir(dirNonEmpty, 0755)
		require.NoError(t, err)
		createRealTempFile(t, dirNonEmpty, "child.txt", "child")

		err = rfs.Remove(dirNonEmpty)
		assert.Error(t, err, "Removing non-empty directory should fail with os.Remove")
		// Error type might differ by OS, check if it exists
		_, statErr := os.Stat(dirNonEmpty)
		assert.NoError(t, statErr, "Directory should still exist")
	})

	t.Run("RemoveNonExisting", func(t *testing.T) {
		nonExistentPath := filepath.Join(tempDir, "ghost.dat")
		err := rfs.Remove(nonExistentPath)
		assert.ErrorIs(t, err, os.ErrNotExist, "Removing non-existent should return ErrNotExist")
	})
}

// TestRealFileSystem_Rename tests renaming files and directories.
func TestRealFileSystem_Rename(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-rename-")
	defer os.RemoveAll(tempDir)

	oldFilePath := createRealTempFile(t, tempDir, "old_name.txt", "rename content")
	newFilePath := filepath.Join(tempDir, "new_name.txt")

	oldDirPath := filepath.Join(tempDir, "old_dir")
	err := os.Mkdir(oldDirPath, 0755)
	require.NoError(t, err)
	newDirPath := filepath.Join(tempDir, "new_dir")

	t.Run("RenameFile", func(t *testing.T) {
		err := rfs.Rename(oldFilePath, newFilePath)
		assert.NoError(t, err)
		// Verify old path doesn't exist
		_, statErrOld := os.Stat(oldFilePath)
		assert.ErrorIs(t, statErrOld, os.ErrNotExist)
		// Verify new path exists
		infoNew, statErrNew := os.Stat(newFilePath)
		assert.NoError(t, statErrNew)
		assert.Equal(t, "new_name.txt", infoNew.Name())
		// Verify content
		data, readErr := os.ReadFile(newFilePath)
		assert.NoError(t, readErr)
		assert.Equal(t, []byte("rename content"), data)
	})

	t.Run("RenameDirectory", func(t *testing.T) {
		err := rfs.Rename(oldDirPath, newDirPath)
		assert.NoError(t, err)
		// Verify old path doesn't exist
		_, statErrOld := os.Stat(oldDirPath)
		assert.ErrorIs(t, statErrOld, os.ErrNotExist)
		// Verify new path exists
		infoNew, statErrNew := os.Stat(newDirPath)
		assert.NoError(t, statErrNew)
		assert.True(t, infoNew.IsDir())
		assert.Equal(t, "new_dir", infoNew.Name())
	})

	t.Run("RenameNonExisting", func(t *testing.T) {
		err := rfs.Rename(filepath.Join(tempDir, "nonexistent_old"), filepath.Join(tempDir, "nonexistent_new"))
		assert.Error(t, err)
		// Check if it's a LinkError containing ErrNotExist (common behavior)
		linkErr := &os.LinkError{}
		if errors.As(err, &linkErr) {
			assert.ErrorIs(t, linkErr.Err, os.ErrNotExist)
		}
	})

	t.Run("RenameToFileOverExistingFile", func(t *testing.T) {
		srcPath := createRealTempFile(t, tempDir, "source.txt", "source")
		dstPath := createRealTempFile(t, tempDir, "destination.txt", "destination_old")

		err := rfs.Rename(srcPath, dstPath)
		assert.NoError(t, err, "Renaming over an existing file should succeed on most OS")

		// Verify old source path doesn't exist
		_, statErrOld := os.Stat(srcPath)
		assert.ErrorIs(t, statErrOld, os.ErrNotExist)

		// Verify destination path exists and has new content
		infoNew, statErrNew := os.Stat(dstPath)
		assert.NoError(t, statErrNew)
		assert.Equal(t, "destination.txt", infoNew.Name())
		data, readErr := os.ReadFile(dstPath)
		assert.NoError(t, readErr)
		assert.Equal(t, []byte("source"), data)
	})
}

// TestRealFileSystem_Chtimes tests changing file modification times.
// Note: Access time checking is unreliable and omitted. Platform-specific tests may be needed
// for more rigorous verification if precise access time handling is critical.
func TestRealFileSystem_Chtimes(t *testing.T) {
	rfs := NewRealFileSystem()
	tempDir := createRealTempDir(t, "test-chtimes-")
	defer os.RemoveAll(tempDir)

	filePath := createRealTempFile(t, tempDir, "times.txt", "content")
	initialInfo, err := os.Stat(filePath)
	require.NoError(t, err)

	// Choose times significantly different from now and each other
	newATime := time.Now().Add(-24 * time.Hour)
	newMTime := time.Now().Add(-48 * time.Hour)

	err = rfs.Chtimes(filePath, newATime, newMTime)
	assert.NoError(t, err)

	// Stat again and check times
	// Use tolerance because filesystem time resolution varies
	tolerance := time.Second * 2 // Increase tolerance slightly for cross-platform robustness

	updatedInfo, err := os.Stat(filePath)
	assert.NoError(t, err)
	assert.NotEqual(t, initialInfo.ModTime(), updatedInfo.ModTime(), "Modification time should have changed")
	assert.WithinDuration(t, newMTime, updatedInfo.ModTime(), tolerance, "Modification time mismatch")

	// Access time checks are often unreliable across different OS and mount options, hence omitted.

	t.Run("ChtimesNonExisting", func(t *testing.T) {
		nonExistentPath := filepath.Join(tempDir, "ghost_times.txt")
		err := rfs.Chtimes(nonExistentPath, time.Now(), time.Now())
		assert.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

// Note: More platform-specific testing might be needed for edge cases related
// to file permissions, symbolic links, or special file types if those are
// expected to be handled robustly by `convi` beyond standard file operations.
// The current tests focus on the core functionality provided by the RealFileSystem wrapper.
