package portablesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultSubstitutionLimit int64 = 1 << 20
	defaultExpansionDepth          = 32
)

// Config defines the process-like environment used by a Runner.
type Config struct {
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Handler can implement application-owned commands. It is called after
	// functions and builtins but before external executable resolution.
	Handler CommandHandler

	// PipeFail makes a pipeline return the rightmost non-zero status.
	PipeFail bool

	// MaxCommandSubstitutionBytes limits the captured stdout of each $(...).
	// Zero selects 1 MiB. A negative value disables command substitution.
	MaxCommandSubstitutionBytes int64

	// MaxExpansionDepth bounds nested substitutions and parameter operands.
	// Zero selects 32.
	MaxExpansionDepth int
}

// Command describes an expanded command passed to a CommandHandler.
type Command struct {
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// CommandHandler implements an application-owned command. Returning false
// delegates the command to normal external executable resolution.
type CommandHandler func(context.Context, Command) (handled bool, err error)

// ExitStatus represents a normal shell command failure.
type ExitStatus int

func (s ExitStatus) Error() string { return fmt.Sprintf("exit status %d", int(s)) }

// Status returns the conventional process status represented by err.
func Status(err error) (int, bool) {
	if err == nil {
		return 0, true
	}
	var status ExitStatus
	if errors.As(err, &status) {
		return normalizeStatus(int(status)), true
	}
	return 0, false
}

// SyntaxError reports a parser or lexer failure with a source position.
type SyntaxError struct {
	Line    int
	Column  int
	Message string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("shell syntax error at %d:%d: %s", e.Line, e.Column, e.Message)
}

// Runner parses and executes scripts. A Runner keeps directory, environment,
// functions and positional parameters between sequential Run calls. It is not
// safe for concurrent use.
type Runner struct {
	cfg   Config
	state *shellState
}

type shellState struct {
	dir       string
	env       map[string]string
	exported  map[string]bool
	functions map[string]node
	position  []string
	last      int
	options   shellOptions
	depth     int
}

type shellOptions struct {
	nounset  bool
	pipefail bool
}

// New creates a reusable shell runner.
func New(cfg Config) (*Runner, error) {
	dir := cfg.Dir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("portable shell directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("portable shell directory is not a directory: %s", absDir)
	}
	if cfg.Stdin == nil {
		cfg.Stdin = strings.NewReader("")
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if cfg.MaxCommandSubstitutionBytes == 0 {
		cfg.MaxCommandSubstitutionBytes = defaultSubstitutionLimit
	}
	if cfg.MaxExpansionDepth == 0 {
		cfg.MaxExpansionDepth = defaultExpansionDepth
	}
	if cfg.MaxExpansionDepth < 1 {
		return nil, errors.New("MaxExpansionDepth must be positive")
	}

	env := make(map[string]string, len(cfg.Env)+2)
	exported := make(map[string]bool, len(cfg.Env)+2)
	for _, entry := range cfg.Env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		env[name] = value
		exported[name] = true
	}
	if _, ok := env["PWD"]; !ok {
		env["PWD"] = absDir
		exported["PWD"] = true
	}
	return &Runner{
		cfg: cfg,
		state: &shellState{
			dir:       absDir,
			env:       env,
			exported:  exported,
			functions: make(map[string]node),
			options:   shellOptions{pipefail: cfg.PipeFail},
		},
	}, nil
}

// Run parses and executes script.
func (r *Runner) Run(ctx context.Context, script string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	program, err := parse(script)
	if err != nil {
		return err
	}
	flow := r.execNode(ctx, r.state, program, streams{in: r.cfg.Stdin, out: r.cfg.Stdout, err: r.cfg.Stderr})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if flow.kind == flowExit {
		r.state.last = normalizeStatus(flow.status)
		if flow.status != 0 {
			return ExitStatus(normalizeStatus(flow.status))
		}
		return nil
	}
	if flow.kind != flowNone {
		return &SyntaxError{Line: 1, Column: 1, Message: "control-flow command used outside its valid scope"}
	}
	r.state.last = normalizeStatus(flow.status)
	if flow.err != nil {
		return flow.err
	}
	if flow.status != 0 {
		return ExitStatus(normalizeStatus(flow.status))
	}
	return nil
}

func (s *shellState) clone() *shellState {
	env := make(map[string]string, len(s.env))
	for k, v := range s.env {
		env[k] = v
	}
	exported := make(map[string]bool, len(s.exported))
	for k, v := range s.exported {
		exported[k] = v
	}
	functions := make(map[string]node, len(s.functions))
	for k, v := range s.functions {
		functions[k] = v
	}
	return &shellState{
		dir:       s.dir,
		env:       env,
		exported:  exported,
		functions: functions,
		position:  append([]string(nil), s.position...),
		last:      s.last,
		options:   s.options,
		depth:     s.depth,
	}
}

func (s *shellState) environment() []string {
	result := make([]string, 0, len(s.exported))
	for name := range s.exported {
		if value, ok := s.env[name]; ok {
			result = append(result, name+"="+value)
		}
	}
	sort.Strings(result)
	return result
}

func normalizeStatus(status int) int {
	if status < 0 {
		return 1
	}
	if status > 255 {
		return status & 0xff
	}
	return status
}
