package filesystem

import (
	"io/fs" // Import io/fs for FileMode and FileInfo
	"time"  // Import time for ModTime
)

// FileSystem defines an interface for interacting with the filesystem.
// This allows for decoupling core logic from the OS package, facilitating testing.
// Conforms to specifications required by TASK-CONVI-020.
type FileSystem interface {
	// ReadFile reads the named file and returns the contents.
	ReadFile(name string) ([]byte, error)

	// WriteFile writes data to the named file, creating it if necessary.
	// If the file does not exist, WriteFile creates it with permissions perm;
	// otherwise WriteFile truncates it before writing, without changing permissions.
	WriteFile(name string, data []byte, perm fs.FileMode) error

	// Stat returns a FileInfo describing the named file.
	Stat(name string) (fs.FileInfo, error)

	// MkdirAll creates a directory named path,
	// along with any necessary parents, and returns nil,
	// or else returns an error.
	// The permission bits perm (before umask) are used for all
	// directories that MkdirAll creates.
	MkdirAll(path string, perm fs.FileMode) error

	// WalkDir walks the file tree rooted at root, calling fn for each file or
	// directory in the tree, including root.
	// All errors that arise visiting files or directories are filtered by fn:
	// see the WalkDirFunc documentation for details.
	WalkDir(root string, fn fs.WalkDirFunc) error

	// Remove removes the named file or (empty) directory.
	Remove(name string) error

	// Rename renames (moves) oldpath to newpath.
	// If newpath already exists and is not a directory, Rename replaces it.
	// OS-specific restrictions may apply when oldpath and newpath are in different directories.
	Rename(oldpath, newpath string) error

	// Chtimes changes the modification time of the named file.
	// Added potentially for cache testing scenarios or specific filesystem interactions.
	Chtimes(name string, atime time.Time, mtime time.Time) error
}
