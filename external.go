package portablesh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LookPath resolves an executable using dir and the supplied environment.
func LookPath(dir string, env []string, file string) (string, error) {
	if file == "" {
		return "", errors.New("empty executable name")
	}
	environment := envMap(env)
	if strings.ContainsAny(file, `/\`) || filepath.IsAbs(file) {
		candidate := file
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(dir, candidate)
		}
		return executableFile(candidate, environment)
	}
	pathValue := environment["PATH"]
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = dir
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(dir, directory)
		}
		if resolved, err := executableFile(filepath.Join(directory, file), environment); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%s: command not found", file)
}

func executableFile(path string, env map[string]string) (string, error) {
	candidates := []string{path}
	if runtime.GOOS == "windows" && filepath.Ext(path) == "" {
		extensions := env["PATHEXT"]
		if extensions == "" {
			extensions = ".COM;.EXE;.BAT;.CMD"
		}
		for _, extension := range strings.Split(extensions, ";") {
			candidates = append(candidates, path+strings.ToLower(extension), path+strings.ToUpper(extension))
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err == nil {
			candidate = absolute
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%s is not executable", path)
}

func runExternal(ctx context.Context, command Command) error {
	path, err := LookPath(command.Dir, command.Env, command.Args[0])
	if err != nil {
		fmt.Fprintf(command.Stderr, "%s: command not found\n", command.Args[0])
		return ExitStatus(127)
	}
	cmd := exec.CommandContext(ctx, path, command.Args[1:]...)
	cmd.Args = append([]string{command.Args[0]}, command.Args[1:]...)
	cmd.Dir = command.Dir
	cmd.Env = append([]string(nil), command.Env...)
	cmd.Stdin = command.Stdin
	cmd.Stdout = command.Stdout
	cmd.Stderr = command.Stderr
	prepareCommand(cmd)
	err = cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ExitStatus(exitErr.ExitCode())
	}
	fmt.Fprintf(command.Stderr, "%s: %v\n", command.Args[0], err)
	return ExitStatus(126)
}

func envMap(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, entry := range values {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			result[name] = value
			if runtime.GOOS == "windows" {
				result[strings.ToUpper(name)] = value
			}
		}
	}
	return result
}
