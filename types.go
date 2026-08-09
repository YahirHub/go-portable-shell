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
	"sync"
	"time"
)

const (
	defaultSubstitutionLimit   int64 = 1 << 20
	defaultExpansionDepth            = 32
	defaultMaxScriptBytes      int64 = 1 << 20
	defaultMaxASTNodes               = 20_000
	defaultMaxCommands               = 1_000_000
	defaultMaxLoopIterations         = 1_000_000
	defaultMaxPipelineCommands       = 64
	defaultMaxGlobMatches            = 10_000
	defaultMaxOpenFiles              = 256
	defaultMaxFunctionDepth          = 128
	defaultMaxSourceDepth            = 32
	defaultMaxBraceExpansions        = 1_000
	defaultMaxHeredocBytes     int64 = 64 << 10
)

// ExternalMode controls operating-system executable resolution.
type ExternalMode uint8

const (
	// ExternalEnabled lets unresolved commands continue to the host OS.
	ExternalEnabled ExternalMode = iota
	// ExternalDisabled only permits functions, builtins and handled commands.
	ExternalDisabled
)

// Config defines the process-like environment used by a Runner.
type Config struct {
	// Name is exposed as $0. Empty selects "portablesh".
	Name string

	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// FileSystem handles shell-owned file reads and redirections. Nil uses the
	// host operating system.
	FileSystem FileSystem

	// Handler can implement application-owned commands. It is called after
	// functions and builtins but before external executable resolution.
	Handler CommandHandler
	// Handlers are tried in order after Handler.
	Handlers []CommandHandler

	// Policy authorizes expanded commands and redirections.
	Policy Policy
	// Observer receives synchronous structured execution events.
	Observer Observer
	// External controls host executable resolution. Zero enables it.
	External ExternalMode
	// RootDir lexically restricts cd, source and redirection paths. It is a
	// guardrail rather than a complete filesystem sandbox.
	RootDir string
	// SourceLoader overrides filesystem reads for source and dot builtins.
	SourceLoader func(context.Context, string) ([]byte, error)

	// PipeFail makes a pipeline return the rightmost non-zero status.
	PipeFail bool

	// MaxCommandSubstitutionBytes limits the captured stdout of each $(...).
	// Zero selects 1 MiB. A negative value disables command substitution.
	MaxCommandSubstitutionBytes int64

	// MaxExpansionDepth bounds nested substitutions and parameter operands.
	// Zero selects 32.
	MaxExpansionDepth int

	// AllowHeredocs enables << and <<-. They are disabled by default to retain
	// the fail-closed v0.1 behavior. Here strings (<<<) remain enabled.
	AllowHeredocs bool
	// MaxHeredocBytes limits one heredoc. Zero selects 64 KiB.
	MaxHeredocBytes int64

	// The remaining zero-valued limits select conservative defaults. Negative
	// values disable the respective limit.
	MaxScriptBytes      int64
	MaxASTNodes         int
	MaxCommands         int
	MaxLoopIterations   int
	MaxPipelineCommands int
	MaxGlobMatches      int
	MaxOpenFiles        int
	MaxFunctionDepth    int
	MaxSourceDepth      int
	MaxBraceExpansions  int
	// MaxOutputBytes limits each top-level stdout and stderr stream. Zero or a
	// negative value leaves top-level output unlimited.
	MaxOutputBytes int64
}

// Command describes an expanded command passed to a CommandHandler.
type Command struct {
	Args []string
	// Path optionally pins the resolved external executable.
	Path   string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Descriptors exposes the virtual descriptor table to handlers. Host
	// executables receive descriptors above 2 on Unix when backed by *os.File.
	Descriptors map[int]Descriptor
}

// Descriptor is one virtual shell file descriptor.
type Descriptor struct {
	Reader io.Reader
	Writer io.Writer
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
	cfg        Config
	state      *shellState
	observerMu *sync.Mutex
}

type shellState struct {
	dir           string
	env           map[string]string
	exported      map[string]bool
	functions     map[string]node
	position      []string
	last          int
	options       shellOptions
	depth         int
	readonly      map[string]bool
	locals        []localFrame
	traps         map[string]string
	commandHash   map[string]string
	functionDepth int
	sourceDepth   int
	getoptsIndex  int
	getoptsOffset int
	umask         os.FileMode
	budget        *executionBudget
	started       time.Time
	inTrap        bool
	lastArg       string
}

type shellOptions struct {
	nounset  bool
	pipefail bool
	errexit  bool
	xtrace   bool
	noglob   bool
}

type localValue struct {
	value    string
	set      bool
	exported bool
	readonly bool
}

type localFrame map[string]localValue

// New creates a reusable shell runner.
func New(cfg Config) (*Runner, error) {
	if cfg.FileSystem == nil {
		cfg.FileSystem = OSFileSystem{}
	}
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
	info, err := cfg.FileSystem.Stat(absDir)
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
	if cfg.Name == "" {
		cfg.Name = "portablesh"
	}
	cfg.MaxHeredocBytes = defaultInt64Limit(cfg.MaxHeredocBytes, defaultMaxHeredocBytes)
	cfg.MaxScriptBytes = defaultInt64Limit(cfg.MaxScriptBytes, defaultMaxScriptBytes)
	cfg.MaxASTNodes = defaultIntLimit(cfg.MaxASTNodes, defaultMaxASTNodes)
	cfg.MaxCommands = defaultIntLimit(cfg.MaxCommands, defaultMaxCommands)
	cfg.MaxLoopIterations = defaultIntLimit(cfg.MaxLoopIterations, defaultMaxLoopIterations)
	cfg.MaxPipelineCommands = defaultIntLimit(cfg.MaxPipelineCommands, defaultMaxPipelineCommands)
	cfg.MaxGlobMatches = defaultIntLimit(cfg.MaxGlobMatches, defaultMaxGlobMatches)
	cfg.MaxOpenFiles = defaultIntLimit(cfg.MaxOpenFiles, defaultMaxOpenFiles)
	cfg.MaxFunctionDepth = defaultIntLimit(cfg.MaxFunctionDepth, defaultMaxFunctionDepth)
	cfg.MaxSourceDepth = defaultIntLimit(cfg.MaxSourceDepth, defaultMaxSourceDepth)
	cfg.MaxBraceExpansions = defaultIntLimit(cfg.MaxBraceExpansions, defaultMaxBraceExpansions)
	if cfg.External != ExternalEnabled && cfg.External != ExternalDisabled {
		return nil, errors.New("invalid External mode")
	}
	if cfg.RootDir != "" {
		root, err := filepath.Abs(cfg.RootDir)
		if err != nil {
			return nil, fmt.Errorf("portable shell root directory: %w", err)
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("portable shell root directory is not a directory: %s", root)
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("portable shell root directory: %w", err)
		}
		resolvedDir, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			return nil, fmt.Errorf("portable shell directory: %w", err)
		}
		cfg.RootDir = root
		if !withinRoot(root, resolvedDir) {
			return nil, fmt.Errorf("portable shell directory %s is outside root %s", absDir, root)
		}
		absDir = resolvedDir
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
	if _, ok := env["OPTIND"]; !ok {
		env["OPTIND"] = "1"
	}
	return &Runner{
		cfg:        cfg,
		observerMu: &sync.Mutex{},
		state: &shellState{
			dir:          absDir,
			env:          env,
			exported:     exported,
			functions:    make(map[string]node),
			readonly:     make(map[string]bool),
			traps:        make(map[string]string),
			commandHash:  make(map[string]string),
			options:      shellOptions{pipefail: cfg.PipeFail},
			getoptsIndex: 1,
			umask:        0o022,
		},
	}, nil
}

func defaultIntLimit(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func defaultInt64Limit(value, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
}

// Run parses and executes script.
func (r *Runner) Run(ctx context.Context, script string) error {
	if limitExceeded(int64(len(script)), r.cfg.MaxScriptBytes) {
		return r.limitError("script_bytes", r.cfg.MaxScriptBytes)
	}
	program, err := Parse(script)
	if err != nil {
		return err
	}
	return r.RunProgram(ctx, program)
}

// RunProgram executes a previously parsed program.
func (r *Runner) RunProgram(ctx context.Context, program *Program) error {
	if program == nil || program.root == nil {
		return &StateError{Message: "program is nil"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limitExceeded(int64(len(program.source)), r.cfg.MaxScriptBytes) {
		return r.limitError("script_bytes", r.cfg.MaxScriptBytes)
	}
	if limitExceeded(int64(program.report.Nodes), int64(r.cfg.MaxASTNodes)) {
		return r.limitError("ast_nodes", int64(r.cfg.MaxASTNodes))
	}
	if limitExceeded(int64(program.report.MaxPipelineWidth), int64(r.cfg.MaxPipelineCommands)) {
		return r.limitError("pipeline_commands", int64(r.cfg.MaxPipelineCommands))
	}

	budget := newExecutionBudget()
	previousBudget := r.state.budget
	r.state.budget = budget
	r.state.started = time.Now()
	defer func() { r.state.budget = previousBudget }()

	stdout := newOutputLimiter(r.cfg.Stdout, r.cfg.MaxOutputBytes, "stdout_bytes")
	stderr := newOutputLimiter(r.cfg.Stderr, r.cfg.MaxOutputBytes, "stderr_bytes")
	runStreams := streams{in: r.cfg.Stdin, out: stdout, err: stderr}
	flow := r.execNode(ctx, r.state, program.root, runStreams)
	trapContext := context.WithoutCancel(ctx)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		_ = r.runTrap(trapContext, r.state, "TERM", runStreams)
	} else if errors.Is(ctx.Err(), context.Canceled) {
		_ = r.runTrap(trapContext, r.state, "INT", runStreams)
	}
	if trapResult := r.runTrap(trapContext, r.state, "EXIT", runStreams); trapResult.err != nil {
		flow = trapResult
	} else if trapResult.kind == flowExit {
		flow = trapResult
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := stdout.Err(); err != nil {
		return r.observeLimit(err)
	}
	if err := stderr.Err(); err != nil {
		return r.observeLimit(err)
	}
	return r.finishFlow(flow)
}

func (r *Runner) finishFlow(flow flowResult) error {
	if flow.kind == flowExit {
		r.state.last = normalizeStatus(flow.status)
		if flow.status != 0 {
			if flow.statusErr != nil {
				return flow.statusErr
			}
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
		if flow.statusErr != nil {
			return flow.statusErr
		}
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
	readonly := make(map[string]bool, len(s.readonly))
	for key, value := range s.readonly {
		readonly[key] = value
	}
	traps := make(map[string]string, len(s.traps))
	for key, value := range s.traps {
		traps[key] = value
	}
	commandHash := make(map[string]string, len(s.commandHash))
	for key, value := range s.commandHash {
		commandHash[key] = value
	}
	locals := make([]localFrame, len(s.locals))
	for index, frame := range s.locals {
		locals[index] = make(localFrame, len(frame))
		for key, value := range frame {
			locals[index][key] = value
		}
	}
	return &shellState{
		dir:           s.dir,
		env:           env,
		exported:      exported,
		functions:     functions,
		readonly:      readonly,
		locals:        locals,
		traps:         traps,
		commandHash:   commandHash,
		position:      append([]string(nil), s.position...),
		last:          s.last,
		options:       s.options,
		depth:         s.depth,
		functionDepth: s.functionDepth,
		sourceDepth:   s.sourceDepth,
		getoptsIndex:  s.getoptsIndex,
		getoptsOffset: s.getoptsOffset,
		umask:         s.umask,
		budget:        s.budget,
		started:       s.started,
		inTrap:        s.inTrap,
		lastArg:       s.lastArg,
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
