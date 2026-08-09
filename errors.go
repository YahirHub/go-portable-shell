package portablesh

import "fmt"

// UnsupportedFeatureError reports a recognized feature disabled by Config.
type UnsupportedFeatureError struct {
	Feature string
	Message string
}

func (e *UnsupportedFeatureError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("unsupported shell feature %s: %s", e.Feature, e.Message)
	}
	return fmt.Sprintf("unsupported shell feature: %s", e.Feature)
}

// ResourceLimitError reports a configured execution limit.
type ResourceLimitError struct {
	Resource string
	Limit    int64
}

func (e *ResourceLimitError) Error() string {
	return fmt.Sprintf("portable shell %s limit exceeded (%d)", e.Resource, e.Limit)
}

// PolicyDeniedError wraps a denial returned by Policy.
type PolicyDeniedError struct {
	Operation string
	Err       error
}

func (e *PolicyDeniedError) Error() string {
	return fmt.Sprintf("portable shell policy denied %s: %v", e.Operation, e.Err)
}

func (e *PolicyDeniedError) Unwrap() error { return e.Err }

// CommandNotFoundError is both descriptive and compatible with exit status 127.
type CommandNotFoundError struct{ Name string }

func (e *CommandNotFoundError) Error() string { return fmt.Sprintf("%s: command not found", e.Name) }
func (e *CommandNotFoundError) Unwrap() error { return ExitStatus(127) }

// RedirectionError identifies a failed redirection.
type RedirectionError struct {
	FD       int
	Operator string
	Target   string
	Err      error
}

func (e *RedirectionError) Error() string {
	return fmt.Sprintf("redirection %d%s %s: %v", e.FD, e.Operator, e.Target, e.Err)
}

func (e *RedirectionError) Unwrap() error { return e.Err }

// ExpansionError identifies the expansion class that failed.
type ExpansionError struct {
	Kind string
	Err  error
}

func (e *ExpansionError) Error() string { return fmt.Sprintf("%s expansion: %v", e.Kind, e.Err) }
func (e *ExpansionError) Unwrap() error { return e.Err }

// StateError reports invalid public state operations.
type StateError struct{ Message string }

func (e *StateError) Error() string { return "portable shell state: " + e.Message }
