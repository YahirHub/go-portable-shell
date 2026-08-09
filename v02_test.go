package portablesh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseCheckQuoteJoinAndSnapshots(t *testing.T) {
	program, err := Parse(`case "$1" in a) printf ok ;; esac`)
	if err != nil {
		t.Fatal(err)
	}
	report := Check(program)
	if report.Nodes == 0 || report.Commands == 0 || !contains(report.Features, "case") {
		t.Fatalf("report=%+v", report)
	}
	if got := Join("printf", "%s", "a b", "it's"); got != `printf '%s' 'a b' 'it'\''s'` {
		t.Fatalf("Join=%q", got)
	}
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	if err := runner.Run(t.Context(), `value=before`); err != nil {
		t.Fatal(err)
	}
	snapshot := runner.Snapshot()
	clone := runner.Clone()
	if err := clone.Run(t.Context(), `value=clone`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), `value=after`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runner.Run(t.Context(), `printf %s "$value"`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "before" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestCaseBraceBackticksIFSAndAdvancedParameters(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	script := `
value=src/path/file.txt
case "$value" in *.txt) printf 'case:' ;; *) false ;; esac
printf '%s,' item{1..3}
printf '[%s]' {01..03}
printf '[%s]' {1..5..-2}
printf '<%s>' "quoted{a,b}"
printf ':%s:%s:%s:%s' "${value##*/}" "${value%/*}" "${value//path/P}" "${value/path/$}"
IFS=,; fields='a,b,c'; for item in $fields; do printf ':%s' "$item"; done
printf ':%s' ` + "`printf old`" + `
`
	if err := runner.Run(t.Context(), script); err != nil {
		t.Fatal(err)
	}
	want := "case:item1,item2,item3,[01][02][03][1][3][5]<quoted{a,b}>:file.txt:src/path:src/P/file.txt:src/$/file.txt:a:b:c:old"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}

func TestSourceLocalReadonlyTrapAndShellOptions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "library.sh"), []byte(`loaded=$1; helper() { local value=inner; readonly guard=locked; printf '%s:%s' "$value" "$guard"; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	runner, stdout, stderr := testRunner(t, root, nil)
	script := `value=outer; source ./library.sh sourced; helper; printf ':%s:%s' "$value" "$loaded"; set -x; printf ':trace'; set +x; trap 'printf :exit' EXIT`
	if err := runner.Run(t.Context(), script); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "inner:locked:outer:sourced:trace:exit" {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "+ printf :trace") {
		t.Fatalf("stderr=%q", stderr.String())
	}

	runner, _, _ = testRunner(t, root, nil)
	err := runner.Run(t.Context(), `set -e; false || true; if false; then printf never; fi; false; printf never`)
	if status, ok := Status(err); !ok || status != 1 {
		t.Fatalf("errexit err=%v status=%d ok=%v", err, status, ok)
	}
}

func TestReadonlyVariablesCannotBeBypassedByStatefulBuiltins(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, stdout, _ := testRunner(t, root, nil)
	if err := runner.Run(t.Context(), `readonly PWD; cd child`); err == nil {
		t.Fatal("cd should not bypass readonly PWD")
	}
	stdout.Reset()
	if err := runner.Run(t.Context(), `pwd`); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != root {
		t.Fatalf("directory changed to %q", stdout.String())
	}
	if err := runner.Run(t.Context(), `readonly OPTIND; set -- -a; getopts a option`); err == nil {
		t.Fatal("getopts should not bypass readonly OPTIND")
	}
	if err := runner.Run(t.Context(), `readonly protected; f() { local protected=value; }; f`); err == nil {
		t.Fatal("local should not shadow a readonly variable")
	}
}

func TestHeredocHereStringGetoptsAndUmask(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	handler := func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] != "copy-input" {
			return false, nil
		}
		_, err := io.Copy(command.Stdout, command.Stdin)
		return true, err
	}
	runner, err := New(Config{
		Dir: root, Env: []string{"PATH="}, Stdout: &stdout, Stderr: &stderr,
		Handler: handler, AllowHeredocs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := "value=hello; copy-input <<EOF\n$value\nEOF\ncopy-input <<'EOF'\n$value\nEOF\nvalue=inline; copy-input <<< \"$value\"\nset -- -a -b value tail; while getopts 'ab:' opt; do printf '%s=%s;' \"$opt\" \"${OPTARG:-}\"; done\numask 077; printf file > private.txt"
	if err := runner.Run(t.Context(), script); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "hello\n$value\ninline\na=;b=value;" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	info, err := os.Stat(filepath.Join(root, "private.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	stdout.Reset()
	if err := runner.Run(t.Context(), `umask`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "0077\n" {
		t.Fatalf("logical umask=%q", stdout.String())
	}
}

func TestVirtualDescriptorsCanBeDuplicatedAndHandled(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	handler := func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] != "write-fd" {
			return false, nil
		}
		descriptor, ok := command.Descriptors[3]
		if !ok || descriptor.Writer == nil {
			return true, errors.New("descriptor 3 missing")
		}
		_, err := fmt.Fprint(descriptor.Writer, "handler")
		return true, err
	}
	runner, err := New(Config{Dir: root, Env: []string{"PATH="}, Stdout: &stdout, Stderr: &stderr, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), `printf builtin 3> builtin.txt 1>&3; write-fd 3> handler.txt`); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"builtin.txt": "builtin", "handler.txt": "handler"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s=%q want=%q", name, data, want)
		}
	}
}

func TestOversizedDescriptorFailsWithoutIntegerOverflow(t *testing.T) {
	root := t.TempDir()
	runner, _, _ := testRunner(t, root, nil)
	err := runner.Run(t.Context(), `999999999999999999999999999999> oversized.txt`)
	var redirectErr *RedirectionError
	if !errors.As(err, &redirectErr) || redirectErr.FD != 256 {
		t.Fatalf("descriptor err=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "oversized.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("oversized descriptor created output: %v", statErr)
	}
}

func TestPoliciesEventsExternalModeRootsAndLimits(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	var events []Event
	deny := errors.New("denied in test")
	runner, err := New(Config{
		Dir: root, Env: []string{"PATH="}, External: ExternalDisabled,
		Stdout: io.Discard, Stderr: io.Discard, RootDir: root,
		Observer: func(event Event) { events = append(events, event) },
		Policy: PolicyFuncs{Command: func(_ context.Context, command Command) error {
			if command.Args[0] == "blocked" {
				return deny
			}
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), `blocked`); err == nil || !errors.Is(err, deny) {
		t.Fatalf("policy err=%v", err)
	}
	if len(events) < 2 || events[0].Kind != EventCommandStart {
		t.Fatalf("events=%+v", events)
	}
	if err := runner.Run(t.Context(), `missing`); err == nil {
		t.Fatal("external-disabled command should fail")
	} else if status, ok := Status(err); !ok || status != 127 {
		t.Fatalf("external err=%v status=%d ok=%v", err, status, ok)
	}
	command := fmt.Sprintf("printf value > %s", Quote(filepath.Join(outside, "blocked.txt")))
	if err := runner.Run(t.Context(), command); err == nil || !strings.Contains(err.Error(), "outside configured root") {
		t.Fatalf("root err=%v", err)
	}

	limited, err := New(Config{Dir: root, Env: []string{"PATH="}, MaxCommands: 2})
	if err != nil {
		t.Fatal(err)
	}
	err = limited.Run(t.Context(), `:; :; :`)
	var limitErr *ResourceLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "commands" {
		t.Fatalf("limit err=%v", err)
	}
}

func TestPolicyDenialPrecedesRedirectionSideEffects(t *testing.T) {
	root := t.TempDir()
	commandDenial := errors.New("command denied")
	runner, err := New(Config{
		Dir: root, Env: []string{"PATH="},
		Policy: PolicyFuncs{Command: func(context.Context, Command) error {
			return commandDenial
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	commandOutput := filepath.Join(root, "command-denied.txt")
	err = runner.Run(t.Context(), `blocked > command-denied.txt`)
	if !errors.Is(err, commandDenial) {
		t.Fatalf("command policy err=%v", err)
	}
	if _, statErr := os.Stat(commandOutput); !os.IsNotExist(statErr) {
		t.Fatalf("command denial created output: %v", statErr)
	}

	redirectDenial := errors.New("redirect denied")
	runner, err = New(Config{
		Dir: root, Env: []string{"PATH="},
		Policy: PolicyFuncs{Redirection: func(context.Context, Redirection) error {
			return redirectDenial
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	redirectOutput := filepath.Join(root, "redirect-denied.txt")
	err = runner.Run(t.Context(), `printf value > redirect-denied.txt`)
	if !errors.Is(err, redirectDenial) {
		t.Fatalf("redirection policy err=%v", err)
	}
	if _, statErr := os.Stat(redirectOutput); !os.IsNotExist(statErr) {
		t.Fatalf("redirection denial created output: %v", statErr)
	}
}

func TestOrderedHandlersAndExternalErrorTypes(t *testing.T) {
	root := t.TempDir()
	var calls []string
	var stdout bytes.Buffer
	runner, err := New(Config{
		Dir: root, Env: []string{"PATH="}, Stdout: &stdout,
		Handlers: []CommandHandler{
			func(context.Context, Command) (bool, error) {
				calls = append(calls, "first")
				return false, nil
			},
			func(_ context.Context, command Command) (bool, error) {
				calls = append(calls, "second")
				_, err := fmt.Fprint(command.Stdout, "handled")
				return true, err
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), `custom`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "first,second" || stdout.String() != "handled" {
		t.Fatalf("calls=%v stdout=%q", calls, stdout.String())
	}

	runner, err = New(Config{Dir: root, Env: []string{"PATH="}})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(t.Context(), `definitely-missing-portable-shell-command`)
	var missing *CommandNotFoundError
	if !errors.As(err, &missing) || missing.Name != "definitely-missing-portable-shell-command" {
		t.Fatalf("missing command err=%v", err)
	}
	if status, ok := Status(err); !ok || status != 127 {
		t.Fatalf("missing status=%d ok=%v", status, ok)
	}
	if err := runner.Run(t.Context(), `definitely-missing-portable-shell-command || true; ! definitely-missing-portable-shell-command`); err != nil {
		t.Fatalf("handled missing status should not escape: %v", err)
	}
}

func TestPipelineFinishEventIncludesResult(t *testing.T) {
	var events []Event
	handler := func(_ context.Context, command Command) (bool, error) {
		switch command.Args[0] {
		case "fail-seven":
			return true, ExitStatus(7)
		case "pass":
			_, _ = io.Copy(io.Discard, command.Stdin)
			return true, nil
		default:
			return false, nil
		}
	}
	runner, err := New(Config{
		Dir: t.TempDir(), Env: []string{"PATH="}, Handler: handler, PipeFail: true,
		Observer: func(event Event) { events = append(events, event) },
	})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(t.Context(), `fail-seven | pass`)
	if status, ok := Status(err); !ok || status != 7 {
		t.Fatalf("pipeline err=%v status=%d ok=%v", err, status, ok)
	}
	for _, event := range events {
		if event.Kind == EventPipelineEnd {
			if event.Status != 7 || event.Err == nil {
				t.Fatalf("pipeline event=%+v", event)
			}
			return
		}
	}
	t.Fatalf("pipeline finish event missing: %+v", events)
}

func TestConfiguredResourceLimitMatrix(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "recursive.sh"), []byte(`source ./recursive.sh`), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		config   Config
		script   string
		resource string
	}{
		{"script", Config{MaxScriptBytes: 3}, `true`, "script_bytes"},
		{"ast", Config{MaxASTNodes: 1}, `:; :`, "ast_nodes"},
		{"pipeline", Config{MaxPipelineCommands: 1}, `: | :`, "pipeline_commands"},
		{"loop", Config{MaxLoopIterations: 1}, `for i in one two; do :; done`, "loop_iterations"},
		{"glob", Config{MaxGlobMatches: 1}, `printf %s *.txt`, "glob_matches"},
		{"brace", Config{MaxBraceExpansions: 2}, `printf %s {1..3}`, "brace_expansions"},
		{"open files", Config{MaxOpenFiles: 1}, `: 3>three.txt 4>four.txt`, "open_files"},
		{"source", Config{MaxSourceDepth: 1}, `source ./recursive.sh`, "source_depth"},
		{"heredoc", Config{AllowHeredocs: true, MaxHeredocBytes: 3}, "read value <<EOF\nfour\nEOF\n", "heredoc_bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.config
			config.Dir = root
			config.Env = []string{"PATH="}
			runner, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			err = runner.Run(t.Context(), test.script)
			var limitErr *ResourceLimitError
			if !errors.As(err, &limitErr) || limitErr.Resource != test.resource {
				t.Fatalf("err=%v resource=%q", err, test.resource)
			}
		})
	}
}

func TestLargeBraceRangeStopsAtConfiguredLimit(t *testing.T) {
	runner, err := New(Config{Dir: t.TempDir(), Env: []string{"PATH="}, MaxBraceExpansions: 4})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(t.Context(), `printf %s {1..1000000000}`)
	var limitErr *ResourceLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "brace_expansions" {
		t.Fatalf("large brace range err=%v", err)
	}
}

func TestBraceExpansionHonorsContextWithoutLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := expandBraceWordContext(ctx, `{1..1000000000}`, -1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("brace context err=%v", err)
	}
}

func TestParserNestingHasAHardSafetyLimit(t *testing.T) {
	script := strings.Repeat("(", maxParserNesting+1) + ":" + strings.Repeat(")", maxParserNesting+1)
	_, err := Parse(script)
	var limitErr *ResourceLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "parser_nesting" {
		t.Fatalf("parser nesting err=%v", err)
	}
}

func TestOutputFunctionHeredocAndFilesystemLimits(t *testing.T) {
	root := t.TempDir()
	limitedOutput, err := New(Config{Dir: root, Env: []string{"PATH="}, MaxOutputBytes: 3, Stdout: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	err = limitedOutput.Run(t.Context(), `printf 1234`)
	var limitErr *ResourceLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "stdout_bytes" {
		t.Fatalf("output limit err=%v", err)
	}

	recursive, err := New(Config{Dir: root, Env: []string{"PATH="}, MaxFunctionDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	err = recursive.Run(t.Context(), `again() { again; }; again`)
	if !errors.As(err, &limitErr) || limitErr.Resource != "function_depth" {
		t.Fatalf("function limit err=%v", err)
	}

	withoutHeredoc, err := New(Config{Dir: root, Env: []string{"PATH="}})
	if err != nil {
		t.Fatal(err)
	}
	err = withoutHeredoc.Run(t.Context(), "read value <<EOF\ncontent\nEOF\n")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != "heredoc" {
		t.Fatalf("heredoc err=%v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "loaded.sh"), []byte(`from_fs=yes`), 0o644); err != nil {
		t.Fatal(err)
	}
	reads := 0
	filesystemRunner, err := New(Config{Dir: root, Env: []string{"PATH="}, FileSystem: recordingFileSystem{OSFileSystem: OSFileSystem{}, reads: &reads}})
	if err != nil {
		t.Fatal(err)
	}
	if err := filesystemRunner.Run(t.Context(), `source ./loaded.sh`); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("filesystem reads=%d", reads)
	}
}

func TestCustomFilesystemGlobResultsAreDeterministic(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout bytes.Buffer
	runner, err := New(Config{
		Dir: root, Env: []string{"PATH="}, Stdout: &stdout,
		FileSystem: reverseGlobFileSystem{OSFileSystem: OSFileSystem{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), `printf '%s\n' *.txt`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "a.txt\nb.txt\n" {
		t.Fatalf("glob order=%q", stdout.String())
	}
}

func TestVersionAndInvalidPublicState(t *testing.T) {
	if Version != "0.2.0" {
		t.Fatalf("Version=%q", Version)
	}
	runner, _, _ := testRunner(t, t.TempDir(), nil)
	if err := runner.Restore(Snapshot{}); err == nil {
		t.Fatal("empty snapshot should fail")
	} else {
		var stateErr *StateError
		if !errors.As(err, &stateErr) {
			t.Fatalf("restore err=%T %v", err, err)
		}
	}
	if err := runner.RunProgram(t.Context(), nil); err == nil {
		t.Fatal("nil program should fail")
	}
	outside, _, _ := testRunner(t, t.TempDir(), nil)
	guardedRoot := t.TempDir()
	guarded, err := New(Config{Dir: guardedRoot, RootDir: guardedRoot, Env: []string{"PATH="}})
	if err != nil {
		t.Fatal(err)
	}
	if err := guarded.Restore(outside.Snapshot()); err == nil {
		t.Fatal("snapshot outside RootDir should fail")
	}
}

func TestRootDirRejectsAnInitialSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := New(Config{Dir: link, RootDir: root, Env: []string{"PATH="}}); err == nil {
		t.Fatal("RootDir accepted an initial symlink escape")
	}
}

type recordingFileSystem struct {
	OSFileSystem
	reads *int
}

type reverseGlobFileSystem struct{ OSFileSystem }

func (f reverseGlobFileSystem) Glob(pattern string) ([]string, error) {
	matches, err := f.OSFileSystem.Glob(pattern)
	for left, right := 0, len(matches)-1; left < right; left, right = left+1, right-1 {
		matches[left], matches[right] = matches[right], matches[left]
	}
	return matches, err
}

func (f recordingFileSystem) ReadFile(name string) ([]byte, error) {
	(*f.reads)++
	return f.OSFileSystem.ReadFile(name)
}
