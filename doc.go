// Package portablesh provides a dependency-free interpreter for a bounded,
// non-interactive Bash/POSIX-compatible shell language.
//
// Parse creates reusable immutable programs, while Runner executes them with
// persistent shell state. Policies, handlers, observers, filesystem adapters,
// resource limits, snapshots, and explicit external-process controls support
// embedding in larger applications.
//
// The package is intended for portable automation, not as a sandbox or a
// replacement for a user's native shell. Its exact language contract is
// documented in COMPATIBILITY.md.
package portablesh
