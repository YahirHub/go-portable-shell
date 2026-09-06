# Compatibility contract

`go-portable-shell` accepts a stable, bounded subset of shell syntax for
non-interactive automation. This document describes v0.3.0; behavior not listed
as supported must not be assumed.

## Supported syntax

| Area | Supported |
| --- | --- |
| Commands | simple commands, assignments, functions, groups, subshells |
| Composition | newline/`;` lists, `&&`, `||`, `!`, pipelines, `pipefail` |
| Control flow | `if`/`elif`/`else`, `case`, `while`, `until`, `for`, `break`, `continue`, `return`, `exit` |
| Quotes | single quotes, double quotes, backslash escaping |
| Parameters | `$name`, `${name}`, positional/special parameters, length, default/alternate/error/assignment operators, prefix/suffix removal, replacement |
| Substitution | `$(...)`, legacy backticks, `$((...))` arithmetic |
| Word processing | tilde, IFS splitting, pathname globbing, `{a,b}`, `{1..5}`, `{5..1..2}` |
| Redirection | `<`, `>`, `>>`, `<>`, `>|`, descriptor duplication/closure, `<<<`, opt-in `<<` and `<<-` |
| Options | `errexit`, `noglob`, `nounset`, `xtrace`, `pipefail` |
| Traps | `EXIT`, `INT`, and `TERM` in non-interactive execution |

Heredoc redirections must end their command line. Quoting the delimiter disables
body expansion; `<<-` strips leading tabs. Heredocs are disabled unless
`Config.AllowHeredocs` is true.

## Builtins

`:` `true` `false` `echo` `printf` `pwd` `cd` `export` `unset` `test` `[`
`read` `set` `shift` `break` `continue` `return` `exit` `type` `command` `.`
`source` `local` `readonly` `trap` `getopts` `umask` `exec` `hash` `times`

Portable utilities resolved after application handlers and before host
executables:

`ls` `mkdir` `rmdir` `rm` `cp` `mv` `touch` `chmod` `cat` `head` `tail` `wc`
`basename` `dirname` `which` `env` `printenv` `sleep` `uname` `whoami` `find`
`grep`

These utilities implement documented automation-oriented subsets rather than
full GNU/BSD command compatibility. Unsupported flags fail explicitly. `grep`
regexp mode uses Go RE2 syntax. Explicit executable paths bypass portable utility
resolution.

## Platform behavior

| Capability | Linux/macOS/Android | Windows |
| --- | --- | --- |
| Pure Go, `CGO_ENABLED=0` | yes | yes |
| Builtins and handlers | yes | yes |
| Native executable lookup | yes | yes |
| `.cmd`/`.bat` execution | not applicable | through `COMSPEC` |
| Cancellation cleanup | process group | Job Object |
| Virtual descriptors 0–255 | builtins/handlers | builtins/handlers |
| Host descriptors above 2 | `*os.File` descriptors | explicitly unsupported |
| Basic Linux-style utilities | pure-Go portable utilities | pure-Go portable utilities |
| `apt` fallback when `apt` is absent | native `apt` on Linux; Homebrew mapping on macOS | bounded mapping to `winget` |
| Git Bash requirement | no | no |

Shell-owned file operations use `Config.FileSystem`. External programs always
use the host filesystem.

## Host command translation

Translation is fallback-only: native executable resolution is attempted first.
The bounded v0.3 mappings are:

- Windows: `apt`/`apt-get` actions `update`, one-package `install`, `upgrade`,
  one-package `remove`, `search`, `show`, and `list` map to `winget` when
  available; `python3`, `pip3`, and `xdg-open` have native fallback candidates.
- macOS: the same bounded `apt` family maps to Homebrew when `apt` is absent;
  `xdg-open` maps to `open`.
- Linux/Android: commands keep their native spelling.

Unknown or ambiguous package-manager forms are not guessed. The translated
command is rechecked by `Policy.CheckCommand` before its external process starts.

## Deliberately unsupported

- interactive prompts, terminal control, aliases, history, and job control;
- background jobs (`&`), `wait`, process substitution, and coprocesses;
- indexed or associative arrays;
- `[[ ... ]]`, `select`, `eval`, extended globbing, and Bash regular-expression
  operators;
- programmable completion, shell startup files, and interactive signal
  semantics;
- full POSIX or Bash conformance.

Unsupported recognized syntax returns `SyntaxError` or
`UnsupportedFeatureError`. Applications should run `Parse` and inspect `Check`
when they need to validate a program before execution. Parser nesting has a
hard safety ceiling of 256 commands; exceeding it returns `ResourceLimitError`.
