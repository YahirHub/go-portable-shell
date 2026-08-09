package portablesh

import (
	"errors"
	"testing"
)

func TestSyntaxErrorsIncludeSourcePosition(t *testing.T) {
	_, err := parse("printf ok\nif true; then\nprintf broken")
	if err == nil {
		t.Fatal("missing fi should fail")
	}
	var syntaxErr *SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if syntaxErr.Line < 2 || syntaxErr.Column < 1 {
		t.Fatalf("position=%d:%d", syntaxErr.Line, syntaxErr.Column)
	}
}

func TestCommentsAndLineContinuations(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	if err := runner.Run(t.Context(), "value=hel\\\nlo # comment\nprintf '%s#%s\\n' \"$value\" literal"); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "hello#literal\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestCRLFInputIsNormalized(t *testing.T) {
	runner, stdout, _ := testRunner(t, t.TempDir(), nil)
	if err := runner.Run(t.Context(), "value=ok\r\nprintf '%s\\n' \"$value\"\r\n"); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func FuzzParserNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"printf '%s\\n' hello",
		"if true; then echo ok; fi",
		"while false; do :; done",
		"value=$(printf nested)",
		"for x in a b; do echo $x; done",
		"unterminated ' quote",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 64<<10 {
			t.Skip()
		}
		_, _ = parse(source)
	})
}
