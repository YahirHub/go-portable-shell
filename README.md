# go-portable-shell

`go-portable-shell` is a dependency-free interpreter for a bounded,
non-interactive shell language. It is written in pure Go for applications that
need predictable automation on Windows, Linux, macOS, or Android without
shipping Bash.

The project deliberately implements a documented Bash/POSIX-compatible subset.
It does not claim to be a complete POSIX shell, and syntax outside its contract
fails explicitly.

## Highlights

- UTF-8-safe quotes and literal words, variables, positional parameters,
  arithmetic, command substitution, brace expansion, globbing, tilde expansion,
  and IFS field splitting;
- lists, `&&`, `||`, `!`, pipelines, `if`, `case`, `while`, `until`, `for`,
  groups, subshells, and functions;
- here strings, opt-in bounded heredocs, file redirections, and virtual file
  descriptors from 0 through 255;
- useful non-interactive builtins including `source`, `local`, `readonly`,
  `trap`, `getopts`, `umask`, `exec`, `hash`, and `times`;
- portable Linux-style utilities implemented in Go (`ls`, `mkdir`, `rm`, `cp`,
  `mv`, `touch`, `chmod`, `cat`, `head`, `tail`, `wc`, `find`, `grep`, and
  others), so Windows does not need Git Bash for common automation;
- host fallback translation for selected commands when their native spelling is
  unavailable, including bounded `apt`/`apt-get` mappings on Windows and macOS;
- a `portablesh -c` executable suitable for CLI tools that need one predictable
  non-interactive default shell on every supported operating system;
- reusable parsed `Program` values, state snapshots and independent clones;
- ordered in-process command handlers, authorization policies, structured
  execution events, and a replaceable shell filesystem;
- typed syntax, expansion, policy, resource, redirection, and command errors;
- configurable limits for input, AST size, execution, expansion, open files,
  recursion, and output;
- context cancellation with process-tree cleanup on Unix and Windows;
- no runtime dependencies and no CGO.

See [COMPATIBILITY.md](COMPATIBILITY.md) for the precise language contract and
[SECURITY.md](SECURITY.md) before evaluating untrusted input.

## Install

```sh
go get github.com/YahirHub/go-portable-shell@v0.3.1
```

The module requires Go 1.24 or newer.

To install the standalone shell adapter:

```sh
go install github.com/YahirHub/go-portable-shell/cmd/portablesh@v0.3.1
```

The executable is intentionally non-interactive. It supports `-c`, script files,
stdin, `--check`, and `--version`, making it suitable as the execution shell for
agent-style or Claude-style CLIs without probing for Git Bash first.

## Basic use

```go
runner, err := portablesh.New(portablesh.Config{
	Name:   "automation",
	Dir:    projectDir,
	Env:    os.Environ(),
	Stdin:  os.Stdin,
	Stdout: os.Stdout,
	Stderr: os.Stderr,
})
if err != nil {
	log.Fatal(err)
}

program, err := portablesh.Parse(`
	name=${NAME:-world}
	for item in {1..3}; do printf 'hello %s #%s\n' "$name" "$item"; done
`)
if err != nil {
	log.Fatal(err)
}
if err := runner.RunProgram(ctx, program); err != nil {
	log.Fatal(err)
}
```

`Program` values are immutable and may be reused by independent runners.
`Runner` retains variables, functions, positional parameters, and its working
directory between sequential calls. A runner is not safe for concurrent use;
use `Clone` or create another runner for concurrent sessions.

## Portable command toolbox

Common Linux-style commands are provided by the interpreter itself after
application handlers and before host executable lookup:

`ls` `mkdir` `rmdir` `rm` `cp` `mv` `touch` `chmod` `cat` `head` `tail` `wc`
`basename` `dirname` `which` `env` `printenv` `sleep` `uname` `whoami` `find`
`grep`

The implementations are deliberately bounded subsets, not GNU coreutils clones.
Frequently used forms such as `ls -la`, `mkdir -p`, `rm -rf`, `cp -R`,
`head -n`, `tail -n`, `find ... -name/-type/-maxdepth`, and
`grep -inr/-F/-E` are supported. Unsupported flags fail explicitly. `grep`
regexp mode uses Go RE2 syntax. An explicit executable path such as
`/usr/bin/grep` bypasses the portable utility when a host-specific implementation
is required.

Command composition is parsed by the shell, so chains are platform-independent:

```sh
mkdir -p build && cp -R src build/src; find build -type f -name '*.go'
```

On Windows, when the original executable cannot be found, selected host commands
have conservative fallbacks. For example, a single-package
`apt install jq` can become `winget install jq`, while `apt update` becomes
`winget source update`. On macOS the corresponding bounded `apt` fallback uses
Homebrew. Existing native commands always win over translation. Ambiguous forms
that cannot be mapped safely are left unresolved instead of being guessed.
Translated host commands are checked by `Policy` again using their actual
arguments before execution.

Resolution order for a simple command is: shell function, language builtin,
`Handler`, ordered `Handlers`, portable utility, then host executable. This keeps
application-owned CLI commands authoritative while still making the portable
shell independent of Git Bash for its basic toolbox.

## CLI shell adapter

```sh
portablesh -c 'mkdir -p out && printf "ok\n" > out/status.txt; cat out/status.txt'
portablesh script.sh arg1 arg2
portablesh --check -c 'apt update && apt install jq'
```

A host application can select `portablesh` unconditionally as its automation
shell; the adapter itself does not inspect whether Git is installed. It remains
a non-interactive automation shell rather than a terminal/job-control shell.

## Embedding commands

`Config.Handler` and `Config.Handlers` implement application-owned commands.
Handlers run after shell functions and language builtins and before portable
utilities and host executable resolution. Returning `handled=false` delegates to
the next handler and then to the portable/host command layers.

```go
runner, err := portablesh.New(portablesh.Config{
	Dir:      projectDir,
	External: portablesh.ExternalDisabled,
	Handler: func(ctx context.Context, command portablesh.Command) (bool, error) {
		if command.Args[0] != "app-version" {
			return false, nil
		}
		_, err := fmt.Fprintln(command.Stdout, version)
		return true, err
	},
})
```

An expanded `Command` includes its directory, exported environment, standard
streams, and virtual descriptor table. On Unix, host processes can inherit
descriptors above 2 when the descriptor is backed by `*os.File`. Windows
rejects that unsupported host-process case explicitly; builtins and handlers
still support the full virtual table.

## Controlling execution

Use these independent controls according to the trust model of the caller:

- `ExternalDisabled` prevents fallback to host executables.
- `Policy` authorizes expanded commands and redirections before their effects.
- `RootDir` restricts shell-owned `cd`, `source`, redirections, and portable
  utility paths.
- `FileSystem` supplies shell-owned file reads, metadata, globbing, redirections,
  and optional mutation capabilities for portable utilities.
- `Observer` receives synchronous command, pipeline, process, and limit events.
- `Max*` fields bound scripts, ASTs, loops, commands, expansions, files,
  recursion, pipelines, substitutions, heredocs, and output.

These are composable guardrails, not a security sandbox. External executables
can access the host directly. For adversarial workloads, combine a strict
policy with operating-system isolation.

Heredocs are disabled by default to preserve the fail-closed v0.1 behavior.
Enable them deliberately with `AllowHeredocs: true`; `MaxHeredocBytes` remains
enforced.

Complete programs are available under [examples](examples).

## Errors and status codes

`Status` recognizes `ExitStatus`, including `CommandNotFoundError` as status
127. Callers can use `errors.As` with `SyntaxError`, `UnsupportedFeatureError`,
`ResourceLimitError`, `PolicyDeniedError`, `RedirectionError`, `ExpansionError`,
and `StateError` for structured handling.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build ./...
```

CI also tests Windows and macOS natively, fuzzes parser and quoting boundaries,
checks coverage, and cross-compiles Windows, macOS, and Android targets.

## License

0BSD. See [LICENSE](LICENSE).
