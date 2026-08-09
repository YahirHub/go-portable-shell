package portablesh

import (
	"regexp"
	"strings"
)

func shellPatternMatch(pattern, value string) bool {
	expression, err := compileShellPattern(pattern, true)
	return err == nil && expression.MatchString(value)
}

func compileShellPattern(pattern string, anchored bool) (*regexp.Regexp, error) {
	var result strings.Builder
	if anchored {
		result.WriteByte('^')
	}
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			result.WriteString(".*")
		case '?':
			result.WriteByte('.')
		case '[':
			end := index + 1
			if end < len(pattern) && (pattern[end] == '!' || pattern[end] == '^') {
				end++
			}
			if end < len(pattern) && pattern[end] == ']' {
				end++
			}
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end >= len(pattern) {
				result.WriteString(`\[`)
				continue
			}
			class := pattern[index+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			result.WriteByte('[')
			result.WriteString(class)
			result.WriteByte(']')
			index = end
		case '\\':
			if index+1 < len(pattern) {
				index++
				result.WriteString(regexp.QuoteMeta(string(pattern[index])))
			} else {
				result.WriteString(`\\`)
			}
		default:
			result.WriteString(regexp.QuoteMeta(string(pattern[index])))
		}
	}
	if anchored {
		result.WriteByte('$')
	}
	return regexp.Compile(result.String())
}

func removeParameterPattern(value, pattern, operator string) string {
	indices := runeBoundaries(value)
	switch operator {
	case "#":
		for _, end := range indices {
			if shellPatternMatch(pattern, value[:end]) {
				return value[end:]
			}
		}
	case "##":
		for index := len(indices) - 1; index >= 0; index-- {
			end := indices[index]
			if shellPatternMatch(pattern, value[:end]) {
				return value[end:]
			}
		}
	case "%":
		for index := len(indices) - 1; index >= 0; index-- {
			start := indices[index]
			if shellPatternMatch(pattern, value[start:]) {
				return value[:start]
			}
		}
	case "%%":
		for _, start := range indices {
			if shellPatternMatch(pattern, value[start:]) {
				return value[:start]
			}
		}
	}
	return value
}

func replaceParameterPattern(value, pattern, replacement string, all bool) (string, error) {
	if pattern == "" {
		return value, nil
	}
	expression, err := compileShellPattern(pattern, false)
	if err != nil {
		return "", err
	}
	if all {
		escaped := strings.ReplaceAll(replacement, "$", "$$")
		return expression.ReplaceAllString(value, escaped), nil
	}
	location := expression.FindStringIndex(value)
	if location == nil {
		return value, nil
	}
	return value[:location[0]] + replacement + value[location[1]:], nil
}

func runeBoundaries(value string) []int {
	result := []int{0}
	for index := range value {
		if index != 0 {
			result = append(result, index)
		}
	}
	return append(result, len(value))
}
