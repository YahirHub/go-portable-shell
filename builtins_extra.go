package portablesh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func builtinSource(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		fmt.Fprintln(ioStreams.err, "source: file required")
		return normal(2)
	}
	if runner.cfg.MaxSourceDepth >= 0 && state.sourceDepth >= runner.cfg.MaxSourceDepth {
		return failure(runner.limitError("source_depth", int64(runner.cfg.MaxSourceDepth)))
	}
	path, err := runner.findSourcePath(state.dir, state.environment(), args[0])
	if err != nil {
		fmt.Fprintf(ioStreams.err, "source: %v\n", err)
		return normal(1)
	}
	path, err = runner.resolvePath(state, path, false)
	if err != nil {
		fmt.Fprintf(ioStreams.err, "source: %v\n", err)
		return normal(1)
	}
	var source []byte
	if runner.cfg.SourceLoader != nil {
		source, err = runner.cfg.SourceLoader(ctx, path)
	} else {
		source, err = runner.cfg.FileSystem.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintf(ioStreams.err, "source: %v\n", err)
		return normal(1)
	}
	if limitExceeded(int64(len(source)), runner.cfg.MaxScriptBytes) {
		return failure(runner.limitError("script_bytes", runner.cfg.MaxScriptBytes))
	}
	program, err := Parse(string(source))
	if err != nil {
		return failure(err)
	}
	previousPosition := state.position
	if len(args) > 1 {
		state.position = append([]string(nil), args[1:]...)
	}
	state.sourceDepth++
	result := runner.execNode(ctx, state, program.root, ioStreams)
	state.sourceDepth--
	state.position = previousPosition
	if result.kind == flowReturn {
		result.kind = flowNone
	}
	return result
}

func (r *Runner) findSourcePath(dir string, environment []string, name string) (string, error) {
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		if !filepath.IsAbs(name) {
			name = filepath.Join(dir, name)
		}
		if info, err := r.cfg.FileSystem.Stat(name); err == nil && !info.IsDir() {
			return name, nil
		}
		return "", fmt.Errorf("%s: file not found", name)
	}
	pathValue := envMap(environment)["PATH"]
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = dir
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(dir, directory)
		}
		candidate := filepath.Join(directory, name)
		if info, err := r.cfg.FileSystem.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: file not found", name)
}

func builtinLocal(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(state.locals) == 0 {
		fmt.Fprintln(ioStreams.err, "local: can only be used in a function")
		return normal(1)
	}
	for _, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		if !validName(name) {
			fmt.Fprintf(ioStreams.err, "local: invalid name %q\n", name)
			return normal(2)
		}
		if state.readonly[name] {
			fmt.Fprintf(ioStreams.err, "local: %s: readonly variable\n", name)
			return normal(1)
		}
		if err := rememberLocal(state, name); err != nil {
			fmt.Fprintln(ioStreams.err, err)
			return normal(1)
		}
		if !hasValue {
			value = ""
		}
		if err := assignVariable(state, name, value); err != nil {
			fmt.Fprintf(ioStreams.err, "local: %v\n", err)
			return normal(1)
		}
		delete(state.exported, name)
	}
	return normal(0)
}

func builtinReadonly(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 || len(args) == 1 && args[0] == "-p" {
		names := make([]string, 0, len(state.readonly))
		for name := range state.readonly {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(ioStreams.out, "readonly %s=%s\n", name, Quote(state.env[name]))
		}
		return normal(0)
	}
	for _, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		if !validName(name) {
			fmt.Fprintf(ioStreams.err, "readonly: invalid name %q\n", name)
			return normal(2)
		}
		if hasValue {
			if err := assignVariable(state, name, value); err != nil {
				fmt.Fprintf(ioStreams.err, "readonly: %v\n", err)
				return normal(1)
			}
		}
		state.readonly[name] = true
	}
	return normal(0)
}

func builtinTrap(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 || len(args) == 1 && args[0] == "-p" {
		names := make([]string, 0, len(state.traps))
		for name := range state.traps {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(ioStreams.out, "trap -- %s %s\n", Quote(state.traps[name]), name)
		}
		return normal(0)
	}
	if len(args) < 2 {
		fmt.Fprintln(ioStreams.err, "trap: action and signal required")
		return normal(2)
	}
	action := args[0]
	for _, raw := range args[1:] {
		name, ok := normalizeTrapName(raw)
		if !ok {
			fmt.Fprintf(ioStreams.err, "trap: unsupported signal %s\n", raw)
			return normal(2)
		}
		if action == "-" {
			delete(state.traps, name)
		} else {
			state.traps[name] = action
		}
	}
	return normal(0)
}

func normalizeTrapName(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimPrefix(value, "SIG"))
	switch value {
	case "0", "EXIT":
		return "EXIT", true
	case "2", "INT":
		return "INT", true
	case "15", "TERM":
		return "TERM", true
	default:
		return "", false
	}
}

func builtinGetopts(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) < 2 || !validName(args[1]) {
		fmt.Fprintln(ioStreams.err, "getopts: OPTSTRING and variable name required")
		return normal(2)
	}
	for _, name := range []string{args[1], "OPTARG", "OPTIND"} {
		if state.readonly[name] {
			fmt.Fprintf(ioStreams.err, "getopts: %s: readonly variable\n", name)
			return normal(1)
		}
	}
	options := args[0]
	silent := strings.HasPrefix(options, ":")
	options = strings.TrimPrefix(options, ":")
	values := state.position
	if len(args) > 2 {
		values = args[2:]
	}
	if configured, err := strconv.Atoi(state.env["OPTIND"]); err == nil && configured > 0 && configured != state.getoptsIndex {
		state.getoptsIndex = configured
		state.getoptsOffset = 0
	}
	if state.getoptsIndex < 1 {
		state.getoptsIndex = 1
	}
	if state.getoptsIndex > len(values) {
		state.env["OPTIND"] = strconv.Itoa(state.getoptsIndex)
		return normal(1)
	}
	current := values[state.getoptsIndex-1]
	if state.getoptsOffset == 0 {
		if current == "--" {
			state.getoptsIndex++
			state.env["OPTIND"] = strconv.Itoa(state.getoptsIndex)
			return normal(1)
		}
		if len(current) < 2 || current[0] != '-' || current == "-" {
			return normal(1)
		}
		state.getoptsOffset = 1
	}
	option := current[state.getoptsOffset]
	state.getoptsOffset++
	position := strings.IndexByte(options, option)
	if position < 0 || option == ':' {
		state.env[args[1]] = "?"
		state.env["OPTARG"] = string(option)
		if !silent {
			fmt.Fprintf(ioStreams.err, "getopts: illegal option -- %c\n", option)
		}
		advanceGetopts(state, current)
		return normal(0)
	}
	requiresArgument := position+1 < len(options) && options[position+1] == ':'
	state.env[args[1]] = string(option)
	delete(state.env, "OPTARG")
	if requiresArgument {
		if state.getoptsOffset < len(current) {
			state.env["OPTARG"] = current[state.getoptsOffset:]
			state.getoptsOffset = len(current)
		} else if state.getoptsIndex < len(values) {
			state.getoptsIndex++
			state.env["OPTARG"] = values[state.getoptsIndex-1]
		} else {
			if silent {
				state.env[args[1]] = ":"
				state.env["OPTARG"] = string(option)
			} else {
				state.env[args[1]] = "?"
				fmt.Fprintf(ioStreams.err, "getopts: option requires an argument -- %c\n", option)
			}
		}
	}
	advanceGetopts(state, current)
	return normal(0)
}

func advanceGetopts(state *shellState, current string) {
	if state.getoptsOffset >= len(current) {
		state.getoptsIndex++
		state.getoptsOffset = 0
	}
	state.env["OPTIND"] = strconv.Itoa(state.getoptsIndex)
}

func builtinUmask(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		fmt.Fprintf(ioStreams.out, "%04o\n", state.umask.Perm())
		return normal(0)
	}
	if len(args) != 1 {
		fmt.Fprintln(ioStreams.err, "umask: too many arguments")
		return normal(2)
	}
	modeText := strings.TrimPrefix(args[0], "0")
	if modeText == "" {
		modeText = "0"
	}
	value, err := strconv.ParseUint(modeText, 8, 9)
	if err != nil || value > 0o777 {
		fmt.Fprintln(ioStreams.err, "umask: octal mode required")
		return normal(2)
	}
	state.umask = os.FileMode(value)
	return normal(0)
}

func builtinExec(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		return normal(0)
	}
	result := builtinCommand(ctx, runner, state, args, ioStreams)
	if result.err != nil || result.kind != flowNone {
		return result
	}
	result.kind = flowExit
	return result
}

func builtinHash(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 1 && args[0] == "-r" {
		clear(state.commandHash)
		return normal(0)
	}
	if len(args) == 0 {
		names := make([]string, 0, len(state.commandHash))
		for name := range state.commandHash {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(ioStreams.out, "%s=%s\n", name, state.commandHash[name])
		}
		return normal(0)
	}
	for _, name := range args {
		path, err := LookPath(state.dir, state.environment(), name)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "hash: %s: not found\n", name)
			return normal(1)
		}
		state.commandHash[name] = path
	}
	return normal(0)
}

func builtinTimes(_ context.Context, _ *Runner, state *shellState, _ []string, ioStreams streams) flowResult {
	elapsed := time.Since(state.started)
	if state.started.IsZero() {
		elapsed = 0
	}
	minutes := int(elapsed / time.Minute)
	seconds := elapsed.Seconds() - float64(minutes*60)
	fmt.Fprintf(ioStreams.out, "%dm%.3fs 0m0.000s\n0m0.000s 0m0.000s\n", minutes, seconds)
	return normal(0)
}
