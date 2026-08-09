package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"

	portablesh "github.com/YahirHub/go-portable-shell"
)

func main() {
	workspace, err := os.MkdirTemp("", "portable-shell-policy-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(workspace)

	policy := portablesh.PolicyFuncs{
		Command: func(_ context.Context, command portablesh.Command) error {
			if command.Args[0] != "printf" {
				return errors.New("only printf is permitted")
			}
			return nil
		},
		Redirection: func(_ context.Context, redirect portablesh.Redirection) error {
			if filepath.Ext(redirect.Path) != ".txt" {
				return errors.New("only .txt outputs are permitted")
			}
			return nil
		},
	}

	runner, err := portablesh.New(portablesh.Config{
		Dir:      workspace,
		RootDir:  workspace,
		External: portablesh.ExternalDisabled,
		Policy:   policy,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := runner.Run(context.Background(), `printf '%s\n' safe > result.txt`); err != nil {
		log.Fatal(err)
	}
}
