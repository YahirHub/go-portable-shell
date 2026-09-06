package portablesh

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type readDirFileSystem interface {
	ReadDir(string) ([]fs.DirEntry, error)
}

type mkdirFileSystem interface {
	Mkdir(string, fs.FileMode) error
}

type mkdirAllFileSystem interface {
	MkdirAll(string, fs.FileMode) error
}

type removeFileSystem interface {
	Remove(string) error
}

type removeAllFileSystem interface {
	RemoveAll(string) error
}

type renameFileSystem interface {
	Rename(string, string) error
}

type chtimesFileSystem interface {
	Chtimes(string, time.Time, time.Time) error
}

type chmodFileSystem interface {
	Chmod(string, fs.FileMode) error
}

type portableDirEntry struct {
	name string
	info fs.FileInfo
}

func readPortableDir(fileSystem FileSystem, name string) ([]portableDirEntry, error) {
	if capable, ok := fileSystem.(readDirFileSystem); ok {
		entries, err := capable.ReadDir(name)
		if err != nil {
			return nil, err
		}
		result := make([]portableDirEntry, 0, len(entries))
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			result = append(result, portableDirEntry{name: entry.Name(), info: info})
		}
		sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
		return result, nil
	}

	matches, err := fileSystem.Glob(filepath.Join(name, "*"))
	if err != nil {
		return nil, err
	}
	result := make([]portableDirEntry, 0, len(matches))
	for _, match := range matches {
		info, err := fileSystem.Lstat(match)
		if err != nil {
			continue
		}
		result = append(result, portableDirEntry{name: filepath.Base(match), info: info})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func mkdirPortable(fileSystem FileSystem, path string, mode fs.FileMode) error {
	capable, ok := fileSystem.(mkdirFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support mkdir")
	}
	return capable.Mkdir(path, mode)
}

func mkdirAllPortable(fileSystem FileSystem, path string, mode fs.FileMode) error {
	capable, ok := fileSystem.(mkdirAllFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support mkdir -p")
	}
	return capable.MkdirAll(path, mode)
}

func removePortable(fileSystem FileSystem, path string) error {
	capable, ok := fileSystem.(removeFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support remove")
	}
	return capable.Remove(path)
}

func removeAllPortable(fileSystem FileSystem, path string) error {
	capable, ok := fileSystem.(removeAllFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support recursive remove")
	}
	return capable.RemoveAll(path)
}

func renamePortable(fileSystem FileSystem, oldpath, newpath string) error {
	capable, ok := fileSystem.(renameFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support rename")
	}
	return capable.Rename(oldpath, newpath)
}

func chtimesPortable(fileSystem FileSystem, path string, atime, mtime time.Time) error {
	capable, ok := fileSystem.(chtimesFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support changing timestamps")
	}
	return capable.Chtimes(path, atime, mtime)
}

func chmodPortable(fileSystem FileSystem, path string, mode fs.FileMode) error {
	capable, ok := fileSystem.(chmodFileSystem)
	if !ok {
		return fmt.Errorf("configured filesystem does not support chmod")
	}
	return capable.Chmod(path, mode)
}

func portableFileMode(mode fs.FileMode, umask os.FileMode) fs.FileMode {
	return mode &^ fs.FileMode(umask.Perm())
}
