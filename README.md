# go-portable-shell

`go-portable-shell` is a dependency-free interpreter for a bounded,
non-interactive shell language. It is written in pure Go for applications that
need predictable automation on Windows, Linux, macOS, or Android without
shipping Bash.

The project deliberately implements a documented Bash/POSIX-compatible subset.
It does not claim to be a complete POSIX shell, and syntax outside its contract
fails explicitly.

## Highlights

- quotes, variables, positional parameters, arithmetic, command substitution,
  brace expansion, globbing, tilde expansion, and IFS field splitting;
- lists, `&&`, `||`, `!`, pipelines, `if`, `case`, `while`, `until`, `for`,
  groups, subshells, and functions;
- here strings, opt-in bounded heredocs, file redirections, and virtual file
  descriptors from 0 through 255;
- useful non-interactive builtins including `source`, `local`, `readonly`,
  `trap`, `getopts`, `umask`, `exec`, `hash`, and `times`;
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
go get github.com/YahirHub/go-portable-shell@v0.2.1
```

The module requires Go 1.24 or newer.

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

## Embedding commands

`Config.Handler` and `Config.Handlers` implement application-owned commands.
Handlers run after shell functions and builtins and before host executable
resolution. Returning `handled=false` delegates to the next handler or the
operating system.

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
- `RootDir` restricts shell-owned `cd`, `source`, and redirection paths.
- `FileSystem` supplies shell-owned file reads, metadata, globbing, and writes.
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
