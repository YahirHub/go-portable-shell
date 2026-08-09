package portablesh

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type builtinFunc func(context.Context, *Runner, *shellState, []string, streams) flowResult

var builtins map[string]builtinFunc

func init() {
	builtins = map[string]builtinFunc{
		":":        builtinTrue,
		"true":     builtinTrue,
		"false":    builtinFalse,
		"echo":     builtinEcho,
		"printf":   builtinPrintf,
		"pwd":      builtinPwd,
		"cd":       builtinCD,
		"export":   builtinExport,
		"unset":    builtinUnset,
		"test":     builtinTest,
		"[":        builtinBracket,
		"read":     builtinRead,
		"set":      builtinSet,
		"shift":    builtinShift,
		"break":    builtinBreak,
		"continue": builtinContinue,
		"return":   builtinReturn,
		"exit":     builtinExit,
		"type":     builtinType,
		"command":  builtinCommand,
	}
}

func builtinTrue(context.Context, *Runner, *shellState, []string, streams) flowResult {
	return normal(0)
}

func builtinFalse(context.Context, *Runner, *shellState, []string, streams) flowResult {
	return normal(1)
}

func builtinEcho(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	newline, escapes := true, false
	for len(args) > 0 {
		option := args[0]
		if len(option) < 2 || option[0] != '-' {
			goto output
		}
		valid := true
		for _, flag := range option[1:] {
			switch flag {
			case 'n':
				newline = false
			case 'e':
				escapes = true
			case 'E':
				escapes = false
			default:
				valid = false
			}
		}
		if !valid {
			goto output
		}
		args = args[1:]
	}
output:
	text := strings.Join(args, " ")
	stop := false
	if escapes {
		text, stop = decodeEscapes(text)
	}
	if stop {
		newline = false
	}
	if newline {
		text += "\n"
	}
	_, err := io.WriteString(ioStreams.out, text)
	return builtinIO(err)
}

func builtinPrintf(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		return normal(0)
	}
	format := args[0]
	values := args[1:]
	used := 0
	for {
		consumed, stop, err := printFormat(ioStreams.out, format, values[used:])
		if err != nil {
			fmt.Fprintf(ioStreams.err, "printf: %v\n", err)
			return normal(1)
		}
		used += consumed
		if stop || used >= len(values) || consumed == 0 {
			return normal(0)
		}
	}
}

func printFormat(w io.Writer, format string, args []string) (int, bool, error) {
	used := 0
	for index := 0; index < len(format); {
		if format[index] == '\\' {
			value, consumed, stop := decodeOneEscape(format[index:])
			if _, err := io.WriteString(w, value); err != nil {
				return used, false, err
			}
			index += consumed
			if stop {
				return used, true, nil
			}
			continue
		}
		if format[index] != '%' {
			if _, err := io.WriteString(w, string(format[index])); err != nil {
				return used, false, err
			}
			index++
			continue
		}
		if index+1 < len(format) && format[index+1] == '%' {
			if _, err := io.WriteString(w, "%"); err != nil {
				return used, false, err
			}
			index += 2
			continue
		}
		end := index + 1
		for end < len(format) && strings.ContainsRune("-+ #0.123456789", rune(format[end])) {
			end++
		}
		if end >= len(format) {
			return used, false, fmt.Errorf("incomplete conversion")
		}
		verb := format[end]
		spec := format[index : end+1]
		value := ""
		if used < len(args) {
			value = args[used]
		}
		used++
		var rendered string
		switch verb {
		case 's':
			rendered = fmt.Sprintf(spec, value)
		case 'q':
			rendered = strconv.Quote(value)
		case 'b':
			var stop bool
			rendered, stop = decodeEscapes(value)
			if _, err := io.WriteString(w, rendered); err != nil {
				return used, false, err
			}
			if stop {
				return used, true, nil
			}
			index = end + 1
			continue
		case 'c':
			if value != "" {
				char, _ := utf8.DecodeRuneInString(value)
				rendered = string(char)
			}
		case 'd', 'i', 'u', 'o', 'x', 'X':
			number, err := strconv.ParseInt(value, 0, 64)
			if value == "" {
				number, err = 0, nil
			}
			if err != nil {
				return used, false, fmt.Errorf("%q is not a number", value)
			}
			if verb == 'u' {
				rendered = fmt.Sprintf(spec, uint64(number))
			} else {
				rendered = fmt.Sprintf(spec, number)
			}
		default:
			return used, false, fmt.Errorf("unsupported conversion %%%c", verb)
		}
		if _, err := io.WriteString(w, rendered); err != nil {
			return used, false, err
		}
		index = end + 1
	}
	return used, false, nil
}

func builtinPwd(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) > 1 || len(args) == 1 && args[0] != "-L" && args[0] != "-P" {
		fmt.Fprintln(ioStreams.err, "pwd: unsupported arguments")
		return normal(2)
	}
	_, err := fmt.Fprintln(ioStreams.out, state.dir)
	return builtinIO(err)
}

func builtinCD(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) > 1 {
		fmt.Fprintln(ioStreams.err, "cd: too many arguments")
		return normal(2)
	}
	target := state.env["HOME"]
	show := false
	if len(args) == 1 {
		target = args[0]
	}
	if target == "-" {
		target = state.env["OLDPWD"]
		show = true
	}
	if target == "" {
		fmt.Fprintln(ioStreams.err, "cd: target directory is empty")
		return normal(1)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(state.dir, target)
	}
	target, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(ioStreams.err, "cd: %v\n", err)
		return normal(1)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		fmt.Fprintf(ioStreams.err, "cd: %s: %v\n", target, err)
		return normal(1)
	}
	state.env["OLDPWD"] = state.dir
	state.env["PWD"] = target
	state.exported["OLDPWD"] = true
	state.exported["PWD"] = true
	state.dir = target
	if show {
		fmt.Fprintln(ioStreams.out, target)
	}
	return normal(0)
}

func builtinExport(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 || len(args) == 1 && args[0] == "-p" {
		names := make([]string, 0, len(state.exported))
		for name := range state.exported {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(ioStreams.out, "export %s=%s\n", name, strconv.Quote(state.env[name]))
		}
		return normal(0)
	}
	for _, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		if !validName(name) {
			fmt.Fprintf(ioStreams.err, "export: invalid name %q\n", name)
			return normal(2)
		}
		if hasValue {
			state.env[name] = value
		}
		state.exported[name] = true
	}
	return normal(0)
}

func builtinUnset(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	for _, name := range args {
		if !validName(name) {
			fmt.Fprintf(ioStreams.err, "unset: invalid name %q\n", name)
			return normal(2)
		}
		delete(state.env, name)
		delete(state.exported, name)
		delete(state.functions, name)
	}
	return normal(0)
}

func builtinTest(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	truth, err := evaluateTest(state, args)
	if err != nil {
		fmt.Fprintf(ioStreams.err, "test: %v\n", err)
		return normal(2)
	}
	if truth {
		return normal(0)
	}
	return normal(1)
}

func builtinBracket(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 || args[len(args)-1] != "]" {
		fmt.Fprintln(ioStreams.err, "[: missing ]")
		return normal(2)
	}
	return builtinTest(ctx, runner, state, args[:len(args)-1], ioStreams)
}

func evaluateTest(state *shellState, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if args[0] == "!" {
		value, err := evaluateTest(state, args[1:])
		return !value, err
	}
	if len(args) == 1 {
		return args[0] != "", nil
	}
	if len(args) == 2 {
		switch args[0] {
		case "-n":
			return args[1] != "", nil
		case "-z":
			return args[1] == "", nil
		case "-e", "-f", "-d", "-s", "-L", "-h", "-r", "-w", "-x":
			path := args[1]
			if !filepath.IsAbs(path) {
				path = filepath.Join(state.dir, path)
			}
			info, err := os.Lstat(path)
			if err != nil {
				return false, nil
			}
			switch args[0] {
			case "-e":
				return true, nil
			case "-f":
				return info.Mode().IsRegular(), nil
			case "-d":
				return info.IsDir(), nil
			case "-s":
				return info.Size() > 0, nil
			case "-L", "-h":
				return info.Mode()&os.ModeSymlink != 0, nil
			case "-r":
				_, err := os.Open(path)
				return err == nil, nil
			case "-w":
				return info.Mode().Perm()&0o222 != 0, nil
			case "-x":
				return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0, nil
			}
		}
	}
	if len(args) == 3 {
		left, op, right := args[0], args[1], args[2]
		switch op {
		case "=", "==":
			return left == right, nil
		case "!=":
			return left != right, nil
		case "<":
			return left < right, nil
		case ">":
			return left > right, nil
		case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
			a, errA := strconv.ParseInt(left, 10, 64)
			b, errB := strconv.ParseInt(right, 10, 64)
			if errA != nil || errB != nil {
				return false, fmt.Errorf("integer expression expected")
			}
			switch op {
			case "-eq":
				return a == b, nil
			case "-ne":
				return a != b, nil
			case "-lt":
				return a < b, nil
			case "-le":
				return a <= b, nil
			case "-gt":
				return a > b, nil
			case "-ge":
				return a >= b, nil
			}
		}
	}
	return false, fmt.Errorf("unsupported expression")
}

func builtinRead(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	raw := false
	var names []string
	for _, arg := range args {
		if arg == "-r" {
			raw = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			fmt.Fprintf(ioStreams.err, "read: unsupported option %s\n", arg)
			return normal(2)
		}
		names = append(names, arg)
	}
	if len(names) == 0 {
		names = []string{"REPLY"}
	}
	line, err := readOneLine(ioStreams.in)
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if !raw {
		line = strings.ReplaceAll(line, `\ `, " ")
	}
	fields := strings.Fields(line)
	for index, name := range names {
		if !validName(name) {
			fmt.Fprintf(ioStreams.err, "read: invalid name %q\n", name)
			return normal(2)
		}
		value := ""
		if index < len(fields) {
			if index == len(names)-1 {
				value = strings.Join(fields[index:], " ")
			} else {
				value = fields[index]
			}
		}
		state.env[name] = value
	}
	if err != nil {
		return normal(1)
	}
	return normal(0)
}

func builtinSet(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		names := make([]string, 0, len(state.env))
		for name := range state.env {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(ioStreams.out, "%s=%s\n", name, strconv.Quote(state.env[name]))
		}
		return normal(0)
	}
	for len(args) > 0 {
		switch args[0] {
		case "--":
			state.position = append([]string(nil), args[1:]...)
			return normal(0)
		case "-u":
			state.options.nounset = true
		case "+u":
			state.options.nounset = false
		case "-o", "+o":
			if len(args) < 2 || args[1] != "pipefail" {
				fmt.Fprintln(ioStreams.err, "set: only -o pipefail is supported")
				return normal(2)
			}
			state.options.pipefail = args[0] == "-o"
			args = args[1:]
		default:
			if strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[0], "+") {
				fmt.Fprintf(ioStreams.err, "set: unsupported option %s\n", args[0])
				return normal(2)
			}
			state.position = append([]string(nil), args...)
			return normal(0)
		}
		args = args[1:]
	}
	return normal(0)
}

func builtinShift(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	count := 1
	if len(args) > 1 {
		fmt.Fprintln(ioStreams.err, "shift: too many arguments")
		return normal(2)
	}
	if len(args) == 1 {
		var err error
		count, err = strconv.Atoi(args[0])
		if err != nil || count < 0 {
			fmt.Fprintln(ioStreams.err, "shift: invalid count")
			return normal(2)
		}
	}
	if count > len(state.position) {
		return normal(1)
	}
	state.position = append([]string(nil), state.position[count:]...)
	return normal(0)
}

func builtinBreak(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	levels, ok := optionalLevels(args, ioStreams, "break")
	if !ok {
		return normal(2)
	}
	return flowResult{kind: flowBreak, levels: levels}
}

func builtinContinue(_ context.Context, _ *Runner, _ *shellState, args []string, ioStreams streams) flowResult {
	levels, ok := optionalLevels(args, ioStreams, "continue")
	if !ok {
		return normal(2)
	}
	return flowResult{kind: flowContinue, levels: levels}
}

func builtinReturn(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	status, ok := optionalStatus(args, ioStreams, "return")
	if !ok {
		return normal(2)
	}
	if len(args) == 0 {
		status = state.last
	}
	return flowResult{kind: flowReturn, status: status}
}

func builtinExit(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	status, ok := optionalStatus(args, ioStreams, "exit")
	if !ok {
		return normal(2)
	}
	if len(args) == 0 {
		status = state.last
	}
	return flowResult{kind: flowExit, status: status}
}

func builtinType(_ context.Context, _ *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	status := 0
	for _, name := range args {
		if _, ok := state.functions[name]; ok {
			fmt.Fprintf(ioStreams.out, "%s is a function\n", name)
		} else if builtins[name] != nil {
			fmt.Fprintf(ioStreams.out, "%s is a shell builtin\n", name)
		} else if path, err := LookPath(state.dir, state.environment(), name); err == nil {
			fmt.Fprintf(ioStreams.out, "%s is %s\n", name, path)
		} else {
			fmt.Fprintf(ioStreams.err, "%s: not found\n", name)
			status = 1
		}
	}
	return normal(status)
}

func builtinCommand(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	if len(args) == 0 {
		return normal(0)
	}
	if args[0] == "-v" {
		if len(args) != 2 {
			return normal(2)
		}
		name := args[1]
		if builtins[name] != nil {
			fmt.Fprintln(ioStreams.out, name)
			return normal(0)
		}
		path, err := LookPath(state.dir, state.environment(), name)
		if err != nil {
			return normal(1)
		}
		fmt.Fprintln(ioStreams.out, path)
		return normal(0)
	}
	if builtin := builtins[args[0]]; builtin != nil && args[0] != "command" {
		return builtin(ctx, runner, state, args[1:], ioStreams)
	}
	request := Command{Args: append([]string(nil), args...), Dir: state.dir, Env: state.environment(), Stdin: ioStreams.in, Stdout: ioStreams.out, Stderr: ioStreams.err}
	if runner.cfg.Handler != nil {
		handled, err := runner.cfg.Handler(ctx, request)
		if handled || err != nil {
			return failure(err)
		}
	}
	return failure(runExternal(ctx, request))
}

func optionalStatus(args []string, ioStreams streams, name string) (int, bool) {
	if len(args) == 0 {
		return 0, true
	}
	if len(args) > 1 {
		fmt.Fprintf(ioStreams.err, "%s: too many arguments\n", name)
		return 0, false
	}
	status, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(ioStreams.err, "%s: numeric status required\n", name)
		return 0, false
	}
	return normalizeStatus(status), true
}

func optionalLevels(args []string, ioStreams streams, name string) (int, bool) {
	if len(args) == 0 {
		return 1, true
	}
	if len(args) > 1 {
		fmt.Fprintf(ioStreams.err, "%s: too many arguments\n", name)
		return 0, false
	}
	levels, err := strconv.Atoi(args[0])
	if err != nil || levels < 1 {
		fmt.Fprintf(ioStreams.err, "%s: positive loop level required\n", name)
		return 0, false
	}
	return levels, true
}

func builtinIO(err error) flowResult {
	if err != nil {
		return failure(err)
	}
	return normal(0)
}

func readOneLine(reader io.Reader) (string, error) {
	var result strings.Builder
	buffer := []byte{0}
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			if buffer[0] == '\n' {
				return result.String(), nil
			}
			result.WriteByte(buffer[0])
		}
		if err != nil {
			return result.String(), err
		}
	}
}

func decodeEscapes(value string) (string, bool) {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] != '\\' {
			result.WriteByte(value[index])
			index++
			continue
		}
		decoded, consumed, stop := decodeOneEscape(value[index:])
		result.WriteString(decoded)
		index += consumed
		if stop {
			return result.String(), true
		}
	}
	return result.String(), false
}

func decodeOneEscape(value string) (string, int, bool) {
	if len(value) < 2 || value[0] != '\\' {
		return value[:1], 1, false
	}
	switch value[1] {
	case 'a':
		return "\a", 2, false
	case 'b':
		return "\b", 2, false
	case 'c':
		return "", 2, true
	case 'e', 'E':
		return "\x1b", 2, false
	case 'f':
		return "\f", 2, false
	case 'n':
		return "\n", 2, false
	case 'r':
		return "\r", 2, false
	case 't':
		return "\t", 2, false
	case 'v':
		return "\v", 2, false
	case '\\':
		return "\\", 2, false
	case '0':
		end := 2
		for end < len(value) && end < 5 && value[end] >= '0' && value[end] <= '7' {
			end++
		}
		number, _ := strconv.ParseUint(value[2:end], 8, 8)
		return string([]byte{byte(number)}), end, false
	case 'x':
		end := 2
		for end < len(value) && end < 4 && strings.ContainsRune("0123456789abcdefABCDEF", rune(value[end])) {
			end++
		}
		if end == 2 {
			return "x", 2, false
		}
		number, _ := strconv.ParseUint(value[2:end], 16, 8)
		return string([]byte{byte(number)}), end, false
	default:
		return string(value[1]), 2, false
	}
}
