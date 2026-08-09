package portablesh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

func (r *Runner) expandWords(ctx context.Context, state *shellState, input []word, streams streams) ([]string, error) {
	var result []string
	for _, item := range input {
		fields, err := r.expandWord(ctx, state, item, true, true, streams)
		if err != nil {
			return nil, err
		}
		result = append(result, fields...)
	}
	return result, nil
}

func (r *Runner) expandWord(ctx context.Context, state *shellState, input word, split, glob bool, streams streams) ([]string, error) {
	if state.depth >= r.cfg.MaxExpansionDepth {
		return nil, errors.New("portable shell expansion depth exceeded")
	}
	state.depth++
	defer func() { state.depth-- }()

	parts := append([]wordPart(nil), input.parts...)
	if len(parts) > 0 && parts[0].kind == partLiteral && !parts[0].quoted {
		parts[0].value = expandTilde(state, parts[0].value)
	}
	fields := []string{""}
	preserveEmpty := false
	allowGlob := false
	for _, part := range parts {
		values, multi, err := r.evaluatePart(ctx, state, part, streams)
		if err != nil {
			return nil, err
		}
		if part.quoted {
			preserveEmpty = true
			if multi {
				if len(values) == 0 {
					continue
				}
				fields[len(fields)-1] += values[0]
				fields = append(fields, values[1:]...)
				continue
			}
			fields[len(fields)-1] += first(values)
			continue
		}
		if part.kind == partLiteral || !split {
			value := first(values)
			fields[len(fields)-1] += value
			if strings.ContainsAny(value, "*?[") {
				allowGlob = true
			}
			continue
		}
		value := first(values)
		pieces := splitFields(value)
		if len(pieces) == 0 {
			continue
		}
		fields[len(fields)-1] += pieces[0]
		fields = append(fields, pieces[1:]...)
		if strings.ContainsAny(value, "*?[") {
			allowGlob = true
		}
	}
	if len(fields) == 1 && fields[0] == "" && !preserveEmpty && len(parts) > 0 && parts[0].kind != partLiteral {
		return nil, nil
	}
	if !glob || !allowGlob {
		return fields, nil
	}
	var expanded []string
	for _, field := range fields {
		matches := globMatches(state.dir, field)
		if len(matches) == 0 {
			expanded = append(expanded, field)
		} else {
			expanded = append(expanded, matches...)
		}
	}
	return expanded, nil
}

func (r *Runner) evaluatePart(ctx context.Context, state *shellState, part wordPart, streams streams) ([]string, bool, error) {
	switch part.kind {
	case partLiteral:
		return []string{part.value}, false, nil
	case partParameter:
		return r.expandParameter(ctx, state, part.value, part.quoted, streams)
	case partCommand:
		value, err := r.commandSubstitution(ctx, state, part.value, streams)
		return []string{value}, false, err
	case partArithmetic:
		value, err := evalArithmetic(part.value, state)
		if err != nil {
			return nil, false, err
		}
		return []string{strconv.FormatInt(value, 10)}, false, nil
	default:
		return nil, false, errors.New("unknown shell word expansion")
	}
}

func (r *Runner) expandParameter(ctx context.Context, state *shellState, expression string, quoted bool, streams streams) ([]string, bool, error) {
	if expression == "@" {
		return append([]string(nil), state.position...), quoted, nil
	}
	if expression == "*" {
		return []string{strings.Join(state.position, " ")}, false, nil
	}
	if strings.HasPrefix(expression, "#") && validParameterName(expression[1:]) {
		value, _, _ := parameterValue(state, expression[1:])
		return []string{strconv.Itoa(len([]rune(value)))}, false, nil
	}
	name, op, operand := splitParameterOperator(expression)
	if !validParameterName(name) {
		return nil, false, fmt.Errorf("unsupported parameter expansion ${%s}", expression)
	}
	value, set, positional := parameterValue(state, name)
	if op == "" {
		if !set && state.options.nounset {
			return nil, false, fmt.Errorf("%s: unbound variable", name)
		}
		if positional != nil {
			return positional, quoted, nil
		}
		return []string{value}, false, nil
	}
	colon := strings.HasPrefix(op, ":")
	testUnset := !set || colon && value == ""
	operator := strings.TrimPrefix(op, ":")
	switch operator {
	case "-":
		if testUnset {
			result, err := r.expandOperand(ctx, state, operand, streams)
			return []string{result}, false, err
		}
	case "+":
		if !testUnset {
			result, err := r.expandOperand(ctx, state, operand, streams)
			return []string{result}, false, err
		}
		return []string{""}, false, nil
	case "=":
		if testUnset {
			if !validName(name) {
				return nil, false, fmt.Errorf("cannot assign special parameter %s", name)
			}
			result, err := r.expandOperand(ctx, state, operand, streams)
			if err != nil {
				return nil, false, err
			}
			state.env[name] = result
			value = result
		}
	case "?":
		if testUnset {
			message, err := r.expandOperand(ctx, state, operand, streams)
			if err != nil {
				return nil, false, err
			}
			if message == "" {
				message = "parameter is unset or empty"
			}
			return nil, false, fmt.Errorf("%s: %s", name, message)
		}
	default:
		return nil, false, fmt.Errorf("unsupported parameter operator %s", op)
	}
	return []string{value}, false, nil
}

func (r *Runner) expandOperand(ctx context.Context, state *shellState, source string, streams streams) (string, error) {
	tokens, err := lex(source)
	if err != nil {
		return "", err
	}
	if len(tokens) == 1 && tokens[0].kind == tokenEOF {
		return "", nil
	}
	if len(tokens) != 2 || tokens[0].kind != tokenWord {
		return "", fmt.Errorf("parameter operand must expand from one shell word")
	}
	fields, err := r.expandWord(ctx, state, tokens[0].word, false, false, streams)
	if err != nil {
		return "", err
	}
	return first(fields), nil
}

func (r *Runner) commandSubstitution(ctx context.Context, state *shellState, source string, parent streams) (string, error) {
	if r.cfg.MaxCommandSubstitutionBytes < 0 {
		return "", errors.New("command substitution is disabled")
	}
	program, err := parse(source)
	if err != nil {
		return "", err
	}
	buffer := &limitedBuffer{limit: r.cfg.MaxCommandSubstitutionBytes}
	child := state.clone()
	flow := r.execNode(ctx, child, program, streams{in: parent.in, out: buffer, err: parent.err})
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if buffer.exceeded {
		return "", fmt.Errorf("command substitution exceeds %d bytes", r.cfg.MaxCommandSubstitutionBytes)
	}
	if flow.err != nil {
		return "", flow.err
	}
	state.last = normalizeStatus(flow.status)
	return strings.TrimRight(buffer.String(), "\n"), nil
}

type limitedBuffer struct {
	builder  strings.Builder
	limit    int64
	exceeded bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - int64(b.builder.Len())
	if remaining <= 0 {
		b.exceeded = b.exceeded || original > 0
		return original, nil
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
		b.exceeded = true
	}
	_, _ = b.builder.Write(data)
	return original, nil
}

func (b *limitedBuffer) String() string { return b.builder.String() }

func splitAssignment(input word) (string, word, bool) {
	if len(input.parts) == 0 || input.parts[0].kind != partLiteral || input.parts[0].quoted {
		return "", word{}, false
	}
	index := strings.IndexByte(input.parts[0].value, '=')
	if index < 1 {
		return "", word{}, false
	}
	name := input.parts[0].value[:index]
	if !validName(name) {
		return "", word{}, false
	}
	value := word{pos: input.pos, parts: append([]wordPart(nil), input.parts...)}
	value.parts[0].value = value.parts[0].value[index+1:]
	return name, value, true
}

func expandTilde(state *shellState, value string) string {
	if value != "~" && !strings.HasPrefix(value, "~/") && !strings.HasPrefix(value, `~\`) {
		return value
	}
	home := state.env["HOME"]
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	if home == "" {
		return value
	}
	return home + strings.TrimPrefix(value, "~")
}

func splitFields(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) })
}

func globMatches(dir, pattern string) []string {
	full := pattern
	relative := !filepath.IsAbs(pattern)
	if relative {
		full = filepath.Join(dir, pattern)
	}
	matches, err := filepath.Glob(full)
	if err != nil || len(matches) == 0 {
		return nil
	}
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if globExposesHidden(pattern, match, dir, relative) {
			continue
		}
		if relative {
			rel, err := filepath.Rel(dir, match)
			if err != nil {
				continue
			}
			match = rel
		}
		result = append(result, match)
	}
	return result
}

func globExposesHidden(pattern, match, dir string, relative bool) bool {
	matchPattern := pattern
	matchPath := match
	if relative {
		if rel, err := filepath.Rel(dir, match); err == nil {
			matchPath = rel
		}
	} else {
		matchPattern = filepath.Clean(pattern)
		matchPath = filepath.Clean(match)
	}
	patternParts := strings.Split(filepath.ToSlash(matchPattern), "/")
	pathParts := strings.Split(filepath.ToSlash(matchPath), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for index, part := range pathParts {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." && !strings.HasPrefix(patternParts[index], ".") {
			return true
		}
	}
	return false
}

func parameterValue(state *shellState, name string) (string, bool, []string) {
	switch name {
	case "?":
		return strconv.Itoa(state.last), true, nil
	case "$":
		return strconv.Itoa(os.Getpid()), true, nil
	case "#":
		return strconv.Itoa(len(state.position)), true, nil
	case "@":
		return strings.Join(state.position, " "), true, append([]string(nil), state.position...)
	case "*":
		return strings.Join(state.position, " "), true, nil
	case "-":
		var options string
		if state.options.nounset {
			options += "u"
		}
		return options, true, nil
	}
	if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
		index := int(name[0] - '1')
		if name == "0" {
			return "portablesh", true, nil
		}
		if index >= 0 && index < len(state.position) {
			return state.position[index], true, nil
		}
		return "", false, nil
	}
	value, ok := state.env[name]
	return value, ok, nil
}

func splitParameterOperator(expression string) (name, operator, operand string) {
	for i := 1; i < len(expression); i++ {
		if strings.ContainsRune("-+=?", rune(expression[i])) {
			start := i
			if i > 0 && expression[i-1] == ':' {
				start = i - 1
			}
			return expression[:start], expression[start : i+1], expression[i+1:]
		}
	}
	return expression, "", ""
}

func validParameterName(name string) bool {
	if validName(name) {
		return true
	}
	if len(name) != 1 {
		return false
	}
	return strings.ContainsRune("?$#!*@-0123456789", rune(name[0]))
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
