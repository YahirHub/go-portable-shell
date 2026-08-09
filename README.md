# go-portable-shell

`go-portable-shell` is a small, dependency-free shell interpreter written in
pure Go. It is designed for applications that need predictable, non-interactive
shell automation on Windows, Linux, macOS or Android without bundling Bash.

It intentionally implements a bounded Bash/POSIX-compatible language rather
than claiming full shell conformance.

## Features

- quoting, escaping, variables and parameter expansion;
- command substitution with a configurable output limit;
- sequential lists, `&&`, `||`, `!` and pipelines;
- `<`, `>`, `>>`, `2>`, `2>>` and descriptor duplication;
- `if`, `while`, `until`, `for`, groups, subshells and functions;
- filesystem globbing and tilde expansion;
- useful builtins such as `cd`, `printf`, `export`, `test`, `read` and `set`;
- external processes plus a pluggable command-handler API;
- typed exit statuses and cooperative context cancellation;
- Unix process-group cleanup when a context is canceled;
- bounded expansion depth and command-substitution output;
- no runtime dependencies and no CGO.

Unsupported features such as heredocs, job control, process substitution,
arrays and Bash-specific extended globbing return syntax errors.

The language is deliberately fail-closed: unsupported options and syntax are
reported instead of being approximated with a different meaning.

## Usage

```go
runner, err := portablesh.New(portablesh.Config{
    Dir:    projectDir,
    Env:    os.Environ(),
    Stdin:  os.Stdin,
    Stdout: os.Stdout,
    Stderr: os.Stderr,
})
if err != nil {
    log.Fatal(err)
}
if err := runner.Run(ctx, `name=world; printf 'hello %s\n' "$name"`); err != nil {
    log.Fatal(err)
}
```

Applications can provide `Config.Handler` to implement commands internally.
The handler runs after shell functions and builtins but before external-command
resolution. Returning `handled=false` delegates to the operating system.

`Runner` keeps its environment, working directory, functions and positional
parameters across sequential `Run` calls. A runner is intentionally not safe
for concurrent use; create one runner per independent shell session.

## Security

This package interprets commands. It is not a sandbox. Callers must apply their
own authorization and filesystem/process policies before running untrusted
scripts. Context cancellation and substitution limits are reliability controls,
not a security boundary.

## License

0BSD. See [LICENSE](LICENSE).
