package portablesh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func builtinBasename(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(ioStreams.err, "basename: expected NAME [SUFFIX]")
		return normal(2)
	}
	name := filepath.Base(filepath.Clean(args[0]))
	if len(args) == 2 && args[1] != name && strings.HasSuffix(name, args[1]) {
		name = strings.TrimSuffix(name, args[1])
	}
	_, err := fmt.Fprintln(ioStreams.out, name)
	return builtinIO(err)
}

func builtinDirname(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	if len(args) != 1 {
		fmt.Fprintln(ioStreams.err, "dirname: expected one operand")
		return normal(2)
	}
	_, err := fmt.Fprintln(ioStreams.out, filepath.Dir(filepath.Clean(args[0])))
	return builtinIO(err)
}

func builtinWhich(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	all := false
	if len(args) > 0 && args[0] == "-a" {
		all = true
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(ioStreams.err, "which: missing command name")
		return normal(2)
	}
	status := 0
	for _, name := range args {
		found := false
		if builtins[name] != nil || portableBuiltins[name] != nil {
			fmt.Fprintln(ioStreams.out, name)
			found = true
			if !all {
				continue
			}
		}
		paths := lookAllPaths(state.dir, state.environment(), name)
		for _, path := range paths {
			fmt.Fprintln(ioStreams.out, path)
			found = true
			if !all {
				break
			}
		}
		if !found {
			status = 1
		}
	}
	return normal(status)
}

func lookAllPaths(dir string, environment []string, name string) []string {
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		if path, err := LookPath(dir, environment, name); err == nil {
			return []string{path}
		}
		return nil
	}
	env := envMap(environment)
	seen := make(map[string]bool)
	var result []string
	for _, directory := range filepath.SplitList(env["PATH"]) {
		if directory == "" {
			directory = dir
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(dir, directory)
		}
		if path, err := executableFile(filepath.Join(directory, name), env); err == nil && !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func builtinEnv(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	child := state.clone()
	index := 0
	for index < len(args) {
		arg := args[index]
		if arg == "--" {
			index++
			break
		}
		if arg == "-i" || arg == "--ignore-environment" {
			child.env = make(map[string]string)
			child.exported = make(map[string]bool)
			index++
			continue
		}
		if arg == "-u" || arg == "--unset" {
			if index+1 >= len(args) {
				fmt.Fprintln(ioStreams.err, "env: -u requires a name")
				return normal(2)
			}
			delete(child.env, args[index+1])
			delete(child.exported, args[index+1])
			index += 2
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(ioStreams.err, "env: unsupported option %s\n", arg)
			return normal(2)
		}
		name, value, ok := strings.Cut(arg, "=")
		if ok && validName(name) {
			child.env[name] = value
			child.exported[name] = true
			index++
			continue
		}
		break
	}
	if index >= len(args) {
		for _, entry := range child.environment() {
			fmt.Fprintln(ioStreams.out, entry)
		}
		return normal(0)
	}
	return builtinCommand(ctx, runner, child, args[index:], ioStreams)
}

func builtinPrintenv(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		for _, entry := range state.environment() {
			fmt.Fprintln(ioStreams.out, entry)
		}
		return normal(0)
	}
	status := 0
	for _, name := range args {
		value, ok := state.env[name]
		if !ok || !state.exported[name] {
			status = 1
			continue
		}
		fmt.Fprintln(ioStreams.out, value)
	}
	return normal(status)
}

func builtinSleep(ctx context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		fmt.Fprintln(ioStreams.err, "sleep: missing duration")
		return normal(2)
	}
	total := time.Duration(0)
	for _, value := range args {
		duration, err := parseSleepDuration(value)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "sleep: invalid duration %q\n", value)
			return normal(2)
		}
		if duration > 0 && total > time.Duration(1<<63-1)-duration {
			fmt.Fprintln(ioStreams.err, "sleep: duration overflow")
			return normal(2)
		}
		total += duration
	}
	timer := time.NewTimer(total)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return failure(ctx.Err())
	case <-timer.C:
		return normal(0)
	}
}

func parseSleepDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, errors.New("empty duration")
	}
	if strings.ContainsAny(value, "hmsuµn") {
		return time.ParseDuration(value)
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds < 0 {
		return 0, errors.New("invalid duration")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func builtinUname(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	showSystem, showMachine := len(args) == 0, false
	all := false
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		if len(arg) < 2 || arg[0] != '-' {
			fmt.Fprintf(ioStreams.err, "uname: unsupported argument %s\n", arg)
			return normal(2)
		}
		for _, flag := range arg[1:] {
			switch flag {
			case 'a':
				all = true
			case 's':
				showSystem = true
			case 'm':
				showMachine = true
			default:
				fmt.Fprintf(ioStreams.err, "uname: unsupported option -%c\n", flag)
				return normal(2)
			}
		}
	}
	if all {
		showSystem, showMachine = true, true
	}
	var values []string
	if showSystem {
		values = append(values, portableSystemName(runtime.GOOS))
	}
	if showMachine {
		values = append(values, portableMachineName(runtime.GOARCH))
	}
	_, err := fmt.Fprintln(ioStreams.out, strings.Join(values, " "))
	return builtinIO(err)
}

func portableSystemName(goos string) string {
	switch goos {
	case "windows":
		return "Windows_NT"
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "android":
		return "Android"
	default:
		return goos
	}
}

func portableMachineName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	default:
		return goarch
	}
}

func builtinWhoami(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) != 0 {
		fmt.Fprintln(ioStreams.err, "whoami: arguments are not supported")
		return normal(2)
	}
	for _, name := range []string{"USER", "USERNAME", "LOGNAME"} {
		if value := state.env[name]; value != "" {
			_, err := fmt.Fprintln(ioStreams.out, value)
			return builtinIO(err)
		}
	}
	fmt.Fprintln(ioStreams.err, "whoami: user name is not available in the environment")
	return normal(1)
}

type findOptions struct {
	name       string
	ignoreCase bool
	fileType   byte
	minDepth   int
	maxDepth   int
}

func builtinFind(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	var roots []string
	index := 0
	for index < len(args) && !strings.HasPrefix(args[index], "-") && args[index] != "!" && args[index] != "(" {
		roots = append(roots, args[index])
		index++
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	options := findOptions{maxDepth: -1}
	for index < len(args) {
		arg := args[index]
		switch arg {
		case "-name", "-iname":
			if index+1 >= len(args) {
				fmt.Fprintf(ioStreams.err, "find: %s requires a pattern\n", arg)
				return normal(2)
			}
			options.name = args[index+1]
			options.ignoreCase = arg == "-iname"
			index += 2
		case "-type":
			if index+1 >= len(args) || len(args[index+1]) != 1 || !strings.ContainsRune("fdl", rune(args[index+1][0])) {
				fmt.Fprintln(ioStreams.err, "find: -type supports f, d or l")
				return normal(2)
			}
			options.fileType = args[index+1][0]
			index += 2
		case "-maxdepth", "-mindepth":
			if index+1 >= len(args) {
				fmt.Fprintf(ioStreams.err, "find: %s requires a number\n", arg)
				return normal(2)
			}
			value, err := strconv.Atoi(args[index+1])
			if err != nil || value < 0 {
				fmt.Fprintf(ioStreams.err, "find: invalid depth %q\n", args[index+1])
				return normal(2)
			}
			if arg == "-maxdepth" {
				options.maxDepth = value
			} else {
				options.minDepth = value
			}
			index += 2
		case "-print":
			index++
		default:
			fmt.Fprintf(ioStreams.err, "find: unsupported expression %s\n", arg)
			return normal(2)
		}
	}
	status := 0
	for _, root := range roots {
		absolute, err := runner.resolvePath(state, root, false)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "find: %s: %v\n", root, err)
			status = 1
			continue
		}
		if err := walkPortableFind(ctx, runner, absolute, root, 0, options, ioStreams.out); err != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			fmt.Fprintf(ioStreams.err, "find: %s: %v\n", root, err)
			status = 1
		}
	}
	return normal(status)
}

func walkPortableFind(ctx context.Context, runner *Runner, absolute, display string, depth int, options findOptions, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := runner.cfg.FileSystem.Lstat(absolute)
	if err != nil {
		return err
	}
	if depth >= options.minDepth && matchesFindOptions(filepath.Base(absolute), info, options) {
		if _, err := fmt.Fprintln(out, display); err != nil {
			return err
		}
	}
	if !info.IsDir() || options.maxDepth >= 0 && depth >= options.maxDepth {
		return nil
	}
	entries, err := readPortableDir(runner.cfg.FileSystem, absolute)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childDisplay := filepath.Join(display, entry.name)
		if display == "." {
			childDisplay = "." + string(filepath.Separator) + entry.name
		}
		if err := walkPortableFind(ctx, runner, filepath.Join(absolute, entry.name), childDisplay, depth+1, options, out); err != nil {
			return err
		}
	}
	return nil
}

func matchesFindOptions(name string, info fs.FileInfo, options findOptions) bool {
	if options.name != "" {
		pattern, candidate := options.name, name
		if options.ignoreCase {
			pattern, candidate = strings.ToLower(pattern), strings.ToLower(candidate)
		}
		matched, err := filepath.Match(pattern, candidate)
		if err != nil || !matched {
			return false
		}
	}
	switch options.fileType {
	case 'f':
		return info.Mode().IsRegular()
	case 'd':
		return info.IsDir()
	case 'l':
		return info.Mode()&fs.ModeSymlink != 0
	default:
		return true
	}
}

type grepOptions struct {
	ignoreCase bool
	number     bool
	invert     bool
	quiet      bool
	count      bool
	filesMatch bool
	filesNo    bool
	recursive  bool
	fixed      bool
	forceName  bool
	hideName   bool
}

type grepMatcher func(string) bool

func builtinGrep(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	options := grepOptions{}
	pattern := ""
	patternSet := false
	index := 0
	parseOptions := true
	for index < len(args) {
		arg := args[index]
		if parseOptions && arg == "--" {
			parseOptions = false
			index++
			continue
		}
		if parseOptions && (arg == "-e" || arg == "--regexp") {
			if index+1 >= len(args) {
				fmt.Fprintln(ioStreams.err, "grep: -e requires a pattern")
				return normal(2)
			}
			pattern = args[index+1]
			patternSet = true
			index += 2
			continue
		}
		if parseOptions && strings.HasPrefix(arg, "-") && arg != "-" {
			valid := true
			for _, flag := range strings.TrimPrefix(arg, "-") {
				switch flag {
				case 'i':
					options.ignoreCase = true
				case 'n':
					options.number = true
				case 'v':
					options.invert = true
				case 'q':
					options.quiet = true
				case 'c':
					options.count = true
				case 'l':
					options.filesMatch = true
				case 'L':
					options.filesNo = true
				case 'r', 'R':
					options.recursive = true
				case 'F':
					options.fixed = true
				case 'E':
					// Go's RE2 syntax is used for regexp mode.
				case 'H':
					options.forceName = true
				case 'h':
					options.hideName = true
				default:
					valid = false
				}
			}
			if !valid {
				fmt.Fprintf(ioStreams.err, "grep: unsupported option %s\n", arg)
				return normal(2)
			}
			index++
			continue
		}
		break
	}
	if !patternSet {
		if index >= len(args) {
			fmt.Fprintln(ioStreams.err, "grep: missing pattern")
			return normal(2)
		}
		pattern = args[index]
		index++
	}
	matcher, err := makeGrepMatcher(pattern, options)
	if err != nil {
		fmt.Fprintf(ioStreams.err, "grep: %v\n", err)
		return normal(2)
	}
	files := append([]string(nil), args[index:]...)
	if len(files) == 0 {
		files = []string{"-"}
	}
	expanded, expandErrs := expandGrepFiles(ctx, runner, state, files, options.recursive)
	for _, expandErr := range expandErrs {
		fmt.Fprintf(ioStreams.err, "grep: %v\n", expandErr)
	}
	if len(expanded) == 0 && len(expandErrs) > 0 {
		return normal(2)
	}
	showName := options.forceName || len(expanded) > 1 || options.recursive
	if options.hideName {
		showName = false
	}
	matchedAny := false
	hadError := len(expandErrs) > 0
	for _, value := range expanded {
		reader, closeFn, openErr := openPortableInput(runner, state, value, ioStreams.in)
		if openErr != nil {
			fmt.Fprintf(ioStreams.err, "grep: %s: %v\n", value, openErr)
			hadError = true
			continue
		}
		matched, scanErr := grepReader(ctx, reader, value, matcher, options, showName, ioStreams.out)
		closeFn()
		if scanErr != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			fmt.Fprintf(ioStreams.err, "grep: %s: %v\n", value, scanErr)
			hadError = true
			continue
		}
		if matched {
			matchedAny = true
			if options.quiet {
				return normal(0)
			}
		}
	}
	if hadError {
		return normal(2)
	}
	if matchedAny {
		return normal(0)
	}
	return normal(1)
}

func makeGrepMatcher(pattern string, options grepOptions) (grepMatcher, error) {
	if options.fixed {
		if options.ignoreCase {
			pattern = strings.ToLower(pattern)
			return func(value string) bool { return strings.Contains(strings.ToLower(value), pattern) }, nil
		}
		return func(value string) bool { return strings.Contains(value, pattern) }, nil
	}
	if options.ignoreCase {
		pattern = "(?i:" + pattern + ")"
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return expression.MatchString, nil
}

func expandGrepFiles(ctx context.Context, runner *Runner, state *shellState, values []string, recursive bool) ([]string, []error) {
	var result []string
	var errs []error
	for _, value := range values {
		if value == "-" {
			result = append(result, value)
			continue
		}
		absolute, err := runner.resolvePath(state, value, false)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", value, err))
			continue
		}
		info, err := runner.cfg.FileSystem.Lstat(absolute)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", value, err))
			continue
		}
		if !info.IsDir() {
			result = append(result, value)
			continue
		}
		if !recursive {
			errs = append(errs, fmt.Errorf("%s: is a directory", value))
			continue
		}
		walked, err := collectPortableFiles(ctx, runner, absolute, value)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", value, err))
			continue
		}
		result = append(result, walked...)
	}
	sort.Strings(result)
	return result, errs
}

func collectPortableFiles(ctx context.Context, runner *Runner, absolute, display string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := readPortableDir(runner.cfg.FileSystem, absolute)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		childAbsolute := filepath.Join(absolute, entry.name)
		childDisplay := filepath.Join(display, entry.name)
		if display == "." {
			childDisplay = "." + string(filepath.Separator) + entry.name
		}
		if entry.info.IsDir() {
			nested, err := collectPortableFiles(ctx, runner, childAbsolute, childDisplay)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		} else if entry.info.Mode().IsRegular() {
			result = append(result, childDisplay)
		}
	}
	return result, nil
}

func grepReader(ctx context.Context, in io.Reader, name string, matcher grepMatcher, options grepOptions, showName bool, out io.Writer) (bool, error) {
	reader := bufio.NewReader(in)
	lineNumber := 0
	matchCount := 0
	matchedAny := false
	for {
		if err := ctx.Err(); err != nil {
			return matchedAny, err
		}
		line, err := reader.ReadString('\n')
		if line != "" {
			lineNumber++
			candidate := strings.TrimSuffix(line, "\n")
			candidate = strings.TrimSuffix(candidate, "\r")
			matched := matcher(candidate)
			if options.invert {
				matched = !matched
			}
			if matched {
				matchedAny = true
				matchCount++
				if options.quiet {
					return true, nil
				}
				if !options.count && !options.filesMatch && !options.filesNo {
					if showName {
						fmt.Fprintf(out, "%s:", name)
					}
					if options.number {
						fmt.Fprintf(out, "%d:", lineNumber)
					}
					if _, writeErr := io.WriteString(out, line); writeErr != nil {
						return matchedAny, writeErr
					}
					if !strings.HasSuffix(line, "\n") {
						fmt.Fprintln(out)
					}
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return matchedAny, err
			}
			break
		}
	}
	if options.filesMatch && matchedAny {
		fmt.Fprintln(out, name)
	}
	if options.filesNo && !matchedAny {
		fmt.Fprintln(out, name)
	}
	if options.count {
		if showName {
			fmt.Fprintf(out, "%s:", name)
		}
		fmt.Fprintln(out, matchCount)
	}
	selected := matchedAny
	if options.filesNo {
		selected = !matchedAny
	}
	return selected, nil
}
