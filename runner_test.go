package portablesh

import (
	"bufio"
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
	"time"
)

func testRunner(t *testing.T, dir string, handler CommandHandler, env ...string) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if len(env) == 0 {
		env = []string{"PATH=", "HOME=" + dir}
	}
	runner, err := New(Config{Dir: dir, Env: env, Stdout: &stdout, Stderr: &stderr, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	return runner, &stdout, &stderr
}

func TestVariablesQuotesPipelineAndHandler(t *testing.T) {
	handler := func(ctx context.Context, command Command) (bool, error) {
		if command.Args[0] != "grep" {
			return false, nil
		}
		data, err := io.ReadAll(command.Stdin)
		if err != nil {
			return true, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, command.Args[1]) {
				fmt.Fprintln(command.Stdout, line)
			}
		}
		return true, nil
	}
	runner, stdout, _ := testRunner(t, t.TempDir(), handler)
	err := runner.Run(context.Background(), `name=world; printf 'hello %s\nalpha\nbeta\n' "$name" | grep beta`)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "beta\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRedirectionAndDescriptorOrdering(t *testing.T) {
	root := t.TempDir()
	runner, stdout, _ := testRunner(t, root, nil)
	if err := runner.Run(context.Background(), `printf 'one\n' > out.txt; printf 'two\n' >> out.txt; cat-mock < out.txt`); err == nil {
		t.Fatal("missing command should fail")
	}
	data, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" || stdout.Len() != 0 {
		t.Fatalf("data=%q stdout=%q", data, stdout.String())
	}

	stdout.Reset()
	runner.cfg.Handler = func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] != "both" {
			return false, nil
		}
		fmt.Fprintln(command.Stdout, "out")
		fmt.Fprintln(command.Stderr, "err")
		return true, nil
	}
	if err := runner.Run(context.Background(), `both > combined.txt 2>&1`); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "combined.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "out\nerr\n" {
		t.Fatalf("combined=%q", data)
	}
}

func TestControlFlowFunctionsAndSubshells(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	script := `
value=outer
greet() { printf '%s:%s\n' "$1" "$value"; }
if test -n "$value"; then greet first; else false; fi
i=0
while test "$i" -lt 2; do i=$((i+1)); printf 'w%s\n' "$i"; done
until test "$i" -ge 3; do i=$((i+1)); done
for item in a b; do printf 'f%s\n' "$item"; done
(value=inner; printf 's%s\n' "$value")
printf 'p%s\n' "$value"
`
	if err := runner.Run(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	want := "first:outer\nw1\nw2\nfa\nfb\nsinner\npouter\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}

func TestParameterCommandAndArithmeticExpansion(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	script := `unset missing; value=${missing:-fallback}; assigned=${other:=set}; printf '%s %s %s %s\n' "$value" "$assigned" "$other" "$((2 + 3 * 4))"; printf '<%s>\n' "$(printf 'sub\n\n')"`
	if err := runner.Run(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "fallback set set 14\n<sub>\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestAndOrNegationTestAndPipeFail(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] == "fail" {
			return true, ExitStatus(7)
		}
		if command.Args[0] == "pass" {
			return true, nil
		}
		return false, nil
	})
	if err := runner.Run(context.Background(), `false || printf 'or\n'; true && printf 'and\n'; ! false; set -o pipefail; fail | pass || printf 'pipe\n'`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "or\nand\npipe\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestGlobbingAndTilde(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"b.go", "a.go", "note.md", ".hidden.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner, stdout, _ := testRunner(t, root, nil)
	if err := runner.Run(context.Background(), `printf '%s\n' *.go; printf '%s\n' ~/note.md`); err != nil {
		t.Fatal(err)
	}
	want := "a.go\nb.go\n" + filepath.Join(root, "note.md") + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
	stdout.Reset()
	if err := runner.Run(context.Background(), `printf '%s\n' .*.go`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != ".hidden.go\n" {
		t.Fatalf("explicit hidden glob stdout=%q", stdout.String())
	}
}

func TestContextCancellationStopsLoop(t *testing.T) {
	runner, _, _ := testRunner(t, t.TempDir(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runner.Run(ctx, `while true; do :; done`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestExternalCommandPriorityAndExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper script is POSIX-only")
	}
	root := t.TempDir()
	bin := t.TempDir()
	helper := filepath.Join(bin, "hello")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'native:%s\\n' \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, stdout, stderr := testRunner(t, root, nil, "PATH="+bin, "HOME="+root)
	if err := runner.Run(context.Background(), `hello ok`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "native:ok\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
	err := runner.Run(context.Background(), `missing-tool`)
	if status, ok := Status(err); !ok || status != 127 {
		t.Fatalf("err=%v status=%d ok=%v", err, status, ok)
	}
	if !strings.Contains(stderr.String(), "command not found") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestCommandSubstitutionLimit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner, err := New(Config{Dir: t.TempDir(), Env: []string{"PATH="}, Stdout: &stdout, Stderr: &stderr, MaxCommandSubstitutionBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), `value=$(printf 1234)`)
	if err == nil || !strings.Contains(err.Error(), "exceeds 3 bytes") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadAndPositionalParameters(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner, err := New(Config{Dir: t.TempDir(), Env: []string{"PATH="}, Stdin: strings.NewReader("one two three\n"), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), `read first rest; set -- a b c; shift; printf '%s:%s:%s:%s\n' "$first" "$rest" "$#" "$1"`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "one:two three:2:b\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestNestedBreakAndContinueLevels(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	script := `for outer in a b; do for inner in 1 2; do printf '%s%s\n' "$outer" "$inner"; break 2; done; printf never; done; printf done`
	if err := runner.Run(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "a1\ndone" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestUnsupportedSyntaxIsExplicit(t *testing.T) {
	for _, script := range []string{`cat <<EOF`, `sleep 1 &`, `cat <(echo x)`} {
		_, err := parse(script)
		if err == nil {
			t.Fatalf("script %q should fail", script)
		}
		var syntaxErr *SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Fatalf("script %q err=%T %v", script, err, err)
		}
	}
}

func TestHandlerReceivesEnvironmentDirectoryAndStreams(t *testing.T) {
	root := t.TempDir()
	called := false
	runner, stdout, _ := testRunner(t, root, func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] != "inspect" {
			return false, nil
		}
		called = true
		if command.Dir != root || !contains(command.Env, "LOCAL=value") {
			t.Errorf("command=%+v", command)
		}
		reader := bufio.NewReader(command.Stdin)
		line, _ := reader.ReadString('\n')
		fmt.Fprintf(command.Stdout, "%s:%s", command.Args[1], line)
		return true, nil
	})
	if err := runner.Run(context.Background(), `printf 'input\n' | LOCAL=value inspect arg`); err != nil {
		t.Fatal(err)
	}
	if !called || stdout.String() != "arg:input\n" {
		t.Fatalf("called=%v stdout=%q", called, stdout.String())
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
