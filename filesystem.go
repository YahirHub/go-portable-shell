package portablesh

import (
	"io/fs"
	"os"
	"path/filepath"
)

// File is the minimum read/write file contract used by redirections.
type File interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	Stat() (fs.FileInfo, error)
}

// FileSystem abstracts shell-owned reads, metadata and redirections. External
// executables always run against the host filesystem.
type FileSystem interface {
	Open(string) (File, error)
	OpenFile(string, int, fs.FileMode) (File, error)
	ReadFile(string) ([]byte, error)
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	Glob(string) ([]string, error)
}

// OSFileSystem delegates to the host operating system.
type OSFileSystem struct{}

func (OSFileSystem) Open(name string) (File, error) { return os.Open(name) }
func (OSFileSystem) OpenFile(name string, flag int, mode fs.FileMode) (File, error) {
	return os.OpenFile(name, flag, mode)
}
func (OSFileSystem) ReadFile(name string) ([]byte, error)   { return os.ReadFile(name) }
func (OSFileSystem) Stat(name string) (fs.FileInfo, error)  { return os.Stat(name) }
func (OSFileSystem) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (OSFileSystem) Glob(pattern string) ([]string, error)  { return filepath.Glob(pattern) }
