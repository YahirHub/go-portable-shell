package portablesh

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
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

// OSFileSystem delegates to the host operating system. In addition to the
// FileSystem contract it exposes the optional mutation capabilities used by
// portable utility builtins such as mkdir, rm, cp, mv and touch.
type OSFileSystem struct{}

func (OSFileSystem) Open(name string) (File, error) { return os.Open(name) }
func (OSFileSystem) OpenFile(name string, flag int, mode fs.FileMode) (File, error) {
	return os.OpenFile(name, flag, mode)
}
func (OSFileSystem) ReadFile(name string) ([]byte, error)   { return os.ReadFile(name) }
func (OSFileSystem) Stat(name string) (fs.FileInfo, error)  { return os.Stat(name) }
func (OSFileSystem) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (OSFileSystem) Glob(pattern string) ([]string, error)  { return filepath.Glob(pattern) }
func (OSFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(name)
}
func (OSFileSystem) Mkdir(name string, perm fs.FileMode) error { return os.Mkdir(name, perm) }
func (OSFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}
func (OSFileSystem) Remove(name string) error             { return os.Remove(name) }
func (OSFileSystem) RemoveAll(path string) error          { return os.RemoveAll(path) }
func (OSFileSystem) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
func (OSFileSystem) Chtimes(name string, atime, mtime time.Time) error {
	return os.Chtimes(name, atime, mtime)
}
func (OSFileSystem) Chmod(name string, mode fs.FileMode) error { return os.Chmod(name, mode) }
