package portablesh

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type executionBudget struct {
	commands     atomic.Int64
	loops        atomic.Int64
	openFiles    atomic.Int64
	braceResults atomic.Int64
}

func newExecutionBudget() *executionBudget { return &executionBudget{} }

func limitExceeded(value, limit int64) bool { return limit >= 0 && value > limit }

func consumeLimit(counter *atomic.Int64, limit int, resource string) error {
	value := counter.Add(1)
	if limit >= 0 && value > int64(limit) {
		return &ResourceLimitError{Resource: resource, Limit: int64(limit)}
	}
	return nil
}

func (r *Runner) consumeCommand(state *shellState) error {
	if state.budget == nil {
		return nil
	}
	if err := consumeLimit(&state.budget.commands, r.cfg.MaxCommands, "commands"); err != nil {
		return r.observeLimit(err)
	}
	return nil
}

func (r *Runner) consumeLoop(state *shellState) error {
	if state.budget == nil {
		return nil
	}
	if err := consumeLimit(&state.budget.loops, r.cfg.MaxLoopIterations, "loop_iterations"); err != nil {
		return r.observeLimit(err)
	}
	return nil
}

func (r *Runner) acquireOpenFile(state *shellState) (func(), error) {
	if state.budget == nil {
		return func() {}, nil
	}
	value := state.budget.openFiles.Add(1)
	if r.cfg.MaxOpenFiles >= 0 && value > int64(r.cfg.MaxOpenFiles) {
		state.budget.openFiles.Add(-1)
		return nil, r.observeLimit(&ResourceLimitError{Resource: "open_files", Limit: int64(r.cfg.MaxOpenFiles)})
	}
	return func() { state.budget.openFiles.Add(-1) }, nil
}

func (r *Runner) limitError(resource string, limit int64) error {
	return r.observeLimit(&ResourceLimitError{Resource: resource, Limit: limit})
}

func (r *Runner) observeLimit(err error) error {
	r.emit(Event{Kind: EventLimitReached, Err: err})
	return err
}

func (r *Runner) emit(event Event) {
	if r.cfg.Observer == nil {
		return
	}
	event.Time = time.Now()
	event.Args = append([]string(nil), event.Args...)
	r.observerMu.Lock()
	defer r.observerMu.Unlock()
	r.cfg.Observer(event)
}

type outputLimiter struct {
	mu       sync.Mutex
	w        io.Writer
	limit    int64
	written  int64
	resource string
	exceeded bool
}

func newOutputLimiter(w io.Writer, limit int64, resource string) *outputLimiter {
	return &outputLimiter{w: w, limit: limit, resource: resource}
}

func (w *outputLimiter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.limit <= 0 {
		return w.w.Write(data)
	}
	remaining := w.limit - w.written
	if remaining <= 0 {
		w.exceeded = w.exceeded || len(data) > 0
		return len(data), nil
	}
	write := data
	if int64(len(write)) > remaining {
		write = write[:remaining]
		w.exceeded = true
	}
	n, err := w.w.Write(write)
	w.written += int64(n)
	if err != nil {
		return n, err
	}
	return len(data), nil
}

func (w *outputLimiter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.exceeded {
		return nil
	}
	return &ResourceLimitError{Resource: w.resource, Limit: w.limit}
}
