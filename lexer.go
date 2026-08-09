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
	tokenCaseEnd
)

type token struct {
	kind          tokenKind
	word          word
	op            string
	fd            int
	pos           position
	label         string
	heredoc       string
	stripTabs     bool
	heredocExpand bool
}

type lexer struct {
	source      string
	offset      int
	line        int
	column      int
	emitNewline bool
}

func lex(source string) ([]token, error) {
	source = strings.ReplaceAll(source, "\r\n", "\n")
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
	if l.emitNewline {
		l.emitNewline = false
		return token{kind: tokenNewline, pos: l.position(), label: "newline after heredoc"}, nil
	}
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
		if l.peek(0) == '<' && l.peek(1) == '<' && l.peek(2) != '<' {
			return l.takeHeredocRedirect(pos, fd)
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
			l.take()
			return token{kind: tokenCaseEnd, pos: pos, label: ";;"}, nil
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
		if isWordBoundary(l.peek(1)) {
			l.take()
			return token{kind: tokenLBrace, pos: pos, label: "{"}, nil
		}
	case '}':
		if isWordBoundary(l.peek(1)) {
			l.take()
			return token{kind: tokenRBrace, pos: pos, label: "}"}, nil
		}
	case '(':
		l.take()
		return token{kind: tokenLParen, pos: pos, label: "("}, nil
	case ')':
		l.take()
		return token{kind: tokenRParen, pos: pos, label: ")"}, nil
	case '<', '>':
		if l.peek(0) == '<' && l.peek(1) == '<' && l.peek(2) != '<' {
			return l.takeHeredocRedirect(pos, 0)
		}
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
		case '`':
			value, err := l.takeBacktick()
			if err != nil {
				return word{}, err
			}
			w.parts = append(w.parts, wordPart{kind: partCommand, value: value})
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
		case '`':
			value, err := l.takeBacktick()
			if err != nil {
				return err
			}
			w.parts = append(w.parts, wordPart{kind: partCommand, value: value, quoted: true})
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

func (l *lexer) takeBacktick() (string, error) {
	pos := l.position()
	l.take()
	var value strings.Builder
	for !l.done() {
		c := l.take()
		if c == '`' {
			return value.String(), nil
		}
		if c == '\\' && !l.done() {
			next := l.take()
			if next == '`' || next == '\\' || next == '$' {
				value.WriteByte(next)
				continue
			}
			value.WriteByte(c)
			value.WriteByte(next)
			continue
		}
		value.WriteByte(c)
	}
	return "", l.syntax(pos, "unterminated backtick command substitution")
}

func (l *lexer) takeHeredocRedirect(pos position, fd int) (token, error) {
	l.take()
	l.take()
	stripTabs := false
	if l.peek(0) == '-' {
		stripTabs = true
		l.take()
	}
	for l.peek(0) == ' ' || l.peek(0) == '\t' {
		l.take()
	}
	if l.done() || l.peek(0) == '\n' {
		return token{}, l.syntax(pos, "heredoc requires a delimiter")
	}
	delimiterWord, err := l.takeWord()
	if err != nil {
		return token{}, err
	}
	delimiter, ok := delimiterWord.literal()
	if !ok || delimiter == "" {
		return token{}, l.syntax(pos, "heredoc delimiter must be a non-empty literal word")
	}
	expandBody := true
	for _, part := range delimiterWord.parts {
		if part.quoted {
			expandBody = false
			break
		}
	}
	for l.peek(0) == ' ' || l.peek(0) == '\t' {
		l.take()
	}
	if l.peek(0) != '\n' {
		return token{}, l.syntax(pos, "bounded heredoc must be the final redirection on its command line")
	}
	l.take()
	var body strings.Builder
	for !l.done() {
		lineStart := l.offset
		for !l.done() && l.peek(0) != '\n' {
			l.take()
		}
		line := l.source[lineStart:l.offset]
		compare := line
		if stripTabs {
			compare = strings.TrimLeft(compare, "\t")
		}
		if compare == delimiter {
			if !l.done() {
				l.take()
			}
			l.emitNewline = true
			op := "<<"
			if stripTabs {
				op = "<<-"
			}
			return token{kind: tokenRedirect, fd: fd, op: op, word: delimiterWord, label: op, pos: pos, heredoc: body.String(), stripTabs: stripTabs, heredocExpand: expandBody}, nil
		}
		if stripTabs {
			line = strings.TrimLeft(line, "\t")
		}
		body.WriteString(line)
		if !l.done() {
			body.WriteByte('\n')
			l.take()
		}
	}
	return token{}, l.syntax(pos, fmt.Sprintf("unterminated heredoc; expected %q", delimiter))
}

func (l *lexer) takeRedirect() (string, error) {
	first := l.take()
	op := string(first)
	if first == '<' && l.peek(0) == '>' {
		l.take()
		return "<>", nil
	}
	if first == '>' && l.peek(0) == '|' {
		l.take()
		return ">|", nil
	}
	if l.peek(0) == first {
		l.take()
		op += string(first)
		if op == "<<" {
			if l.peek(0) == '<' {
				l.take()
				return "<<<", nil
			}
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
		if value <= 255 {
			value = value*10 + int(c-'0')
			if value > 255 {
				value = 256
			}
		}
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
	return c == 0 || unicode.IsSpace(rune(c)) || strings.ContainsRune(";&|()<>", rune(c))
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
