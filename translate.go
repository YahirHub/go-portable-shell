package portablesh

import (
	"context"
	"runtime"
	"strings"
)

// prepareExternalRequest preserves native command precedence. Translation is
// only attempted when the original command cannot be resolved on the host.
func (r *Runner) prepareExternalRequest(ctx context.Context, request Command) (Command, error) {
	return r.prepareExternalRequestForOS(ctx, runtime.GOOS, request)
}

func (r *Runner) prepareExternalRequestForOS(ctx context.Context, goos string, request Command) (Command, error) {
	if len(request.Args) == 0 || request.Path != "" {
		return request, nil
	}
	if path, err := LookPath(request.Dir, request.Env, request.Args[0]); err == nil {
		request.Path = path
		return request, nil
	}
	for _, candidate := range translatedHostCandidates(goos, request.Args) {
		path, err := LookPath(request.Dir, request.Env, candidate[0])
		if err != nil {
			continue
		}
		translated := request
		translated.Args = append([]string(nil), candidate...)
		translated.Path = path
		if r.cfg.Policy != nil {
			if err := r.cfg.Policy.CheckCommand(ctx, translated); err != nil {
				return Command{}, &PolicyDeniedError{Operation: "translated command " + candidate[0], Err: err}
			}
		}
		return translated, nil
	}
	return request, nil
}

func translatedHostCandidates(goos string, args []string) [][]string {
	if len(args) == 0 {
		return nil
	}
	name := strings.ToLower(args[0])
	switch goos {
	case "windows":
		switch name {
		case "apt", "apt-get":
			if candidate := translateAPTToWinget(args[1:]); candidate != nil {
				return [][]string{candidate}
			}
		case "python3":
			return [][]string{
				append([]string{"python"}, args[1:]...),
				append([]string{"py", "-3"}, args[1:]...),
			}
		case "pip3":
			return [][]string{
				append([]string{"pip"}, args[1:]...),
				append([]string{"py", "-3", "-m", "pip"}, args[1:]...),
			}
		case "xdg-open":
			if len(args) == 2 {
				return [][]string{{"rundll32.exe", "url.dll,FileProtocolHandler", args[1]}}
			}
		}
	case "darwin":
		switch name {
		case "apt", "apt-get":
			if candidate := translateAPTToBrew(args[1:]); candidate != nil {
				return [][]string{candidate}
			}
		case "xdg-open":
			if len(args) == 2 {
				return [][]string{{"open", args[1]}}
			}
		}
	}
	return nil
}

func translateAPTToWinget(args []string) []string {
	args, assumeYes, ok := normalizeAPTArgs(args)
	if !ok || len(args) == 0 {
		return nil
	}
	action, rest := args[0], args[1:]
	base := []string{"winget"}
	switch action {
	case "update":
		if len(rest) != 0 {
			return nil
		}
		return []string{"winget", "source", "update"}
	case "install":
		if len(rest) != 1 {
			return nil
		}
		base = append(base, "install", rest[0])
		if assumeYes {
			base = append(base, "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
		}
		return base
	case "upgrade", "full-upgrade":
		base = append(base, "upgrade")
		if len(rest) == 0 {
			base = append(base, "--all")
		} else if len(rest) == 1 {
			base = append(base, rest[0])
		} else {
			return nil
		}
		if assumeYes {
			base = append(base, "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
		}
		return base
	case "remove", "uninstall":
		if len(rest) != 1 {
			return nil
		}
		base = append(base, "uninstall", rest[0])
		if assumeYes {
			base = append(base, "--disable-interactivity")
		}
		return base
	case "search":
		if len(rest) == 0 {
			return nil
		}
		return []string{"winget", "search", strings.Join(rest, " ")}
	case "show":
		if len(rest) != 1 {
			return nil
		}
		return []string{"winget", "show", rest[0]}
	case "list":
		if len(rest) == 0 || len(rest) == 1 && rest[0] == "--installed" {
			return []string{"winget", "list"}
		}
	}
	return nil
}

func translateAPTToBrew(args []string) []string {
	args, _, ok := normalizeAPTArgs(args)
	if !ok || len(args) == 0 {
		return nil
	}
	action, rest := args[0], args[1:]
	switch action {
	case "update":
		if len(rest) == 0 {
			return []string{"brew", "update"}
		}
	case "install":
		if len(rest) > 0 {
			return append([]string{"brew", "install"}, rest...)
		}
	case "upgrade", "full-upgrade":
		return append([]string{"brew", "upgrade"}, rest...)
	case "remove", "uninstall":
		if len(rest) > 0 {
			return append([]string{"brew", "uninstall"}, rest...)
		}
	case "search":
		if len(rest) > 0 {
			return append([]string{"brew", "search"}, rest...)
		}
	case "show":
		if len(rest) > 0 {
			return append([]string{"brew", "info"}, rest...)
		}
	case "list":
		if len(rest) == 0 || len(rest) == 1 && rest[0] == "--installed" {
			return []string{"brew", "list"}
		}
	}
	return nil
}

func normalizeAPTArgs(args []string) ([]string, bool, bool) {
	result := make([]string, 0, len(args))
	assumeYes := false
	for _, arg := range args {
		switch arg {
		case "-y", "--yes", "--assume-yes":
			assumeYes = true
		case "-q", "-qq", "--quiet":
			// Output verbosity does not have a portable package-manager equivalent.
		default:
			if strings.HasPrefix(arg, "-") && len(result) == 0 {
				return nil, false, false
			}
			result = append(result, arg)
		}
	}
	return result, assumeYes, true
}
