package portablesh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryEnvironmentAndBuiltinsPersist(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	handler := func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] != "inspect-env" {
			return false, nil
		}
		fmt.Fprintf(command.Stdout, "%s:%t", command.Dir, contains(command.Env, "VISIBLE=yes"))
		return true, nil
	}
	runner, stdout, _ := testRunner(t, root, handler)
	if err := runner.Run(t.Context(), `LOCAL=temp export VISIBLE=yes; cd child; pwd; inspect-env`); err != nil {
		t.Fatal(err)
	}
	want := child + "\n" + child + ":true"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
	stdout.Reset()
	if err := runner.Run(t.Context(), `printf '%s:%s' "$PWD" "${LOCAL:-missing}"`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != child+":missing" {
		t.Fatalf("persistent stdout=%q", stdout.String())
	}
}

func TestEchoPrintfAndEscapeConversions(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	if err := runner.Run(t.Context(), `echo -en 'a\tb\n'; printf '%d %x %b %c %%' 12 15 'x\ny' z`); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "a\tb\n12 f x\ny z %" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestFileAndStringTestOperators(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner, stdout, _ := testRunner(t, root, nil)
	script := `[ -f value.txt ] && [ -s value.txt ] && test 4 -ge 3 && test a != b && printf pass`
	if err := runner.Run(t.Context(), script); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "pass" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestFunctionKeywordReturnAndContinueLevels(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	script := `function render { printf before; return 3; printf never; }; render || printf ':returned'; for a in 1 2; do for b in x y; do printf ':%s%s' "$a" "$b"; continue 2; done; printf never; done`
	if err := runner.Run(t.Context(), script); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "before:returned:1x:2x" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestTypeCommandUnsetNounsetAndExit(t *testing.T) {
	handler := func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] == "owned" {
			fmt.Fprint(command.Stdout, "owned")
			return true, nil
		}
		return false, nil
	}
	runner, stdout, stderr := testRunner(t, t.TempDir(), handler)
	if err := runner.Run(t.Context(), `type printf; command owned; value=x; unset value; set -u`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "printf is a shell builtin\nowned") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if err := runner.Run(t.Context(), `printf '%s' "$value"`); err == nil || !strings.Contains(err.Error(), "unbound variable") {
		t.Fatalf("nounset err=%v stderr=%q", err, stderr.String())
	}
	if err := runner.Run(t.Context(), `set +u; exit 23`); err == nil {
		t.Fatal("exit 23 should return an exit status")
	} else if status, ok := Status(err); !ok || status != 23 {
		t.Fatalf("exit err=%v status=%d ok=%v", err, status, ok)
	}
}

func TestIOWriterErrorsAreReturned(t *testing.T) {
	runner, err := New(Config{Dir: t.TempDir(), Env: []string{"PATH="}, Stdout: errorWriter{}, Stderr: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(t.Context(), `printf value`)
	if status, ok := Status(err); !ok || status != 1 {
		t.Fatalf("err=%v status=%d ok=%v", err, status, ok)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write rejected") }
