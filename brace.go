package portablesh

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (r *Runner) expandBraces(ctx context.Context, state *shellState, fields []string) ([]string, error) {
	var result []string
	for _, field := range fields {
		expanded, err := expandBraceWordContext(ctx, field, r.cfg.MaxBraceExpansions)
		if err != nil {
			var limitErr *ResourceLimitError
			if errors.As(err, &limitErr) {
				return nil, r.observeLimit(err)
			}
			return nil, err
		}
		if state.budget != nil {
			value := state.budget.braceResults.Add(int64(len(expanded)))
			if r.cfg.MaxBraceExpansions >= 0 && value > int64(r.cfg.MaxBraceExpansions) {
				return nil, r.limitError("brace_expansions", int64(r.cfg.MaxBraceExpansions))
			}
		}
		result = append(result, expanded...)
	}
	return result, nil
}

func expandBraceWord(value string, limit int) ([]string, error) {
	return expandBraceWordContext(context.Background(), value, limit)
}

func expandBraceWordContext(ctx context.Context, value string, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	open, close := bracePair(value)
	if open < 0 {
		return []string{value}, nil
	}
	choices, ok, err := braceChoices(ctx, value[open+1:close], limit)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []string{value}, nil
	}
	var result []string
	for _, choice := range choices {
		nested, err := expandBraceWordContext(ctx, value[:open]+choice+value[close+1:], limit)
		if err != nil {
			return nil, err
		}
		result = append(result, nested...)
		if limit >= 0 && len(result) > limit {
			return nil, &ResourceLimitError{Resource: "brace_expansions", Limit: int64(limit)}
		}
	}
	return result, nil
}

func bracePair(value string) (int, int) {
	for open := 0; open < len(value); open++ {
		if value[open] != '{' {
			continue
		}
		depth := 1
		for close := open + 1; close < len(value); close++ {
			switch value[close] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return open, close
				}
			}
		}
	}
	return -1, -1
}

func braceChoices(ctx context.Context, body string, limit int) ([]string, bool, error) {
	if strings.Contains(body, "..") {
		parts := strings.Split(body, "..")
		if len(parts) == 2 || len(parts) == 3 {
			start, startErr := strconv.Atoi(parts[0])
			end, endErr := strconv.Atoi(parts[1])
			if startErr == nil && endErr == nil {
				width := max(braceNumberWidth(parts[0]), braceNumberWidth(parts[1]))
				padded := braceNumberPadded(parts[0]) || braceNumberPadded(parts[1])
				step := 1
				if start > end {
					step = -1
				}
				if len(parts) == 3 {
					parsed, err := strconv.Atoi(parts[2])
					if err != nil || parsed == 0 {
						return nil, false, fmt.Errorf("invalid brace range step %q", parts[2])
					}
					step = parsed
					if start > end && step > 0 {
						step = -step
					} else if start < end && step < 0 {
						step = -step
					}
				}
				var result []string
				for value := start; ; {
					if err := ctx.Err(); err != nil {
						return nil, false, err
					}
					result = append(result, formatBraceNumber(value, width, padded))
					if limit >= 0 && len(result) > limit {
						return nil, false, &ResourceLimitError{Resource: "brace_expansions", Limit: int64(limit)}
					}
					if value == end {
						break
					}
					next := value + step
					if (step > 0 && (next <= value || next > end)) || (step < 0 && (next >= value || next < end)) {
						break
					}
					value = next
				}
				return result, true, nil
			}
		}
	}
	parts := splitBraceList(body)
	if len(parts) < 2 {
		return nil, false, nil
	}
	return parts, true, nil
}

func braceNumberWidth(value string) int {
	return len(strings.TrimLeft(value, "+-"))
}

func braceNumberPadded(value string) bool {
	digits := strings.TrimLeft(value, "+-")
	return len(digits) > 1 && digits[0] == '0'
}

func formatBraceNumber(value, width int, padded bool) string {
	text := strconv.Itoa(value)
	if !padded {
		return text
	}
	sign := ""
	digits := text
	if strings.HasPrefix(digits, "-") {
		sign = "-"
		digits = digits[1:]
	}
	if padding := width - len(digits); padding > 0 {
		digits = strings.Repeat("0", padding) + digits
	}
	return sign + digits
}

func splitBraceList(value string) []string {
	var result []string
	depth, start := 0, 0
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, value[start:index])
				start = index + 1
			}
		}
	}
	if start > 0 {
		result = append(result, value[start:])
	}
	return result
}
