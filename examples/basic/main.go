package main

import (
	"context"
	"log"
	"os"

	portablesh "github.com/YahirHub/go-portable-shell"
)

func main() {
	runner, err := portablesh.New(portablesh.Config{
		Name:   "portable-example",
		Env:    append(os.Environ(), "NAME=portable shell"),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		log.Fatal(err)
	}

	program, err := portablesh.Parse(`
		for item in {1..3}; do
			printf 'hello %s #%s\n' "${NAME:-world}" "$item"
		done
	`)
	if err != nil {
		log.Fatal(err)
	}
	if err := runner.RunProgram(context.Background(), program); err != nil {
		log.Fatal(err)
	}
}
