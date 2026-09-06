package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	portablesh "github.com/YahirHub/go-portable-shell"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	checkOnly := false
	for len(args) > 0 {
		switch args[0] {
		case "--version", "-V":
			fmt.Fprintf(os.Stdout, "portablesh %s\n", portablesh.Version)
			return 0
		case "--help", "-h":
			printUsage(os.Stdout)
			return 0
		case "--check", "-n":
			checkOnly = true
			args = args[1:]
			continue
		}
		break
	}

	name := "portablesh"
	var source string
	var positional []string
	var err error

	switch {
	case len(args) > 0 && args[0] == "-c":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "portablesh: -c requires a command string")
			return 2
		}
		source = args[1]
		if len(args) >= 3 {
			name = args[2]
			positional = args[3:]
		}
	case len(args) > 0 && args[0] == "--":
		args = args[1:]
		if len(args) == 0 {
			source, err = readAll(os.Stdin)
		} else {
			name = args[0]
			source, err = readScript(args[0])
			positional = args[1:]
		}
	case len(args) > 0:
		if len(args[0]) > 0 && args[0][0] == '-' {
			fmt.Fprintf(os.Stderr, "portablesh: unsupported option %s\n", args[0])
			return 2
		}
		name = args[0]
		source, err = readScript(args[0])
		positional = args[1:]
	default:
		source, err = readAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "portablesh: %v\n", err)
		return 1
	}

	if len(positional) > 0 {
		source = "set -- " + portablesh.Join(positional...) + ";\n" + source
	}
	if checkOnly {
		if _, err := portablesh.Parse(source); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		return 0
	}

	runner, err := portablesh.New(portablesh.Config{
		Name:   name,
		Env:    os.Environ(),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "portablesh: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runner.Run(ctx, source); err != nil {
		if status, ok := portablesh.Status(err); ok {
			return status
		}
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func readScript(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func readAll(reader io.Reader) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: portablesh [-n|--check] -c COMMAND [NAME [ARG...]]")
	fmt.Fprintln(out, "       portablesh [-n|--check] FILE [ARG...]")
	fmt.Fprintln(out, "       portablesh < script")
	fmt.Fprintln(out, "       portablesh --version")
}
