package portablesh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func withinRoot(root, candidate string) bool {
	if root == "" {
		return true
	}
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (r *Runner) resolvePath(state *shellState, value string, forCreate bool) (string, error) {
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(state.dir, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if r.cfg.RootDir == "" {
		return absolute, nil
	}
	checked := absolute
	if forCreate {
		parent := filepath.Dir(absolute)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			checked = filepath.Join(resolved, filepath.Base(absolute))
		}
	} else if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		checked = resolved
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if !withinRoot(r.cfg.RootDir, checked) {
		return "", fmt.Errorf("path %s is outside configured root %s", absolute, r.cfg.RootDir)
	}
	return absolute, nil
}
