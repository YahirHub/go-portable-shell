package portablesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type streams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

type flowKind uint8

const (
	flowNone flowKind = iota
	flowBreak
	flowContinue
	flowReturn
	flowExit
)

type flowResult struct {
	status int
	kind   flowKind
	levels int
	err    error
}

func normal(status int) flowResult { return flowResult{status: normalizeStatus(status)} }

func failure(err error) flowResult {
	if err == nil {
		return normal(0)
	}
	if status, ok := Status(err); ok {
		return normal(status)
	}
	return flowResult{status: 1, err: err}
}

func (r *Runner) execNode(ctx context.Context, state *shellState, current node, ioStreams streams) flowResult {
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	switch item := current.(type) {
	case *listNode:
		result := normal(0)
		for _, child := range item.items {
			result = r.execNode(ctx, state, child, ioStreams)
			state.last = result.status
			if result.err != nil || result.kind != flowNone || ctx.Err() != nil {
				return result
			}
		}
		return result
	case *andOrNode:
		result := r.execNode(ctx, state, item.first, ioStreams)
		for _, part := range item.rest {
			if result.err != nil || result.kind != flowNone {
				return result
			}
			if part.op == tokenAndIf && result.status != 0 || part.op == tokenOrIf && result.status == 0 {
				continue
			}
			result = r.execNode(ctx, state, part.node, ioStreams)
			state.last = result.status
		}
		return result
	case *pipelineNode:
		return r.execPipeline(ctx, state, item, ioStreams)
	case *simpleNode:
		return r.execSimple(ctx, state, item, ioStreams)
	case *ifNode:
		for _, branch := range item.branches {
			condition := r.execNode(ctx, state, branch.condition, ioStreams)
			if condition.err != nil || condition.kind != flowNone {
				return condition
			}
			if condition.status == 0 {
				return r.execNode(ctx, state, branch.body, ioStreams)
			}
		}
		if item.other != nil {
			return r.execNode(ctx, state, item.other, ioStreams)
		}
		return normal(0)
	case *whileNode:
		result := normal(0)
		for {
			if err := ctx.Err(); err != nil {
				return failure(err)
			}
			condition := r.execNode(ctx, state, item.condition, ioStreams)
			if condition.err != nil || condition.kind != flowNone {
				return condition
			}
			run := condition.status == 0
			if item.until {
				run = !run
			}
			if !run {
				return result
			}
			result = r.execNode(ctx, state, item.body, ioStreams)
			switch result.kind {
			case flowBreak:
				if result.levels > 1 {
					result.levels--
					return result
				}
				return normal(result.status)
			case flowContinue:
				if result.levels > 1 {
					result.levels--
					return result
				}
				result = normal(0)
				continue
			case flowReturn, flowExit:
				return result
			}
			if result.err != nil {
				return result
			}
		}
	case *forNode:
		values := append([]string(nil), state.position...)
		if item.words != nil {
			var err error
			values, err = r.expandWords(ctx, state, item.words, ioStreams)
			if err != nil {
				return failure(err)
			}
		}
		result := normal(0)
		for _, value := range values {
			if err := ctx.Err(); err != nil {
				return failure(err)
			}
			state.env[item.name] = value
			result = r.execNode(ctx, state, item.body, ioStreams)
			switch result.kind {
			case flowBreak:
				if result.levels > 1 {
					result.levels--
					return result
				}
				return normal(result.status)
			case flowContinue:
				if result.levels > 1 {
					result.levels--
					return result
				}
				result = normal(0)
				continue
			case flowReturn, flowExit:
				return result
			}
			if result.err != nil {
				return result
			}
		}
		return result
	case *groupNode:
		if item.subshell {
			return r.execNode(ctx, state.clone(), item.body, ioStreams)
		}
		return r.execNode(ctx, state, item.body, ioStreams)
	case *functionNode:
		state.functions[item.name] = item.body
		return normal(0)
	default:
		return failure(fmt.Errorf("unsupported shell AST node %T", current))
	}
}

func (r *Runner) execPipeline(ctx context.Context, state *shellState, pipeline *pipelineNode, ioStreams streams) flowResult {
	if len(pipeline.commands) == 1 {
		result := r.execNode(ctx, state, pipeline.commands[0], ioStreams)
		if pipeline.negated && result.err == nil && result.kind == flowNone {
			if result.status == 0 {
				result.status = 1
			} else {
				result.status = 0
			}
		}
		return result
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	readers := make([]*io.PipeReader, len(pipeline.commands)-1)
	writers := make([]*io.PipeWriter, len(pipeline.commands)-1)
	for i := range readers {
		readers[i], writers[i] = io.Pipe()
	}
	results := make([]flowResult, len(pipeline.commands))
	var wg sync.WaitGroup
	for index, command := range pipeline.commands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stageStreams := ioStreams
			if index > 0 {
				stageStreams.in = readers[index-1]
				defer readers[index-1].Close()
			}
			if index < len(pipeline.commands)-1 {
				stageStreams.out = writers[index]
				defer writers[index].Close()
			}
			results[index] = r.execNode(ctx, state.clone(), command, stageStreams)
			if results[index].err != nil {
				cancel()
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		for _, result := range results {
			if result.err != nil && !errors.Is(result.err, context.Canceled) {
				return result
			}
		}
		return failure(ctx.Err())
	}
	for _, result := range results {
		if result.err != nil || result.kind != flowNone {
			return result
		}
	}
	status := results[len(results)-1].status
	if state.options.pipefail {
		for i := len(results) - 1; i >= 0; i-- {
			if results[i].status != 0 {
				status = results[i].status
				break
			}
		}
	}
	if pipeline.negated {
		if status == 0 {
			status = 1
		} else {
			status = 0
		}
	}
	return normal(status)
}

func (r *Runner) execSimple(ctx context.Context, state *shellState, command *simpleNode, ioStreams streams) flowResult {
	assignments := make(map[string]string)
	wordIndex := 0
	for wordIndex < len(command.words) {
		name, valueWord, ok := splitAssignment(command.words[wordIndex])
		if !ok {
			break
		}
		value, err := r.expandWord(ctx, state, valueWord, false, false, ioStreams)
		if err != nil {
			return failure(err)
		}
		assignments[name] = first(value)
		wordIndex++
	}
	args, err := r.expandWords(ctx, state, command.words[wordIndex:], ioStreams)
	if err != nil {
		return failure(err)
	}
	redirected, closers, err := r.applyRedirects(ctx, state, command.redirs, ioStreams)
	if err != nil {
		return failure(err)
	}
	defer closeAll(closers)
	if len(args) == 0 {
		for name, value := range assignments {
			state.env[name] = value
		}
		return normal(0)
	}

	if function, ok := state.functions[args[0]]; ok {
		restore := overlayAssignments(state, assignments)
		defer restore()
		previous := state.position
		state.position = append([]string(nil), args[1:]...)
		result := r.execNode(ctx, state, function, redirected)
		state.position = previous
		if result.kind == flowReturn {
			result.kind = flowNone
		}
		return result
	}
	if builtin := builtins[args[0]]; builtin != nil {
		restore := overlayAssignments(state, assignments)
		defer restore()
		return builtin(ctx, r, state, args[1:], redirected)
	}

	commandState := state.clone()
	for name, value := range assignments {
		commandState.env[name] = value
		commandState.exported[name] = true
	}
	request := Command{
		Args:   append([]string(nil), args...),
		Dir:    commandState.dir,
		Env:    commandState.environment(),
		Stdin:  redirected.in,
		Stdout: redirected.out,
		Stderr: redirected.err,
	}
	if r.cfg.Handler != nil {
		handled, handlerErr := r.cfg.Handler(ctx, request)
		if handled {
			return failure(handlerErr)
		}
		if handlerErr != nil {
			return failure(handlerErr)
		}
	}
	return failure(runExternal(ctx, request))
}

func (r *Runner) applyRedirects(ctx context.Context, state *shellState, redirs []redirect, original streams) (streams, []io.Closer, error) {
	result := original
	var closers []io.Closer
	for _, redir := range redirs {
		fields, err := r.expandWord(ctx, state, redir.target, false, false, result)
		if err != nil {
			closeAll(closers)
			return streams{}, nil, err
		}
		if len(fields) != 1 {
			closeAll(closers)
			return streams{}, nil, errors.New("redirection target must expand to exactly one path or descriptor")
		}
		target := fields[0]
		if strings.HasSuffix(redir.op, "&") {
			if err := duplicateDescriptor(&result, redir.fd, target); err != nil {
				closeAll(closers)
				return streams{}, nil, err
			}
			continue
		}
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(state.dir, path)
		}
		var file *os.File
		switch redir.op {
		case ">", ">>":
			flags := os.O_CREATE | os.O_WRONLY
			if redir.op == ">>" {
				flags |= os.O_APPEND
			} else {
				flags |= os.O_TRUNC
			}
			file, err = os.OpenFile(path, flags, 0o666)
			if err == nil {
				if redir.fd == 1 {
					result.out = file
				} else if redir.fd == 2 {
					result.err = file
				} else {
					err = fmt.Errorf("output descriptor %d is not supported", redir.fd)
				}
			}
		case "<":
			if redir.fd != 0 {
				err = fmt.Errorf("input descriptor %d is not supported", redir.fd)
				break
			}
			file, err = os.Open(path)
			if err == nil {
				result.in = file
			}
		default:
			err = fmt.Errorf("unsupported redirection %s", redir.op)
		}
		if err != nil {
			if file != nil {
				_ = file.Close()
			}
			closeAll(closers)
			return streams{}, nil, fmt.Errorf("redirection %s %s: %w", redir.op, target, err)
		}
		closers = append(closers, file)
	}
	return result, closers, nil
}

func duplicateDescriptor(target *streams, fd int, source string) error {
	if source == "-" {
		if fd == 0 {
			target.in = strings.NewReader("")
		} else if fd == 1 {
			target.out = io.Discard
		} else if fd == 2 {
			target.err = io.Discard
		} else {
			return fmt.Errorf("descriptor %d is not supported", fd)
		}
		return nil
	}
	sourceFD, err := strconv.Atoi(source)
	if err != nil {
		return fmt.Errorf("invalid descriptor %q", source)
	}
	switch fd {
	case 0:
		if sourceFD != 0 {
			return fmt.Errorf("input can only duplicate descriptor 0")
		}
	case 1:
		if sourceFD == 2 {
			target.out = target.err
		} else if sourceFD != 1 {
			return fmt.Errorf("cannot duplicate descriptor %d to stdout", sourceFD)
		}
	case 2:
		if sourceFD == 1 {
			target.err = target.out
		} else if sourceFD != 2 {
			return fmt.Errorf("cannot duplicate descriptor %d to stderr", sourceFD)
		}
	default:
		return fmt.Errorf("descriptor %d is not supported", fd)
	}
	return nil
}

func overlayAssignments(state *shellState, assignments map[string]string) func() {
	type previous struct {
		value    string
		set      bool
		exported bool
	}
	old := make(map[string]previous, len(assignments))
	for name, value := range assignments {
		old[name] = previous{value: state.env[name], set: hasKey(state.env, name), exported: state.exported[name]}
		state.env[name] = value
		state.exported[name] = true
	}
	return func() {
		for name, value := range old {
			if value.set {
				state.env[name] = value.value
			} else {
				delete(state.env, name)
			}
			if value.exported {
				state.exported[name] = true
			} else {
				delete(state.exported, name)
			}
		}
	}
}

func hasKey(values map[string]string, key string) bool {
	_, ok := values[key]
	return ok
}

func closeAll(closers []io.Closer) {
	for i := len(closers) - 1; i >= 0; i-- {
		_ = closers[i].Close()
	}
}
