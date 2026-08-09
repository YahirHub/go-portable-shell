package portablesh

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type arithmeticParser struct {
	source string
	offset int
	state  *shellState
}

func evalArithmetic(source string, state *shellState) (int64, error) {
	p := &arithmeticParser{source: source, state: state}
	value, err := p.parseLogicalOr()
	if err != nil {
		return 0, fmt.Errorf("arithmetic expansion: %w", err)
	}
	p.space()
	if p.offset != len(p.source) {
		return 0, fmt.Errorf("arithmetic expansion: unexpected %q", p.source[p.offset:])
	}
	return value, nil
}

func (p *arithmeticParser) parseLogicalOr() (int64, error) {
	left, err := p.parseLogicalAnd()
	for err == nil && p.accept("||") {
		var right int64
		right, err = p.parseLogicalAnd()
		if left != 0 || right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, err
}

func (p *arithmeticParser) parseLogicalAnd() (int64, error) {
	left, err := p.parseComparison()
	for err == nil && p.accept("&&") {
		var right int64
		right, err = p.parseComparison()
		if left != 0 && right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, err
}

func (p *arithmeticParser) parseComparison() (int64, error) {
	left, err := p.parseAdd()
	if err != nil {
		return 0, err
	}
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if !p.accept(op) {
			continue
		}
		right, err := p.parseAdd()
		if err != nil {
			return 0, err
		}
		truth := false
		switch op {
		case "==":
			truth = left == right
		case "!=":
			truth = left != right
		case "<=":
			truth = left <= right
		case ">=":
			truth = left >= right
		case "<":
			truth = left < right
		case ">":
			truth = left > right
		}
		if truth {
			left = 1
		} else {
			left = 0
		}
		break
	}
	return left, nil
}

func (p *arithmeticParser) parseAdd() (int64, error) {
	left, err := p.parseMultiply()
	for err == nil {
		if p.accept("+") {
			var right int64
			right, err = p.parseMultiply()
			left += right
		} else if p.accept("-") {
			var right int64
			right, err = p.parseMultiply()
			left -= right
		} else {
			break
		}
	}
	return left, err
}

func (p *arithmeticParser) parseMultiply() (int64, error) {
	left, err := p.parseUnary()
	for err == nil {
		var op string
		for _, candidate := range []string{"*", "/", "%"} {
			if p.accept(candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			break
		}
		right, parseErr := p.parseUnary()
		if parseErr != nil {
			return 0, parseErr
		}
		if (op == "/" || op == "%") && right == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		switch op {
		case "*":
			left *= right
		case "/":
			left /= right
		case "%":
			left %= right
		}
	}
	return left, err
}

func (p *arithmeticParser) parseUnary() (int64, error) {
	if p.accept("+") {
		return p.parseUnary()
	}
	if p.accept("-") {
		value, err := p.parseUnary()
		return -value, err
	}
	if p.accept("!") {
		value, err := p.parseUnary()
		if value == 0 {
			return 1, err
		}
		return 0, err
	}
	return p.parsePrimary()
}

func (p *arithmeticParser) parsePrimary() (int64, error) {
	p.space()
	if p.accept("(") {
		value, err := p.parseLogicalOr()
		if err != nil {
			return 0, err
		}
		if !p.accept(")") {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		return value, nil
	}
	start := p.offset
	for p.offset < len(p.source) && (unicode.IsLetter(rune(p.source[p.offset])) || unicode.IsDigit(rune(p.source[p.offset])) || p.source[p.offset] == '_') {
		p.offset++
	}
	if start == p.offset {
		return 0, fmt.Errorf("expected a number or variable")
	}
	token := p.source[start:p.offset]
	if value, err := strconv.ParseInt(token, 0, 64); err == nil {
		return value, nil
	}
	value := p.state.env[token]
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 0, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is not an integer", token)
	}
	return parsed, nil
}

func (p *arithmeticParser) accept(value string) bool {
	p.space()
	if !strings.HasPrefix(p.source[p.offset:], value) {
		return false
	}
	p.offset += len(value)
	return true
}

func (p *arithmeticParser) space() {
	for p.offset < len(p.source) && unicode.IsSpace(rune(p.source[p.offset])) {
		p.offset++
	}
}
