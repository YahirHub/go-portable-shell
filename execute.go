package portablesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

type streams struct {
	in          io.Reader
	out         io.Writer
	err         io.Writer
	descriptors map[int]Descriptor
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
	status    int
	kind      flowKind
	levels    int
	err       error
	statusErr error
}

func normal(status int) flowResult { return flowResult{status: normalizeStatus(status)} }

func failure(err error) flowResult {
	if err == nil {
		return normal(0)
	}
	if status, ok := Status(err); ok {
		return flowResult{status: normalizeStatus(status), statusErr: err}
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
			if state.options.errexit && result.status != 0 {
				result.kind = flowExit
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
			condition := r.execCondition(ctx, state, branch.condition, ioStreams)
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
			condition := r.execCondition(ctx, state, item.condition, ioStreams)
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
			if err := r.consumeLoop(state); err != nil {
				return failure(err)
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
			if err := r.consumeLoop(state); err != nil {
				return failure(err)
			}
			if err := assignVariable(state, item.name, value); err != nil {
				return failure(err)
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
		return result
	case *caseNode:
		values, err := r.expandWord(ctx, state, item.value, false, false, ioStreams)
		if err != nil {
			return failure(err)
		}
		value := first(values)
		for _, clause := range item.clauses {
			for _, patternWord := range clause.patterns {
				patterns, err := r.expandWord(ctx, state, patternWord, false, false, ioStreams)
				if err != nil {
					return failure(err)
				}
				if shellPatternMatch(first(patterns), value) {
					return r.execNode(ctx, state, clause.body, ioStreams)
				}
			}
		}
		return normal(0)
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

func (r *Runner) execCondition(ctx context.Context, state *shellState, current node, ioStreams streams) flowResult {
	errexit := state.options.errexit
	state.options.errexit = false
	result := r.execNode(ctx, state, current, ioStreams)
	state.options.errexit = errexit
	return result
}

func (r *Runner) runTrap(ctx context.Context, state *shellState, name string, ioStreams streams) flowResult {
	source := state.traps[name]
	if source == "" || state.inTrap {
		return normal(state.last)
	}
	program, err := Parse(source)
	if err != nil {
		return failure(err)
	}
	state.inTrap = true
	result := r.execNode(ctx, state, program.root, ioStreams)
	state.inTrap = false
	return result
}

func (r *Runner) execPipeline(ctx context.Context, state *shellState, pipeline *pipelineNode, ioStreams streams) (result flowResult) {
	if limitExceeded(int64(len(pipeline.commands)), int64(r.cfg.MaxPipelineCommands)) {
		return failure(r.limitError("pipeline_commands", int64(r.cfg.MaxPipelineCommands)))
	}
	r.emit(Event{Kind: EventPipelineStart, Dir: state.dir})
	defer func() {
		reportedErr := result.err
		if reportedErr == nil {
			reportedErr = result.statusErr
		}
		r.emit(Event{Kind: EventPipelineEnd, Dir: state.dir, Status: result.status, Err: reportedErr})
	}()
	if len(pipeline.commands) == 1 {
		result = r.execNode(ctx, state, pipeline.commands[0], ioStreams)
		if pipeline.negated && result.err == nil && result.kind == flowNone {
			if result.status == 0 {
				result.status = 1
			} else {
				result.status = 0
			}
			result.statusErr = nil
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
	selected := results[len(results)-1]
	if state.options.pipefail {
		for i := len(results) - 1; i >= 0; i-- {
			if results[i].status != 0 {
				selected = results[i]
				break
			}
		}
	}
	if pipeline.negated {
		if selected.status == 0 {
			selected.status = 1
		} else {
			selected.status = 0
		}
		selected.statusErr = nil
	}
	return selected
}

func (r *Runner) execSimple(ctx context.Context, state *shellState, command *simpleNode, ioStreams streams) (result flowResult) {
	if err := r.consumeCommand(state); err != nil {
		return failure(err)
	}
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
	for name := range assignments {
		if state.readonly[name] {
			return failure(fmt.Errorf("%s: readonly variable", name))
		}
	}
	var policyCommand Command
	if len(args) > 0 {
		r.emit(Event{Kind: EventCommandStart, Args: args, Dir: state.dir})
		defer func() {
			reportedErr := result.err
			if reportedErr == nil {
				reportedErr = result.statusErr
			}
			r.emit(Event{Kind: EventCommandFinish, Args: args, Dir: state.dir, Status: result.status, Err: reportedErr})
		}()
		policyCommand = commandRequest(state, assignments, args, ioStreams)
		if r.cfg.Policy != nil {
			if err := r.cfg.Policy.CheckCommand(ctx, policyCommand); err != nil {
				return failure(&PolicyDeniedError{Operation: "command " + args[0], Err: err})
			}
		}
	}
	redirected, closers, err := r.applyRedirects(ctx, state, command.redirs, ioStreams)
	if err != nil {
		return failure(err)
	}
	defer closeAll(closers)
	if len(args) == 0 {
		for name, value := range assignments {
			if err := assignVariable(state, name, value); err != nil {
				return failure(err)
			}
		}
		return normal(0)
	}
	if state.options.xtrace {
		fmt.Fprintln(redirected.err, "+ "+Join(args...))
	}
	state.lastArg = args[len(args)-1]
	policyCommand.Stdin = redirected.in
	policyCommand.Stdout = redirected.out
	policyCommand.Stderr = redirected.err
	policyCommand.Descriptors = copyDescriptors(redirected)

	if function, ok := state.functions[args[0]]; ok {
		if r.cfg.MaxFunctionDepth >= 0 && state.functionDepth >= r.cfg.MaxFunctionDepth {
			return failure(r.limitError("function_depth", int64(r.cfg.MaxFunctionDepth)))
		}
		restore := overlayAssignments(state, assignments)
		defer restore()
		previous := state.position
		state.functionDepth++
		state.locals = append(state.locals, make(localFrame))
		state.position = append([]string(nil), args[1:]...)
		result := r.execNode(ctx, state, function, redirected)
		restoreLocalFrame(state)
		state.functionDepth--
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

	request := policyCommand
	if cached := state.commandHash[args[0]]; cached != "" {
		request.Path = cached
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
	for _, handler := range r.cfg.Handlers {
		if handler == nil {
			continue
		}
		handled, handlerErr := handler(ctx, request)
		if handled {
			return failure(handlerErr)
		}
		if handlerErr != nil {
			return failure(handlerErr)
		}
	}
	if r.cfg.External == ExternalDisabled {
		fmt.Fprintf(redirected.err, "%s: external commands are disabled\n", args[0])
		return normal(127)
	}
	r.emit(Event{Kind: EventExternalStart, Args: args, Dir: state.dir, External: true})
	externalErr := runExternal(ctx, request)
	status, _ := Status(externalErr)
	r.emit(Event{Kind: EventExternalEnd, Args: args, Dir: state.dir, External: true, Status: status, Err: externalErr})
	return failure(externalErr)
}

func commandRequest(state *shellState, assignments map[string]string, args []string, ioStreams streams) Command {
	commandState := state.clone()
	for name, value := range assignments {
		commandState.env[name] = value
		commandState.exported[name] = true
	}
	return Command{
		Args: append([]string(nil), args...), Dir: commandState.dir,
		Env: commandState.environment(), Stdin: ioStreams.in,
		Stdout: ioStreams.out, Stderr: ioStreams.err,
		Descriptors: copyDescriptors(ioStreams),
	}
}

func (r *Runner) applyRedirects(ctx context.Context, state *shellState, redirs []redirect, original streams) (streams, []io.Closer, error) {
	result := cloneStreams(original)
	var closers []io.Closer
	for _, redir := range redirs {
		if redir.fd < 0 || redir.fd > 255 {
			closeAll(closers)
			return streams{}, nil, &RedirectionError{FD: redir.fd, Operator: redir.op, Err: errors.New("descriptor must be between 0 and 255")}
		}
		if redir.op == "<<" || redir.op == "<<-" {
			if !r.cfg.AllowHeredocs {
				closeAll(closers)
				return streams{}, nil, &UnsupportedFeatureError{Feature: "heredoc", Message: "enable Config.AllowHeredocs explicitly"}
			}
			inline := redir.inline
			if redir.expandInline {
				var err error
				inline, err = r.expandHeredoc(ctx, state, inline, result)
				if err != nil {
					closeAll(closers)
					return streams{}, nil, err
				}
			}
			if limitExceeded(int64(len(inline)), r.cfg.MaxHeredocBytes) {
				closeAll(closers)
				return streams{}, nil, r.limitError("heredoc_bytes", r.cfg.MaxHeredocBytes)
			}
			public := Redirection{FD: redir.fd, Operator: redir.op, Target: "<heredoc>", Inline: true}
			if err := r.checkRedirectionPolicy(ctx, public); err != nil {
				closeAll(closers)
				return streams{}, nil, err
			}
			result.descriptors[redir.fd] = Descriptor{Reader: strings.NewReader(inline)}
			result.syncStandardDescriptors()
			continue
		}
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
		if redir.op == "<<<" {
			public := Redirection{FD: redir.fd, Operator: redir.op, Target: target, Inline: true}
			if err := r.checkRedirectionPolicy(ctx, public); err != nil {
				closeAll(closers)
				return streams{}, nil, err
			}
			result.descriptors[redir.fd] = Descriptor{Reader: strings.NewReader(target + "\n")}
			result.syncStandardDescriptors()
			continue
		}
		if strings.HasSuffix(redir.op, "&") {
			if err := r.checkRedirectionPolicy(ctx, Redirection{FD: redir.fd, Operator: redir.op, Target: target}); err != nil {
				closeAll(closers)
				return streams{}, nil, err
			}
			if err := duplicateDescriptor(&result, redir.fd, target); err != nil {
				closeAll(closers)
				return streams{}, nil, err
			}
			continue
		}
		forCreate := redir.op == ">" || redir.op == ">>" || redir.op == ">|" || redir.op == "<>"
		path, err := r.resolvePath(state, target, forCreate)
		if err != nil {
			closeAll(closers)
			return streams{}, nil, &RedirectionError{FD: redir.fd, Operator: redir.op, Target: target, Err: err}
		}
		if err := r.checkRedirectionPolicy(ctx, Redirection{FD: redir.fd, Operator: redir.op, Target: target, Path: path}); err != nil {
			closeAll(closers)
			return streams{}, nil, err
		}
		release, acquireErr := r.acquireOpenFile(state)
		if acquireErr != nil {
			closeAll(closers)
			return streams{}, nil, acquireErr
		}
		var file File
		switch redir.op {
		case ">", ">>", ">|":
			flags := os.O_CREATE | os.O_WRONLY
			if redir.op == ">>" {
				flags |= os.O_APPEND
			} else {
				flags |= os.O_TRUNC
			}
			file, err = r.cfg.FileSystem.OpenFile(path, flags, 0o666&^state.umask)
			if err == nil {
				result.descriptors[redir.fd] = Descriptor{Writer: file}
				result.syncStandardDescriptors()
			}
		case "<":
			file, err = r.cfg.FileSystem.Open(path)
			if err == nil {
				result.descriptors[redir.fd] = Descriptor{Reader: file}
				result.syncStandardDescriptors()
			}
		case "<>":
			file, err = r.cfg.FileSystem.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666&^state.umask)
			if err == nil {
				result.descriptors[redir.fd] = Descriptor{Reader: file, Writer: file}
				result.syncStandardDescriptors()
			}
		default:
			err = fmt.Errorf("unsupported redirection %s", redir.op)
		}
		if err != nil {
			release()
			if file != nil {
				_ = file.Close()
			}
			closeAll(closers)
			return streams{}, nil, &RedirectionError{FD: redir.fd, Operator: redir.op, Target: target, Err: err}
		}
		closers = append(closers, releaseCloser{Closer: file, release: release})
	}
	return result, closers, nil
}

func (r *Runner) checkRedirectionPolicy(ctx context.Context, redirect Redirection) error {
	if r.cfg.Policy == nil {
		return nil
	}
	if err := r.cfg.Policy.CheckRedirection(ctx, redirect); err != nil {
		return &PolicyDeniedError{Operation: "redirection", Err: err}
	}
	return nil
}

type releaseCloser struct {
	io.Closer
	release func()
}

func (c releaseCloser) Close() error {
	err := c.Closer.Close()
	c.release()
	return err
}

func duplicateDescriptor(target *streams, fd int, source string) error {
	if target.descriptors == nil {
		*target = cloneStreams(*target)
	}
	if source == "-" {
		delete(target.descriptors, fd)
		target.syncStandardDescriptors()
		return nil
	}
	sourceFD, err := strconv.Atoi(source)
	if err != nil {
		return fmt.Errorf("invalid descriptor %q", source)
	}
	descriptor, ok := target.descriptors[sourceFD]
	if !ok {
		return fmt.Errorf("descriptor %d is closed", sourceFD)
	}
	target.descriptors[fd] = descriptor
	target.syncStandardDescriptors()
	return nil
}

func cloneStreams(source streams) streams {
	result := source
	result.descriptors = copyDescriptors(source)
	return result
}

func copyDescriptors(source streams) map[int]Descriptor {
	result := make(map[int]Descriptor, len(source.descriptors)+3)
	for fd, descriptor := range source.descriptors {
		result[fd] = descriptor
	}
	if source.in != nil {
		result[0] = Descriptor{Reader: source.in}
	}
	if source.out != nil {
		result[1] = Descriptor{Writer: source.out}
	}
	if source.err != nil {
		result[2] = Descriptor{Writer: source.err}
	}
	return result
}

func (s *streams) syncStandardDescriptors() {
	if descriptor, ok := s.descriptors[0]; ok && descriptor.Reader != nil {
		s.in = descriptor.Reader
	} else {
		s.in = strings.NewReader("")
	}
	if descriptor, ok := s.descriptors[1]; ok && descriptor.Writer != nil {
		s.out = descriptor.Writer
	} else {
		s.out = io.Discard
	}
	if descriptor, ok := s.descriptors[2]; ok && descriptor.Writer != nil {
		s.err = descriptor.Writer
	} else {
		s.err = io.Discard
	}
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
