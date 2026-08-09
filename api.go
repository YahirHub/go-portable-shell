package portablesh

import (
	"context"
	"io"
	"sort"
	"strings"
	"time"
)

// Program is an immutable parsed shell program. Programs may be reused by
// multiple independent runners.
type Program struct {
	source string
	root   node
	report Report
}

// Source returns the exact source used to parse the program.
func (p *Program) Source() string {
	if p == nil {
		return ""
	}
	return p.source
}

// Report describes the syntax contained in a parsed program.
type Report struct {
	Nodes            int
	Commands         int
	Pipelines        int
	MaxPipelineWidth int
	Features         []string
}

// Check returns a copy of the program's static feature report.
func Check(program *Program) Report {
	if program == nil {
		return Report{}
	}
	report := program.report
	report.Features = append([]string(nil), report.Features...)
	return report
}

// Redirection describes a fully expanded redirection before it is applied.
type Redirection struct {
	FD       int
	Operator string
	Target   string
	Path     string
	Inline   bool
}

// Policy can authorize commands and redirections. It is a guardrail hook, not
// a sandbox: commands may still access resources through operating-system APIs.
type Policy interface {
	CheckCommand(context.Context, Command) error
	CheckRedirection(context.Context, Redirection) error
}

// PolicyFuncs adapts optional functions to Policy.
type PolicyFuncs struct {
	Command     func(context.Context, Command) error
	Redirection func(context.Context, Redirection) error
}

func (p PolicyFuncs) CheckCommand(ctx context.Context, command Command) error {
	if p.Command == nil {
		return nil
	}
	return p.Command(ctx, command)
}

func (p PolicyFuncs) CheckRedirection(ctx context.Context, redirect Redirection) error {
	if p.Redirection == nil {
		return nil
	}
	return p.Redirection(ctx, redirect)
}

// EventKind identifies an execution event.
type EventKind string

const (
	EventCommandStart  EventKind = "command_start"
	EventCommandFinish EventKind = "command_finish"
	EventPipelineStart EventKind = "pipeline_start"
	EventPipelineEnd   EventKind = "pipeline_finish"
	EventExternalStart EventKind = "external_start"
	EventExternalEnd   EventKind = "external_finish"
	EventLimitReached  EventKind = "limit_reached"
)

// Event is emitted synchronously. Observer callbacks are serialized for a
// runner and its clones. Observers must return quickly and must not call methods
// on the same Runner.
type Event struct {
	Kind     EventKind
	Time     time.Time
	Args     []string
	Dir      string
	Status   int
	External bool
	Err      error
}

// Observer receives optional structured execution events.
type Observer func(Event)

// Snapshot is an immutable copy of runner state.
type Snapshot struct{ state *shellState }

// Snapshot returns a restorable copy of the current runner state.
func (r *Runner) Snapshot() Snapshot {
	state := r.state.clone()
	state.budget = nil
	return Snapshot{state: state}
}

// Restore replaces the runner state with a previous snapshot.
func (r *Runner) Restore(snapshot Snapshot) error {
	if snapshot.state == nil {
		return &StateError{Message: "snapshot is empty"}
	}
	if r.cfg.RootDir != "" {
		if _, err := r.resolvePath(snapshot.state, snapshot.state.dir, false); err != nil {
			return &StateError{Message: "snapshot directory is outside configured root"}
		}
	}
	r.state = snapshot.state.clone()
	r.state.budget = nil
	return nil
}

// Clone returns an independent runner with the same configuration and state.
func (r *Runner) Clone() *Runner {
	clone := &Runner{cfg: r.cfg, state: r.state.clone(), observerMu: r.observerMu}
	clone.state.budget = nil
	return clone
}

// Quote returns one POSIX-shell-safe word.
func Quote(value string) string {
	if value == "" {
		return "''"
	}
	safe := true
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Join quotes and joins arguments into a shell command line.
func Join(args ...string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func inspectProgram(root node) Report {
	features := make(map[string]bool)
	report := Report{}
	var inspectWord func(word)
	inspectWord = func(value word) {
		for _, part := range value.parts {
			switch part.kind {
			case partParameter:
				features["parameter-expansion"] = true
			case partCommand:
				features["command-substitution"] = true
			case partArithmetic:
				features["arithmetic"] = true
			}
		}
	}
	var walk func(node)
	walk = func(current node) {
		if current == nil {
			return
		}
		report.Nodes++
		switch item := current.(type) {
		case *listNode:
			for _, child := range item.items {
				walk(child)
			}
		case *andOrNode:
			features["and-or"] = true
			walk(item.first)
			for _, part := range item.rest {
				walk(part.node)
			}
		case *pipelineNode:
			features["pipeline"] = true
			report.Pipelines++
			if len(item.commands) > report.MaxPipelineWidth {
				report.MaxPipelineWidth = len(item.commands)
			}
			for _, child := range item.commands {
				walk(child)
			}
		case *simpleNode:
			report.Commands++
			for _, value := range item.words {
				inspectWord(value)
			}
			for _, redirect := range item.redirs {
				features["redirection"] = true
				if redirect.op == "<<" || redirect.op == "<<-" {
					features["heredoc"] = true
				}
				if redirect.op == "<<<" {
					features["here-string"] = true
				}
				inspectWord(redirect.target)
			}
		case *ifNode:
			features["if"] = true
			for _, branch := range item.branches {
				walk(branch.condition)
				walk(branch.body)
			}
			walk(item.other)
		case *whileNode:
			features["loop"] = true
			walk(item.condition)
			walk(item.body)
		case *forNode:
			features["loop"] = true
			for _, value := range item.words {
				inspectWord(value)
			}
			walk(item.body)
		case *caseNode:
			features["case"] = true
			inspectWord(item.value)
			for _, clause := range item.clauses {
				for _, pattern := range clause.patterns {
					inspectWord(pattern)
				}
				walk(clause.body)
			}
		case *groupNode:
			features["group"] = true
			walk(item.body)
		case *functionNode:
			features["function"] = true
			walk(item.body)
		}
	}
	walk(root)
	for feature := range features {
		report.Features = append(report.Features, feature)
	}
	sort.Strings(report.Features)
	return report
}

// discardReadWriter is useful for closed virtual descriptors.
type discardReadWriter struct{}

func (discardReadWriter) Read([]byte) (int, error)    { return 0, io.EOF }
func (discardReadWriter) Write(p []byte) (int, error) { return len(p), nil }
