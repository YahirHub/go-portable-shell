package portablesh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

func openPortableInput(runner *Runner, state *shellState, value string, stdin io.Reader) (io.Reader, func(), error) {
	if value == "-" {
		return stdin, func() {}, nil
	}
	path, err := runner.resolvePath(state, value, false)
	if err != nil {
		return nil, nil, err
	}
	release, err := runner.acquireOpenFile(state)
	if err != nil {
		return nil, nil, err
	}
	file, err := runner.cfg.FileSystem.Open(path)
	if err != nil {
		release()
		return nil, nil, err
	}
	return file, func() { _ = file.Close(); release() }, nil
}

func builtinHead(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	count := int64(10)
	bytesMode := false
	var files []string
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			files = append(files, args[1:]...)
			break
		}
		if arg == "-n" || arg == "--lines" || arg == "-c" || arg == "--bytes" {
			if len(args) < 2 {
				fmt.Fprintf(ioStreams.err, "head: %s requires a count\n", arg)
				return normal(2)
			}
			parsed, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil || parsed < 0 {
				fmt.Fprintf(ioStreams.err, "head: invalid count %q\n", args[1])
				return normal(2)
			}
			count = parsed
			bytesMode = arg == "-c" || arg == "--bytes"
			args = args[2:]
			continue
		}
		if len(arg) > 1 && arg[0] == '-' {
			if parsed, err := strconv.ParseInt(arg[1:], 10, 64); err == nil {
				count = parsed
				bytesMode = false
				args = args[1:]
				continue
			}
			fmt.Fprintf(ioStreams.err, "head: unsupported option %s\n", arg)
			return normal(2)
		}
		files = append(files, args...)
		break
	}
	if len(files) == 0 {
		files = []string{"-"}
	}
	status := 0
	for index, value := range files {
		if len(files) > 1 {
			if index > 0 {
				fmt.Fprintln(ioStreams.out)
			}
			fmt.Fprintf(ioStreams.out, "==> %s <==\n", value)
		}
		reader, closeFn, err := openPortableInput(runner, state, value, ioStreams.in)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "head: %s: %v\n", value, err)
			status = 1
			continue
		}
		if bytesMode {
			err = copyFirstBytes(ctx, ioStreams.out, reader, count)
		} else {
			err = copyFirstLines(ctx, ioStreams.out, reader, count)
		}
		closeFn()
		if err != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			fmt.Fprintf(ioStreams.err, "head: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func copyFirstBytes(ctx context.Context, out io.Writer, in io.Reader, count int64) error {
	remaining := count
	buffer := make([]byte, 32*1024)
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := int64(len(buffer))
		if remaining < want {
			want = remaining
		}
		n, err := in.Read(buffer[:want])
		if n > 0 {
			if _, writeErr := out.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			remaining -= int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
	return nil
}

func copyFirstLines(ctx context.Context, out io.Writer, in io.Reader, count int64) error {
	reader := bufio.NewReader(in)
	for line := int64(0); line < count; line++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := reader.ReadString('\n')
		if value != "" {
			if _, writeErr := io.WriteString(out, value); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
	return nil
}

func builtinTail(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	count := int64(10)
	bytesMode := false
	var files []string
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			files = append(files, args[1:]...)
			break
		}
		if arg == "-n" || arg == "--lines" || arg == "-c" || arg == "--bytes" {
			if len(args) < 2 {
				fmt.Fprintf(ioStreams.err, "tail: %s requires a count\n", arg)
				return normal(2)
			}
			parsed, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil || parsed < 0 {
				fmt.Fprintf(ioStreams.err, "tail: invalid count %q\n", args[1])
				return normal(2)
			}
			count = parsed
			bytesMode = arg == "-c" || arg == "--bytes"
			args = args[2:]
			continue
		}
		if len(arg) > 1 && arg[0] == '-' {
			if parsed, err := strconv.ParseInt(arg[1:], 10, 64); err == nil {
				count = parsed
				bytesMode = false
				args = args[1:]
				continue
			}
			fmt.Fprintf(ioStreams.err, "tail: unsupported option %s\n", arg)
			return normal(2)
		}
		files = append(files, args...)
		break
	}
	if len(files) == 0 {
		files = []string{"-"}
	}
	status := 0
	for index, value := range files {
		if len(files) > 1 {
			if index > 0 {
				fmt.Fprintln(ioStreams.out)
			}
			fmt.Fprintf(ioStreams.out, "==> %s <==\n", value)
		}
		reader, closeFn, err := openPortableInput(runner, state, value, ioStreams.in)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "tail: %s: %v\n", value, err)
			status = 1
			continue
		}
		if bytesMode {
			err = copyLastBytes(ctx, ioStreams.out, reader, count)
		} else {
			err = copyLastLines(ctx, ioStreams.out, reader, count)
		}
		closeFn()
		if err != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			fmt.Fprintf(ioStreams.err, "tail: %s: %v\n", value, err)
			status = 1
		}
	}
	return normal(status)
}

func copyLastBytes(ctx context.Context, out io.Writer, in io.Reader, count int64) error {
	if count == 0 {
		return nil
	}
	if count > int64(^uint(0)>>1) {
		return fmt.Errorf("requested byte count is too large")
	}
	ring := make([]byte, int(count))
	position, total := 0, int64(0)
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := in.Read(buffer)
		for _, b := range buffer[:n] {
			ring[position] = b
			position = (position + 1) % len(ring)
			total++
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			break
		}
	}
	if total < count {
		_, err := out.Write(ring[:int(total)])
		return err
	}
	if _, err := out.Write(ring[position:]); err != nil {
		return err
	}
	_, err := out.Write(ring[:position])
	return err
}

func copyLastLines(ctx context.Context, out io.Writer, in io.Reader, count int64) error {
	if count == 0 {
		return nil
	}
	if count > int64(^uint(0)>>1) {
		return fmt.Errorf("requested line count is too large")
	}
	ring := make([]string, int(count))
	position, total := 0, int64(0)
	reader := bufio.NewReader(in)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if line != "" {
			ring[position] = line
			position = (position + 1) % len(ring)
			total++
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			break
		}
	}
	start, amount := position, int(count)
	if total < count {
		start, amount = 0, int(total)
	}
	for index := 0; index < amount; index++ {
		value := ring[(start+index)%len(ring)]
		if _, err := io.WriteString(out, value); err != nil {
			return err
		}
	}
	return nil
}

type wcCounts struct {
	lines int64
	words int64
	bytes int64
	chars int64
}

func builtinWC(ctx context.Context, runner *Runner, state *shellState, args []string, ioStreams streams) flowResult {
	showLines, showWords, showBytes, showChars := false, false, false, false
	var files []string
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
				case 'l':
					showLines = true
				case 'w':
					showWords = true
				case 'c':
					showBytes = true
				case 'm':
					showChars = true
				default:
					valid = false
				}
			}
			if !valid {
				fmt.Fprintf(ioStreams.err, "wc: unsupported option %s\n", arg)
				return normal(2)
			}
			continue
		}
		parseOptions = false
		files = append(files, arg)
	}
	if !showLines && !showWords && !showBytes && !showChars {
		showLines, showWords, showBytes = true, true, true
	}
	if len(files) == 0 {
		files = []string{"-"}
	}
	status := 0
	total := wcCounts{}
	validFiles := 0
	for _, value := range files {
		reader, closeFn, err := openPortableInput(runner, state, value, ioStreams.in)
		if err != nil {
			fmt.Fprintf(ioStreams.err, "wc: %s: %v\n", value, err)
			status = 1
			continue
		}
		counts, err := countPortableReader(ctx, reader)
		closeFn()
		if err != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			fmt.Fprintf(ioStreams.err, "wc: %s: %v\n", value, err)
			status = 1
			continue
		}
		validFiles++
		total.lines += counts.lines
		total.words += counts.words
		total.bytes += counts.bytes
		total.chars += counts.chars
		name := ""
		if value != "-" || len(files) > 1 {
			name = value
		}
		writeWC(ioStreams.out, counts, showLines, showWords, showBytes, showChars, name)
	}
	if len(files) > 1 && validFiles > 1 {
		writeWC(ioStreams.out, total, showLines, showWords, showBytes, showChars, "total")
	}
	return normal(status)
}

func countPortableReader(ctx context.Context, in io.Reader) (wcCounts, error) {
	reader := bufio.NewReader(in)
	result := wcCounts{}
	inWord := false
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		r, size, err := reader.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return result, nil
			}
			return result, err
		}
		result.chars++
		result.bytes += int64(size)
		if r == '\n' {
			result.lines++
		}
		space := unicode.IsSpace(r)
		if !space && !inWord {
			result.words++
		}
		inWord = !space
	}
}

func writeWC(out io.Writer, counts wcCounts, lines, words, bytesFlag, chars bool, name string) {
	var values []string
	if lines {
		values = append(values, strconv.FormatInt(counts.lines, 10))
	}
	if words {
		values = append(values, strconv.FormatInt(counts.words, 10))
	}
	if bytesFlag {
		values = append(values, strconv.FormatInt(counts.bytes, 10))
	}
	if chars {
		values = append(values, strconv.FormatInt(counts.chars, 10))
	}
	if name != "" {
		values = append(values, name)
	}
	fmt.Fprintln(out, strings.Join(values, " "))
}
