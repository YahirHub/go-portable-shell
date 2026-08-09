# Security

## Security model

`go-portable-shell` executes commands and is not a sandbox. Parsing a script is
side-effect free; running one can read or write files, invoke application
handlers, and start host processes according to `Config`.

For untrusted input, start with all host process execution disabled:

```go
Config{
	External: portablesh.ExternalDisabled,
	Policy:   policy,
	RootDir:  workspace,
}
```

Then expose only the handlers, filesystem implementation, and limits required
by the application.

## Guardrails

- `Policy.CheckCommand` sees expanded arguments before a command or its
  redirections execute.
- `Policy.CheckRedirection` sees the expanded target and resolved path before
  that individual redirection is opened.
- `RootDir` rejects shell-owned paths that escape the configured root and
  resolves existing symlinks where possible. It is a guardrail, not a complete
  filesystem boundary.
- `FileSystem` controls operations performed by builtins, source loading,
  globbing, and redirections. Host executables do not use this interface.
- resource limits reduce accidental or hostile resource exhaustion, but do not
  replace process or container quotas. Parsing also has a fixed nesting ceiling
  so deeply nested groups cannot exhaust the Go call stack.
- context cancellation terminates tracked process trees. A process that has
  already escaped the platform process group or Job Object is outside this
  guarantee.

Policy hooks and observers execute synchronously. Observer calls are serialized
for a runner and its clones. Policy hooks used by pipeline stages may run
concurrently and must synchronize shared state. Neither hook may call the same
runner recursively.

## Reporting a vulnerability

Please open a private GitHub security advisory for
`YahirHub/go-portable-shell`. Include the affected version, platform, minimal
reproduction, and expected impact. Avoid publishing exploit details in a public
issue before a fix is available.
