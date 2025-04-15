package filesystem

import (
	"io/fs"
	"os"
	"path/filepath" // Import filepath for WalkDir
	"time"
)

// RealFileSystem implements the FileSystem interface using the standard os package.
type RealFileSystem struct{}

// NewRealFileSystem creates a new instance of RealFileSystem.
func NewRealFileSystem() *RealFileSystem {
	return &RealFileSystem{}
}

// ReadFile reads the named file using os.ReadFile.
func (rfs *RealFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

// WriteFile writes data to the named file using os.WriteFile.
func (rfs *RealFileSystem) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

// Stat returns a FileInfo using os.Stat.
func (rfs *RealFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

// MkdirAll creates a directory using os.MkdirAll.
func (rfs *RealFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

// WalkDir walks the file tree using filepath.WalkDir.
func (rfs *RealFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

// Remove removes the named file or directory using os.Remove.
func (rfs *RealFileSystem) Remove(name string) error {
	return os.Remove(name)
}

// Rename renames (moves) a file using os.Rename.
func (rfs *RealFileSystem) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

// Chtimes changes the modification time using os.Chtimes.
func (rfs *RealFileSystem) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return os.Chtimes(name, atime, mtime)
}
