# AGENTS.md — go-portable-shell

## Purpose

`go-portable-shell` is a dependency-free, pure-Go interpreter for a bounded
Bash/POSIX-compatible language. It exists primarily as Lilith Code's portable
fallback when a native POSIX shell is unavailable.

## Invariants

- Keep the module dependency-free and compatible with `CGO_ENABLED=0`.
- Do not copy code from another shell implementation.
- Do not claim full Bash or POSIX conformance.
- Unsupported syntax must fail explicitly; never reinterpret it silently.
- Every loop and blocking operation must honor `context.Context`.
- Preserve deterministic behavior across Windows, Linux, macOS and Android.
- Public API changes require tests and an entry under `contexto/`.
- Git author: `YahirHub <217099863+YahirHub@users.noreply.github.com>`.
- Commits are written in Spanish and never mention AI.

## Validation

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go test -c
```
