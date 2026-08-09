package portablesh

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestSupportedPOSIXSubsetMatchesHostShell(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("host sh is unavailable")
	}
	scripts := []string{
		`value=world; printf 'hello:%s\n' "$value"`,
		`case abc in a*) printf case ;; *) printf other ;; esac`,
		`value=src/path/file.txt; printf '%s:%s' "${value##*/}" "${value%/*}"`,
		`i=0; while test "$i" -lt 3; do i=$((i+1)); printf '%s' "$i"; done`,
		`f() { local_value=$1; printf '%s' "$local_value"; }; f ok`,
	}
	for _, script := range scripts {
		t.Run(Quote(script), func(t *testing.T) {
			root := t.TempDir()
			var portableOut, portableErr bytes.Buffer
			runner, err := New(Config{Dir: root, Env: []string{"PATH="}, Stdout: &portableOut, Stderr: &portableErr})
			if err != nil {
				t.Fatal(err)
			}
			portableRunErr := runner.Run(context.Background(), script)
			portableStatus := statusOrOne(portableRunErr)

			command := exec.Command(shell, "-c", script)
			command.Dir = root
			var hostOut, hostErr bytes.Buffer
			command.Stdout, command.Stderr = &hostOut, &hostErr
			hostRunErr := command.Run()
			hostStatus := statusOrOne(hostRunErr)
			if portableStatus != hostStatus || portableOut.String() != hostOut.String() {
				t.Fatalf("portable=(%d,%q,%q) host=(%d,%q,%q)", portableStatus, portableOut.String(), portableErr.String(), hostStatus, hostOut.String(), hostErr.String())
			}
		})
	}
}

func statusOrOne(err error) int {
	if err == nil {
		return 0
	}
	if status, ok := Status(err); ok {
		return status
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func FuzzBraceExpansionNeverPanics(f *testing.F) {
	for _, seed := range []string{"a{b,c}", "{1..20}", "{1..1000000000}", "{9223372036854775806..9223372036854775807}", "x{a,{b,c}}y", "{"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 4<<10 {
			t.Skip()
		}
		_, _ = expandBraceWord(source, 1_000)
	})
}

func FuzzQuoteProducesOneLiteralArgument(f *testing.F) {
	for _, seed := range []string{"", "plain", "a b", "it's", "$HOME", "line\nnext"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 8<<10 || bytes.IndexByte([]byte(value), 0) >= 0 {
			t.Skip()
		}
		var output bytes.Buffer
		runner, err := New(Config{Dir: t.TempDir(), Env: []string{"PATH="}, Stdout: &output})
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.Run(t.Context(), "printf %s "+Quote(value)); err != nil {
			t.Fatal(err)
		}
		if output.String() != value {
			t.Fatalf("output differs: got=%q want=%q", output.String(), value)
		}
	})
}

func BenchmarkParseAndExecute(b *testing.B) {
	program, err := Parse(`value=0; for item in {1..20}; do value=$((value+item)); done; printf %s "$value"`)
	if err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	for b.Loop() {
		runner, err := New(Config{Dir: root, Env: []string{"PATH="}, Stdout: &bytes.Buffer{}})
		if err != nil {
			b.Fatal(err)
		}
		if err := runner.RunProgram(context.Background(), program); err != nil {
			b.Fatal(err)
		}
	}
}
