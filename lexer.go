package portablesh

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenWord
	tokenNewline
	tokenSemicolon
	tokenAndIf
	tokenOrIf
	tokenPipe
	tokenBang
	tokenLBrace
	tokenRBrace
	tokenLParen
	tokenRParen
	tokenRedirect
)

type token struct {
	kind  tokenKind
	word  word
	op    string
	fd    int
	pos   position
	label string
}

type lexer struct {
	source string
	offset int
	line   int
	column int
}

func lex(source string) ([]token, error) {
	l := &lexer{source: source, line: 1, column: 1}
	var tokens []token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.kind == tokenEOF {
			return tokens, nil
		}
	}
}

func (l *lexer) next() (token, error) {
	for !l.done() {
		switch l.peek(0) {
		case ' ', '\t', '\r':
			l.take()
			continue
		case '#':
			for !l.done() && l.peek(0) != '\n' {
				l.take()
			}
			continue
		}
		break
	}
	pos := l.position()
	if l.done() {
		return token{kind: tokenEOF, pos: pos, label: "end of input"}, nil
	}
	if l.peek(0) == '\n' {
		l.take()
		return token{kind: tokenNewline, pos: pos, label: "newline"}, nil
	}
	if fd, length, ok := l.ioNumber(); ok {
		for range length {
			l.take()
		}
		op, err := l.takeRedirect()
		if err != nil {
			return token{}, err
		}
		return token{kind: tokenRedirect, fd: fd, op: op, pos: pos, label: op}, nil
	}
	switch l.peek(0) {
	case ';':
		l.take()
		if l.peek(0) == ';' {
			return token{}, l.syntax(pos, "case terminator ';;' is not supported")
		}
		return token{kind: tokenSemicolon, pos: pos, label: ";"}, nil
	case '&':
		l.take()
		if l.peek(0) == '&' {
			l.take()
			return token{kind: tokenAndIf, pos: pos, label: "&&"}, nil
		}
		return token{}, l.syntax(pos, "background jobs with '&' are not supported")
	case '|':
		l.take()
		if l.peek(0) == '|' {
			l.take()
			return token{kind: tokenOrIf, pos: pos, label: "||"}, nil
		}
		if l.peek(0) == '&' {
			return token{}, l.syntax(pos, "the '|&' extension is not supported; use '2>&1 |'")
		}
		return token{kind: tokenPipe, pos: pos, label: "|"}, nil
	case '!':
		if isWordBoundary(l.peek(1)) {
			l.take()
			return token{kind: tokenBang, pos: pos, label: "!"}, nil
		}
	case '{':
		l.take()
		return token{kind: tokenLBrace, pos: pos, label: "{"}, nil
	case '}':
		l.take()
		return token{kind: tokenRBrace, pos: pos, label: "}"}, nil
	case '(':
		l.take()
		return token{kind: tokenLParen, pos: pos, label: "("}, nil
	case ')':
		l.take()
		return token{kind: tokenRParen, pos: pos, label: ")"}, nil
	case '<', '>':
		op, err := l.takeRedirect()
		if err != nil {
			return token{}, err
		}
		fd := 1
		if strings.HasPrefix(op, "<") {
			fd = 0
		}
		return token{kind: tokenRedirect, fd: fd, op: op, pos: pos, label: op}, nil
	}
	w, err := l.takeWord()
	if err != nil {
		return token{}, err
	}
	return token{kind: tokenWord, word: w, pos: pos, label: "word"}, nil
}

func (l *lexer) takeWord() (word, error) {
	w := word{pos: l.position()}
	for !l.done() && !isWordBoundary(l.peek(0)) {
		switch l.peek(0) {
		case '\\':
			pos := l.position()
			l.take()
			if l.done() {
				return word{}, l.syntax(pos, "unfinished escape")
			}
			if l.peek(0) == '\n' {
				l.take()
				continue
			}
			l.addLiteral(&w, string(l.take()), true)
		case '\'':
			pos := l.position()
			l.take()
			var value strings.Builder
			for !l.done() && l.peek(0) != '\'' {
				value.WriteByte(l.take())
			}
			if l.done() {
				return word{}, l.syntax(pos, "unterminated single quote")
			}
			l.take()
			l.addLiteral(&w, value.String(), true)
		case '"':
			if err := l.takeDoubleQuoted(&w); err != nil {
				return word{}, err
			}
		case '$':
			part, err := l.takeExpansion(false)
			if err != nil {
				return word{}, err
			}
			w.parts = append(w.parts, part)
		default:
			l.addLiteral(&w, string(l.take()), false)
		}
	}
	if len(w.parts) == 0 {
		return word{}, l.syntax(w.pos, "expected a word")
	}
	return w, nil
}

func (l *lexer) takeDoubleQuoted(w *word) error {
	pos := l.position()
	l.take()
	added := false
	for !l.done() && l.peek(0) != '"' {
		switch l.peek(0) {
		case '\\':
			l.take()
			if l.done() {
				return l.syntax(pos, "unterminated double quote")
			}
			next := l.peek(0)
			if next == '\n' {
				l.take()
				continue
			}
			if strings.ContainsRune(`$\"`, rune(next)) {
				l.addLiteral(w, string(l.take()), true)
			} else {
				l.addLiteral(w, `\`, true)
			}
			added = true
		case '$':
			part, err := l.takeExpansion(true)
			if err != nil {
				return err
			}
			w.parts = append(w.parts, part)
			added = true
		default:
			l.addLiteral(w, string(l.take()), true)
			added = true
		}
	}
	if l.done() {
		return l.syntax(pos, "unterminated double quote")
	}
	l.take()
	if !added {
		l.addLiteral(w, "", true)
	}
	return nil
}

func (l *lexer) takeExpansion(quoted bool) (wordPart, error) {
	pos := l.position()
	l.take()
	if l.done() {
		return wordPart{kind: partLiteral, value: "$", quoted: quoted}, nil
	}
	switch l.peek(0) {
	case '{':
		value, err := l.takeBalanced('{', '}')
		if err != nil {
			return wordPart{}, l.syntax(pos, "unterminated parameter expansion")
		}
		return wordPart{kind: partParameter, value: value, quoted: quoted}, nil
	case '(':
		l.take()
		if l.peek(0) == '(' {
			l.take()
			value, err := l.takeArithmetic()
			if err != nil {
				return wordPart{}, l.syntax(pos, err.Error())
			}
			return wordPart{kind: partArithmetic, value: value, quoted: quoted}, nil
		}
		value, err := l.takeCommandSubstitution()
		if err != nil {
			return wordPart{}, l.syntax(pos, err.Error())
		}
		return wordPart{kind: partCommand, value: value, quoted: quoted}, nil
	default:
		c := l.peek(0)
		if isNameStart(c) {
			start := l.offset
			for !l.done() && isNameContinue(l.peek(0)) {
				l.take()
			}
			return wordPart{kind: partParameter, value: l.source[start:l.offset], quoted: quoted}, nil
		}
		if strings.ContainsRune("?$#!*@-0123456789", rune(c)) {
			l.take()
			return wordPart{kind: partParameter, value: string(c), quoted: quoted}, nil
		}
		return wordPart{kind: partLiteral, value: "$", quoted: quoted}, nil
	}
}

// takeBalanced is called with the opening byte still unread.
func (l *lexer) takeBalanced(open, close byte) (string, error) {
	l.take()
	start := l.offset
	depth := 1
	quote := byte(0)
	escaped := false
	for !l.done() {
		c := l.take()
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == open {
			depth++
		} else if c == close {
			depth--
			if depth == 0 {
				return l.source[start : l.offset-1], nil
			}
		}
	}
	return "", fmt.Errorf("unclosed %c", open)
}

func (l *lexer) takeCommandSubstitution() (string, error) {
	start := l.offset
	depth := 1
	quote := byte(0)
	escaped := false
	for !l.done() {
		c := l.take()
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				return l.source[start : l.offset-1], nil
			}
		}
	}
	return "", fmt.Errorf("unterminated command substitution")
}

func (l *lexer) takeArithmetic() (string, error) {
	start := l.offset
	depth := 1
	for !l.done() {
		c := l.take()
		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			if depth > 1 {
				depth--
				continue
			}
			if l.peek(0) == ')' {
				value := l.source[start : l.offset-1]
				l.take()
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("unterminated arithmetic expansion")
}

func (l *lexer) takeRedirect() (string, error) {
	pos := l.position()
	first := l.take()
	op := string(first)
	if l.peek(0) == first {
		l.take()
		op += string(first)
		if op == "<<" {
			if l.peek(0) == '<' {
				return "", l.syntax(pos, "here strings are not supported")
			}
			return "", l.syntax(pos, "heredocs are not supported")
		}
	}
	if l.peek(0) == '&' {
		l.take()
		op += "&"
	}
	return op, nil
}

func (l *lexer) ioNumber() (int, int, bool) {
	if l.done() || l.peek(0) < '0' || l.peek(0) > '9' {
		return 0, 0, false
	}
	value, length := 0, 0
	for c := l.peek(length); c >= '0' && c <= '9'; c = l.peek(length) {
		value = value*10 + int(c-'0')
		length++
	}
	if next := l.peek(length); next != '<' && next != '>' {
		return 0, 0, false
	}
	return value, length, true
}

func (l *lexer) addLiteral(w *word, value string, quoted bool) {
	if len(w.parts) > 0 {
		last := &w.parts[len(w.parts)-1]
		if last.kind == partLiteral && last.quoted == quoted {
			last.value += value
			return
		}
	}
	w.parts = append(w.parts, wordPart{kind: partLiteral, value: value, quoted: quoted})
}

func (l *lexer) position() position { return position{line: l.line, column: l.column} }

func (l *lexer) syntax(pos position, message string) error {
	return &SyntaxError{Line: pos.line, Column: pos.column, Message: message}
}

func (l *lexer) done() bool { return l.offset >= len(l.source) }

func (l *lexer) peek(ahead int) byte {
	if l.offset+ahead >= len(l.source) {
		return 0
	}
	return l.source[l.offset+ahead]
}

func (l *lexer) take() byte {
	if l.done() {
		return 0
	}
	c := l.source[l.offset]
	l.offset++
	if c == '\n' {
		l.line++
		l.column = 1
	} else {
		l.column++
	}
	return c
}

func isWordBoundary(c byte) bool {
	return c == 0 || unicode.IsSpace(rune(c)) || strings.ContainsRune(";&|{}()<>", rune(c))
}

func isNameStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isNameContinue(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }

func validName(name string) bool {
	if name == "" || !isNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isNameContinue(name[i]) {
			return false
		}
	}
	return true
}
