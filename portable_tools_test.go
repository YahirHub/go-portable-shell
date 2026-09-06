package portablesh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableUtilitiesWorkWithoutHostCommands(t *testing.T) {
	root := t.TempDir()
	runner, stdout, stderr := testRunner(t, root, nil, "PATH=", "HOME="+root)
	runner.cfg.External = ExternalDisabled

	script := `
mkdir -p work/sub && printf 'alpha\nbeta\n' > work/input.txt;
cp work/input.txt work/copy.txt && mv work/copy.txt work/sub/moved.txt;
touch work/empty.txt; chmod 600 work/empty.txt;
printf 'G:'; grep -n beta work/input.txt;
printf 'H:'; head -n 1 work/input.txt;
printf 'T:'; tail -n 1 work/input.txt;
printf 'W:'; wc -l work/input.txt
`
	if err := runner.Run(context.Background(), script); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	want := "G:2:beta\nH:alpha\nT:beta\nW:2 work/input.txt\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q stderr=%q", stdout.String(), want, stderr.String())
	}
	for _, name := range []string{"work/input.txt", "work/sub/moved.txt", "work/empty.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "work", "copy.txt")); !os.IsNotExist(err) {
		t.Fatalf("copy.txt should have been moved: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := runner.Run(context.Background(), `find work -type f -name '*.txt'`); err != nil {
		t.Fatalf("find: %v stderr=%q", err, stderr.String())
	}
	found := filepath.ToSlash(stdout.String())
	for _, name := range []string{"work/empty.txt", "work/input.txt", "work/sub/moved.txt"} {
		if !strings.Contains(found, name+"\n") {
			t.Fatalf("find output %q missing %q", found, name)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if err := runner.Run(context.Background(), `ls -la work; rm -rf work`); err != nil {
		t.Fatalf("ls/rm: %v stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "input.txt") || !strings.Contains(stdout.String(), "sub") {
		t.Fatalf("ls output=%q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "work")); !os.IsNotExist(err) {
		t.Fatalf("work should be removed: %v", err)
	}
}

func TestPortableUtilitiesKeepApplicationHandlerPriority(t *testing.T) {
	root := t.TempDir()
	handler := func(_ context.Context, command Command) (bool, error) {
		if command.Args[0] != "grep" {
			return false, nil
		}
		_, err := fmt.Fprintln(command.Stdout, "handled-grep")
		return true, err
	}
	runner, stdout, stderr := testRunner(t, root, handler, "PATH=", "HOME="+root)
	runner.cfg.External = ExternalDisabled
	if err := runner.Run(context.Background(), `printf 'beta\n' | grep beta`); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	if stdout.String() != "handled-grep\n" {
		t.Fatalf("handler did not win: %q", stdout.String())
	}
}

func TestPortableUtilitiesHonorRootDir(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-outside")
	_ = os.RemoveAll(outside)
	runner, err := New(Config{
		Dir: root, RootDir: root, Env: []string{"PATH=", "HOME=" + root},
		External: ExternalDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), `mkdir ../`+Quote(filepath.Base(outside)))
	if err == nil {
		t.Fatal("mkdir outside RootDir should fail")
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("outside directory should not exist: %v", statErr)
	}
}

func TestTranslatedHostCandidates(t *testing.T) {
	cases := []struct {
		goos string
		args []string
		want []string
	}{
		{"windows", []string{"apt", "update"}, []string{"winget", "source", "update"}},
		{"windows", []string{"apt-get", "install", "-y", "git"}, []string{"winget", "install", "git", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}},
		{"windows", []string{"python3", "-V"}, []string{"python", "-V"}},
		{"darwin", []string{"apt", "install", "jq"}, []string{"brew", "install", "jq"}},
	}
	for _, tc := range cases {
		candidates := translatedHostCandidates(tc.goos, tc.args)
		if len(candidates) == 0 || strings.Join(candidates[0], "\x00") != strings.Join(tc.want, "\x00") {
			t.Fatalf("%s %v => %v, want first %v", tc.goos, tc.args, candidates, tc.want)
		}
	}
	if candidates := translatedHostCandidates("linux", []string{"apt", "update"}); candidates != nil {
		t.Fatalf("linux apt should stay native: %v", candidates)
	}
	if candidates := translatedHostCandidates("windows", []string{"apt", "install", "one", "two"}); candidates != nil {
		t.Fatalf("ambiguous multi-package winget translation should fail explicitly: %v", candidates)
	}
}

func TestTranslatedCommandIsReauthorized(t *testing.T) {
	root := t.TempDir()
	winget := filepath.Join(root, "winget")
	if err := os.WriteFile(winget, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var checked []string
	runner, err := New(Config{
		Dir: root,
		Env: []string{"PATH=" + root},
		Policy: PolicyFuncs{Command: func(_ context.Context, command Command) error {
			checked = append([]string(nil), command.Args...)
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Command{Args: []string{"apt", "install", "demo"}, Dir: root, Env: []string{"PATH=" + root}}
	translated, err := runner.prepareExternalRequestForOS(context.Background(), "windows", request)
	if err != nil {
		t.Fatal(err)
	}
	if translated.Args[0] != "winget" || len(checked) == 0 || checked[0] != "winget" {
		t.Fatalf("translated=%v checked=%v", translated.Args, checked)
	}

	runner.cfg.Policy = PolicyFuncs{Command: func(_ context.Context, command Command) error {
		if command.Args[0] == "winget" {
			return errors.New("blocked")
		}
		return nil
	}}
	_, err = runner.prepareExternalRequestForOS(context.Background(), "windows", request)
	var denied *PolicyDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("expected translated policy denial, got %v", err)
	}
}

func TestPortableCopyAndMoveProtectSamePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "same.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner, _, _ := testRunner(t, root, nil, "PATH=", "HOME="+root)
	runner.cfg.External = ExternalDisabled
	if err := runner.Run(context.Background(), `cp same.txt same.txt`); err == nil {
		t.Fatal("cp onto the same path should fail")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("same-path cp damaged source: data=%q err=%v", data, err)
	}
	if err := runner.Run(context.Background(), `mv same.txt same.txt`); err != nil {
		t.Fatalf("same-path mv should be a no-op: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("same-path mv damaged source: data=%q err=%v", data, err)
	}
}

func TestPortableGrepFilesWithoutMatchesReturnsSuccessWhenSelected(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner, stdout, stderr := testRunner(t, root, nil, "PATH=", "HOME="+root)
	runner.cfg.External = ExternalDisabled
	if err := runner.Run(context.Background(), `grep -L beta a.txt`); err != nil {
		t.Fatalf("grep -L: %v stderr=%q", err, stderr.String())
	}
	if stdout.String() != "a.txt\n" {
		t.Fatalf("grep -L stdout=%q", stdout.String())
	}
}
