# Changelog

All notable changes are documented here. The project follows semantic
versioning for its public Go API and documented shell subset.

## [Unreleased]

## [0.2.0] - 2026-08-09

### Added

- reusable `Program` parsing, static `Check` reports, shell-safe `Quote`/`Join`,
  snapshots, restore, and independent runner cloning;
- command and redirection policies, ordered handlers, execution observers,
  external-command disabling, root path guards, source loaders, and a
  replaceable shell filesystem;
- typed errors and limits for source, AST, commands, loops, pipelines, globbing,
  files, recursion, braces, heredocs, substitutions, and output;
- `case`, brace expansion, backticks, IFS field splitting, advanced parameter
  operators, here strings, bounded opt-in heredocs, and descriptors 0–255;
- `source`, `local`, `readonly`, `trap`, `getopts`, `umask`, `exec`, `hash`, and
  `times`, plus `errexit`, `noglob`, and `xtrace` options;
- Windows batch-file execution and Job Object cleanup for canceled process
  trees;
- conformance comparisons, fuzz targets, examples, compatibility and security
  documentation, coverage enforcement, and native multi-platform CI.

### Compatibility

- existing v0.1 behavior remains the default;
- heredocs require explicit `AllowHeredocs` opt-in;
- the module remains dependency-free, pure Go, and usable with
  `CGO_ENABLED=0`.

## [0.1.0] - 2026-08-08

- initial bounded shell interpreter, builtins, pipelines, control flow,
  handlers, external processes, cancellation, and platform support.

[Unreleased]: https://github.com/YahirHub/go-portable-shell/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/YahirHub/go-portable-shell/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/YahirHub/go-portable-shell/releases/tag/v0.1.0
