package portablesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var portableBuiltins map[string]builtinFunc

func init() {
	portableBuiltins = map[string]builtinFunc{
		"ls":       builtinLS,
		"mkdir":    builtinMkdir,
		"rmdir":    builtinRmdir,
		"rm":       builtinRM,
		"cp":       builtinCP,
		"mv":       builtinMV,
		"touch":    builtinTouch,
		"chmod":    builtinChmod,
		"cat":      builtinCat,
		"head":     builtinHead,
		"tail":     builtinTail,
		"wc":       builtinWC,
		"basename": builtinBasename,
		"dirname":  builtinDirname,
		"which":    builtinWhich,
		"env":      builtinEnv,
		"printenv": builtinPrintenv,
		"sleep":    builtinSleep,
		"uname":    builtinUname,
		"whoami":   builtinWhoami,
		"find":     builtinFind,
		"grep":     builtinGrep,
	}
}

type lsOptions struct {
	all       bool
	almostAll bool
	directory bool
	long      bool
	recursive bool
	human     bool
}

func builtinLS(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	options := lsOptions{}
	paths := make([]string, 0, len(args))
	parseOptions := true
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && strings.HasPrefix(arg, "--") {
			switch arg {
			case "--all":
				options.all = true
			case "--almost-all":
				options.almostAll = true
			case "--directory":
				options.directory = true
			case "--recursive":
				options.recursive = true
			case "--human-readable":
				options.human = true
			default:
				fmt.Fprintf(ioStreams.err, "ls: unsupported option %s\n", arg)
				return normal(2)
			}
			continue
		}
		if parseOptions && len(arg) > 1 && arg[0] == '-' {
			valid := true
			for _, flag := range arg[1:] {
				switch flag {
				case 'a':
					options.all = true
				case 'A':
					options.almostAll = true
				case 'd':
					options.directory = true
				case 'l':
					options.long = true
				case 'R':
					options.recursive = true
				case 'h':
					options.human = true
				case '1':
					// Output is intentionally one entry per line in non-interactive mode.
				default:
					valid = false
				}
			}
			if !valid {
				fmt.Fprintf(ioStreams.err, "ls: unsupported option %s\n", arg)
				return normal(2)
			}
			continue
		}
		parseOptions = false
		paths = append(paths, arg)
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	status := 0
	for index, value := range paths {
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		path, err := runner.resolvePath(state, value, false)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "ls: %s: %v\n", value, err)
			status = 1
			continue
		}
		info, err := runner.cfg.FileSystem.Lstat(path)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "ls: %s: %v\n", value, err)
			status = 1
			continue
		}
		if len(paths) > 1 && index > 0 {
			fmt.Fprintln(ioStreams.out)
		}
		if !info.IsDir() || options.directory {
			if err := writeLSEntry(ioStreams.out, filepath.Base(value), info, options); err != nil {
				return builtinIO(err)
			}
			continue
		}
		if len(paths) > 1 {
			fmt.Fprintf(ioStreams.out, "%s:\n", value)
		}
		if err := listPortableDirectory(ctx, runner, state, path, value, options, ioStreams.out); err != nil {
			fmt.Fprintf(ioStreams.err, "ls: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func listPortableDirectory(ctx context.Context, runner *Runner, state *shellState, path, display string, options lsOptions, out io.Writer) error {
	entries, err := readPortableDir(runner.cfg.FileSystem, path)
	if err != nil {
		return err
	}
	if options.all {
		info, statErr := runner.cfg.FileSystem.Stat(path)
		if statErr == nil {
			if err := writeLSEntry(out, ".", info, options); err != nil {
				return err
			}
		}
		parent := filepath.Dir(path)
		if info, statErr := runner.cfg.FileSystem.Stat(parent); statErr == nil {
			if err := writeLSEntry(out, "..", info, options); err != nil {
				return err
			}
		}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.name, ".") && !options.all && !options.almostAll {
			continue
		}
		if err := writeLSEntry(out, entry.name, entry.info, options); err != nil {
			return err
		}
	}
	if !options.recursive {
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.info.IsDir() || entry.name == "." || entry.name == ".." {
			continue
		}
		if strings.HasPrefix(entry.name, ".") && !options.all && !options.almostAll {
			continue
		}
		childPath := filepath.Join(path, entry.name)
		childDisplay := filepath.Join(display, entry.name)
		fmt.Fprintf(out, "\n%s:\n", childDisplay)
		if err := listPortableDirectory(ctx, runner, state, childPath, childDisplay, options, out); err != nil {
			return err
		}
	}
	return nil
}

func writeLSEntry(out io.Writer, name string, info fs.FileInfo, options lsOptions) error {
	if !options.long {
		_, err := fmt.Fprintln(out, name)
		return err
	}
	size := strconv.FormatInt(info.Size(), 10)
	if options.human {
		size = humanSize(info.Size())
	}
	_, err := fmt.Fprintf(out, "%s %8s %s %s\n", info.Mode().String(), size, info.ModTime().UTC().Format("2006-01-02 15:04"), name)
	return err
}

func humanSize(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%dB", value)
	}
	units := []string{"K", "M", "G", "T", "P"}
	amount := float64(value)
	unit := "B"
	for _, candidate := range units {
		amount /= 1024
		unit = candidate
		if amount < 1024 {
			break
		}
	}
	if amount >= 10 {
		return fmt.Sprintf("%.0f%s", amount, unit)
	}
	return fmt.Sprintf("%.1f%s", amount, unit)
}

func builtinMkdir(_ context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	parents := false
	mode := portableFileMode(0o777, state.umask)
	var paths []string
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			paths = append(paths, args[1:]...)
			break
		}
		if arg == "-p" || arg == "--parents" {
			parents = true
			args = args[1:]
			continue
		}
		if arg == "-m" || arg == "--mode" {
			if len(args) < 2 {
				fmt.Fprintln(ioStreams.err, "mkdir: option requires a mode")
				return normal(2)
			}
			parsed, err := parseFileMode(args[1])
			if err != nil {
				fmt.Fprintf(ioStreams.err, "mkdir: invalid mode %q\n", args[1])
				return normal(2)
			}
			mode = parsed
			args = args[2:]
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(ioStreams.err, "mkdir: unsupported option %s\n", arg)
			return normal(2)
		}
		paths = append(paths, args...)
		break
	}
	if len(paths) == 0 {
		fmt.Fprintln(ioStreams.err, "mkdir: missing operand")
		return normal(2)
	}
	status := 0
	for _, value := range paths {
		path, err := runner.resolvePath(state, value, true)
		if err == nil {
			if parents {
				err = mkdirAllPortable(runner.cfg.FileSystem, path, mode)
			} else {
				err = mkdirPortable(runner.cfg.FileSystem, path, mode)
			}
		}
		if err != nil {
			fmt.Fprintf(ioStreams.err, "mkdir: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func parseFileMode(value string) (fs.FileMode, error) {
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, "0o"), 8, 32)
	if err != nil || parsed > 0o7777 {
		return 0, errors.New("invalid mode")
	}
	return fs.FileMode(parsed), nil
}

func builtinRmdir(_ context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	parents := false
	parseOptions := true
	var paths []string
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && (arg == "-p" || arg == "--parents") {
			parents = true
			continue
		}
		if parseOptions && strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(ioStreams.err, "rmdir: unsupported option %s\n", arg)
			return normal(2)
		}
		parseOptions = false
		paths = append(paths, arg)
	}
	if len(paths) == 0 {
		fmt.Fprintln(ioStreams.err, "rmdir: missing operand")
		return normal(2)
	}
	status := 0
	for _, value := range paths {
		path, err := runner.resolvePath(state, value, false)
		if err == nil {
			err = removePortable(runner.cfg.FileSystem, path)
		}
		if err != nil {
			fmt.Fprintf(ioStreams.err, "rmdir: %s: %v\n", value, err)
			status = 1
			continue
		}
		if parents {
			parent := filepath.Dir(path)
			for parent != path && parent != filepath.Dir(parent) {
				if runner.cfg.RootDir != "" && !withinRoot(runner.cfg.RootDir, parent) {
					break
				}
				if err := removePortable(runner.cfg.FileSystem, parent); err != nil {
					break
				}
				path, parent = parent, filepath.Dir(parent)
			}
		}
	}
	return normal(status)
}

func builtinRM(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	force, recursive := false, false
	var paths []string
	parseOptions := true
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && len(arg) > 1 && arg[0] == '-' {
			valid := true
			for _, flag := range arg[1:] {
				switch flag {
				case 'f':
					force = true
				case 'r', 'R':
					recursive = true
				default:
					valid = false
				}
			}
			if !valid {
				fmt.Fprintf(ioStreams.err, "rm: unsupported option %s\n", arg)
				return normal(2)
			}
			continue
		}
		parseOptions = false
		paths = append(paths, arg)
	}
	if len(paths) == 0 {
		if force {
			return normal(0)
		}
		fmt.Fprintln(ioStreams.err, "rm: missing operand")
		return normal(2)
	}
	status := 0
	for _, value := range paths {
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		path, err := runner.resolvePath(state, value, false)
		if err != nil {
			if force && os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(ioStreams.err, "rm: %s: %v\n", value, err)
			status = 1
			continue
		}
		info, statErr := runner.cfg.FileSystem.Lstat(path)
		if statErr != nil {
			if force && os.IsNotExist(statErr) {
				continue
			}
			fmt.Fprintf(ioStreams.err, "rm: %s: %v\n", value, statErr)
			status = 1
			continue
		}
		if info.IsDir() && !recursive {
			fmt.Fprintf(ioStreams.err, "rm: %s: is a directory\n", value)
			status = 1
			continue
		}
		if recursive {
			err = removeAllPortable(runner.cfg.FileSystem, path)
		} else {
			err = removePortable(runner.cfg.FileSystem, path)
		}
		if err != nil && !(force && os.IsNotExist(err)) {
			fmt.Fprintf(ioStreams.err, "rm: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

type copyOptions struct {
	recursive bool
	noClobber bool
}

func builtinCP(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	options := copyOptions{}
	var operands []string
	parseOptions := true
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && len(arg) > 1 && arg[0] == '-' {
			valid := true
			for _, flag := range arg[1:] {
				switch flag {
				case 'r', 'R':
					options.recursive = true
				case 'n':
					options.noClobber = true
				case 'f':
					// Overwrite is already the default.
				default:
					valid = false
				}
			}
			if !valid {
				fmt.Fprintf(ioStreams.err, "cp: unsupported option %s\n", arg)
				return normal(2)
			}
			continue
		}
		parseOptions = false
		operands = append(operands, arg)
	}
	if len(operands) < 2 {
		fmt.Fprintln(ioStreams.err, "cp: missing source or destination")
		return normal(2)
	}
	destinationArg := operands[len(operands)-1]
	destination, err := runner.resolvePath(state, destinationArg, true)
	if err != nil {
		fmt.Fprintf(ioStreams.err, "cp: %s: %v\n", destinationArg, err)
		return normal(1)
	}
	destinationInfo, destinationErr := runner.cfg.FileSystem.Stat(destination)
	multiple := len(operands) > 2
	if multiple && (destinationErr != nil || !destinationInfo.IsDir()) {
		fmt.Fprintf(ioStreams.err, "cp: target %s is not a directory\n", destinationArg)
		return normal(1)
	}
	status := 0
	for _, sourceArg := range operands[:len(operands)-1] {
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		source, err := runner.resolvePath(state, sourceArg, false)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "cp: %s: %v\n", sourceArg, err)
			status = 1
			continue
		}
		target := destination
		if destinationErr == nil && destinationInfo.IsDir() {
			target = filepath.Join(destination, filepath.Base(source))
		}
		if err := copyPortablePath(ctx, runner, state, source, target, options); err != nil {
			fmt.Fprintf(ioStreams.err, "cp: %s: %v\n", sourceArg, err)
			status = 1
		}
	}
	return normal(status)
}

func copyPortablePath(ctx context.Context, runner *Runner, state *shellState, source, target string, options copyOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if filepath.Clean(source) == filepath.Clean(target) {
		return fmt.Errorf("source and destination are the same path")
	}
	info, err := runner.cfg.FileSystem.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if !options.recursive {
			return fmt.Errorf("omitting directory %s; use -r", source)
		}
		relative, relErr := filepath.Rel(source, target)
		if relErr == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("cannot copy a directory into itself")
		}
		if options.noClobber {
			if _, err := runner.cfg.FileSystem.Stat(target); err == nil {
				return nil
			}
		}
		if err := mkdirAllPortable(runner.cfg.FileSystem, target, portableFileMode(info.Mode().Perm(), state.umask)); err != nil {
			return err
		}
		entries, err := readPortableDir(runner.cfg.FileSystem, source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPortablePath(ctx, runner, state, filepath.Join(source, entry.name), filepath.Join(target, entry.name), options); err != nil {
				return err
			}
		}
		return nil
	}
	if options.noClobber {
		if _, err := runner.cfg.FileSystem.Stat(target); err == nil {
			return nil
		}
	}
	parent := filepath.Dir(target)
	if err := mkdirAllPortable(runner.cfg.FileSystem, parent, portableFileMode(0o777, state.umask)); err != nil {
		return err
	}
	releaseSource, err := runner.acquireOpenFile(state)
	if err != nil {
		return err
	}
	sourceFile, err := runner.cfg.FileSystem.Open(source)
	if err != nil {
		releaseSource()
		return err
	}
	defer func() {
		_ = sourceFile.Close()
		releaseSource()
	}()
	releaseTarget, err := runner.acquireOpenFile(state)
	if err != nil {
		return err
	}
	targetFile, err := runner.cfg.FileSystem.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, portableFileMode(info.Mode().Perm(), state.umask))
	if err != nil {
		releaseTarget()
		return err
	}
	defer func() {
		_ = targetFile.Close()
		releaseTarget()
	}()
	return copyContext(ctx, targetFile, sourceFile)
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if _, err := destination.Write(buffer[:count]); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func builtinMV(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	noClobber := false
	var operands []string
	parseOptions := true
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && len(arg) > 1 && arg[0] == '-' {
			valid := true
			for _, flag := range arg[1:] {
				switch flag {
				case 'n':
					noClobber = true
				case 'f':
					// Overwrite is the default.
				default:
					valid = false
				}
			}
			if !valid {
				fmt.Fprintf(ioStreams.err, "mv: unsupported option %s\n", arg)
				return normal(2)
			}
			continue
		}
		parseOptions = false
		operands = append(operands, arg)
	}
	if len(operands) < 2 {
		fmt.Fprintln(ioStreams.err, "mv: missing source or destination")
		return normal(2)
	}
	destinationArg := operands[len(operands)-1]
	destination, err := runner.resolvePath(state, destinationArg, true)
	if err != nil {
		fmt.Fprintf(ioStreams.err, "mv: %s: %v\n", destinationArg, err)
		return normal(1)
	}
	destinationInfo, destinationErr := runner.cfg.FileSystem.Stat(destination)
	multiple := len(operands) > 2
	if multiple && (destinationErr != nil || !destinationInfo.IsDir()) {
		fmt.Fprintf(ioStreams.err, "mv: target %s is not a directory\n", destinationArg)
		return normal(1)
	}
	status := 0
	for _, sourceArg := range operands[:len(operands)-1] {
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		source, err := runner.resolvePath(state, sourceArg, false)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "mv: %s: %v\n", sourceArg, err)
			status = 1
			continue
		}
		target := destination
		if destinationErr == nil && destinationInfo.IsDir() {
			target = filepath.Join(destination, filepath.Base(source))
		}
		if filepath.Clean(source) == filepath.Clean(target) {
			continue
		}
		if info, statErr := runner.cfg.FileSystem.Stat(source); statErr == nil && info.IsDir() {
			relative, relErr := filepath.Rel(source, target)
			if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				fmt.Fprintf(ioStreams.err, "mv: %s: cannot move a directory into itself\n", sourceArg)
				status = 1
				continue
			}
		}
		if noClobber {
			if _, err := runner.cfg.FileSystem.Stat(target); err == nil {
				continue
			}
		}
		if _, err := runner.cfg.FileSystem.Lstat(target); err == nil {
			info, _ := runner.cfg.FileSystem.Lstat(target)
			if info != nil && info.IsDir() {
				err = removeAllPortable(runner.cfg.FileSystem, target)
			} else {
				err = removePortable(runner.cfg.FileSystem, target)
			}
			if err != nil {
				fmt.Fprintf(ioStreams.err, "mv: %s: %v\n", sourceArg, err)
				status = 1
				continue
			}
		}
		if err := renamePortable(runner.cfg.FileSystem, source, target); err == nil {
			continue
		}
		if err := copyPortablePath(ctx, runner, state, source, target, copyOptions{recursive: true}); err != nil {
			fmt.Fprintf(ioStreams.err, "mv: %s: %v\n", sourceArg, err)
			status = 1
			continue
		}
		info, err := runner.cfg.FileSystem.Lstat(source)
		if err == nil && info.IsDir() {
			err = removeAllPortable(runner.cfg.FileSystem, source)
		} else if err == nil {
			err = removePortable(runner.cfg.FileSystem, source)
		}
		if err != nil {
			fmt.Fprintf(ioStreams.err, "mv: %s: copied but could not remove source: %v\n", sourceArg, err)
			status = 1
		}
	}
	return normal(status)
}

func builtinTouch(_ context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	noCreate := false
	var paths []string
	parseOptions := true
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && arg == "-c" {
			noCreate = true
			continue
		}
		if parseOptions && strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(ioStreams.err, "touch: unsupported option %s\n", arg)
			return normal(2)
		}
		parseOptions = false
		paths = append(paths, arg)
	}
	if len(paths) == 0 {
		fmt.Fprintln(ioStreams.err, "touch: missing operand")
		return normal(2)
	}
	now := time.Now()
	status := 0
	for _, value := range paths {
		path, err := runner.resolvePath(state, value, true)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "touch: %s: %v\n", value, err)
			status = 1
			continue
		}
		if _, err := runner.cfg.FileSystem.Stat(path); err == nil {
			if err := chtimesPortable(runner.cfg.FileSystem, path, now, now); err != nil {
				fmt.Fprintf(ioStreams.err, "touch: %s: %v\n", value, err)
				status = 1
			}
			continue
		} else if noCreate && os.IsNotExist(err) {
			continue
		}
		release, err := runner.acquireOpenFile(state)
		if err != nil {
			return failure(err)
		}
		file, err := runner.cfg.FileSystem.OpenFile(path, os.O_CREATE|os.O_WRONLY, portableFileMode(0o666, state.umask))
		if err == nil {
			err = file.Close()
		}
		release()
		if err != nil {
			fmt.Fprintf(ioStreams.err, "touch: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func builtinChmod(_ context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) < 2 {
		fmt.Fprintln(ioStreams.err, "chmod: expected MODE FILE...")
		return normal(2)
	}
	modeSpec := args[0]
	status := 0
	for _, value := range args[1:] {
		path, err := runner.resolvePath(state, value, false)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "chmod: %s: %v\n", value, err)
			status = 1
			continue
		}
		info, err := runner.cfg.FileSystem.Stat(path)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "chmod: %s: %v\n", value, err)
			status = 1
			continue
		}
		mode, err := applyPortableMode(info.Mode().Perm(), modeSpec)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "chmod: invalid mode %q\n", modeSpec)
			return normal(2)
		}
		if err := chmodPortable(runner.cfg.FileSystem, path, mode); err != nil {
			fmt.Fprintf(ioStreams.err, "chmod: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func applyPortableMode(current fs.FileMode, spec string) (fs.FileMode, error) {
	if parsed, err := parseFileMode(spec); err == nil {
		return parsed.Perm(), nil
	}
	parts := strings.Split(spec, ",")
	mode := current.Perm()
	for _, part := range parts {
		if part == "" {
			return 0, errors.New("empty symbolic mode")
		}
		operatorIndex := strings.IndexAny(part, "+-=")
		if operatorIndex < 0 {
			return 0, errors.New("missing symbolic operator")
		}
		who := part[:operatorIndex]
		if who == "" {
			who = "a"
		}
		operator := part[operatorIndex]
		permissions := part[operatorIndex+1:]
		mask, clearMask, err := symbolicModeMasks(who, permissions)
		if err != nil {
			return 0, err
		}
		switch operator {
		case '+':
			mode |= mask
		case '-':
			mode &^= mask
		case '=':
			mode &^= clearMask
			mode |= mask
		}
	}
	return mode.Perm(), nil
}

func symbolicModeMasks(who, permissions string) (fs.FileMode, fs.FileMode, error) {
	classes := map[rune]bool{}
	for _, class := range who {
		if !strings.ContainsRune("ugoa", class) {
			return 0, 0, errors.New("invalid class")
		}
		if class == 'a' {
			classes['u'], classes['g'], classes['o'] = true, true, true
		} else {
			classes[class] = true
		}
	}
	var mask, clear fs.FileMode
	for class := range classes {
		switch class {
		case 'u':
			clear |= 0o700
		case 'g':
			clear |= 0o070
		case 'o':
			clear |= 0o007
		}
	}
	for _, permission := range permissions {
		if !strings.ContainsRune("rwx", permission) {
			return 0, 0, errors.New("invalid permission")
		}
		for class := range classes {
			switch class {
			case 'u':
				switch permission {
				case 'r':
					mask |= 0o400
				case 'w':
					mask |= 0o200
				case 'x':
					mask |= 0o100
				}
			case 'g':
				switch permission {
				case 'r':
					mask |= 0o040
				case 'w':
					mask |= 0o020
				case 'x':
					mask |= 0o010
				}
			case 'o':
				switch permission {
				case 'r':
					mask |= 0o004
				case 'w':
					mask |= 0o002
				case 'x':
					mask |= 0o001
				}
			}
		}
	}
	return mask, clear, nil
}

func builtinCat(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	number := false
	var files []string
	parseOptions := true
	for _, arg := range args {
		if parseOptions && arg == "--" {
			parseOptions = false
			continue
		}
		if parseOptions && arg == "-n" {
			number = true
			continue
		}
		if parseOptions && strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(ioStreams.err, "cat: unsupported option %s\n", arg)
			return normal(2)
		}
		parseOptions = false
		files = append(files, arg)
	}
	if len(files) == 0 {
		files = []string{"-"}
	}
	status := 0
	line := 1
	for _, value := range files {
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		reader := ioStreams.in
		var closeFn func()
		if value != "-" {
			path, err := runner.resolvePath(state, value, false)
			if err != nil {
				fmt.Fprintf(ioStreams.err, "cat: %s: %v\n", value, err)
				status = 1
				continue
			}
			release, err := runner.acquireOpenFile(state)
			if err != nil {
				return failure(err)
			}
			file, err := runner.cfg.FileSystem.Open(path)
			if err != nil {
				release()
				fmt.Fprintf(ioStreams.err, "cat: %s: %v\n", value, err)
				status = 1
				continue
			}
			reader = file
			closeFn = func() { _ = file.Close(); release() }
		}
		var err error
		if number {
			line, err = copyNumberedLines(ctx, ioStreams.out, reader, line)
		} else {
			err = copyContext(ctx, ioStreams.out, reader)
		}
		if closeFn != nil {
			closeFn()
		}
		if err != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			fmt.Fprintf(ioStreams.err, "cat: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func copyNumberedLines(ctx context.Context, out io.Writer, in io.Reader, start int) (int, error) {
	line := start
	buffer := make([]byte, 0, 4096)
	chunk := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return line, err
		}
		count, readErr := in.Read(chunk)
		for _, b := range chunk[:count] {
			buffer = append(buffer, b)
			if b == '\n' {
				if _, err := fmt.Fprintf(out, "%6d\t%s", line, string(buffer)); err != nil {
					return line, err
				}
				line++
				buffer = buffer[:0]
			}
		}
		if readErr != nil {
			if len(buffer) > 0 {
				if _, err := fmt.Fprintf(out, "%6d\t%s", line, string(buffer)); err != nil {
					return line, err
				}
				line++
			}
			if errors.Is(readErr, io.EOF) {
				return line, nil
			}
			return line, readErr
		}
	}
}
