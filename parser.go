package portablesh

import "fmt"

const maxParserNesting = 256

type parser struct {
	tokens []token
	index  int
	depth  int
}

func parse(source string) (node, error) {
	program, err := Parse(source)
	if err != nil {
		return nil, err
	}
	return program.root, nil
}

// Parse validates source and returns an immutable reusable program.
func Parse(source string) (*Program, error) {
	tokens, err := lex(source)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	program, err := p.parseList(nil, tokenEOF)
	if err != nil {
		return nil, err
	}
	if p.current().kind != tokenEOF {
		return nil, p.unexpected("end of input")
	}
	result := &Program{source: source, root: program}
	result.report = inspectProgram(program)
	return result, nil
}

func (p *parser) parseList(stopWords map[string]bool, stopKinds ...tokenKind) (node, error) {
	result := &listNode{}
	p.skipSeparators()
	for !p.atStop(stopWords, stopKinds...) {
		item, err := p.parseAndOr()
		if err != nil {
			return nil, err
		}
		result.items = append(result.items, item)
		if p.isSeparator() {
			p.skipSeparators()
			continue
		}
		if !p.atStop(stopWords, stopKinds...) {
			return nil, p.unexpected("a command separator")
		}
	}
	return result, nil
}

func (p *parser) parseAndOr() (node, error) {
	first, err := p.parsePipeline()
	if err != nil {
		return nil, err
	}
	result := &andOrNode{first: first}
	for p.current().kind == tokenAndIf || p.current().kind == tokenOrIf {
		op := p.take().kind
		p.skipNewlines()
		next, err := p.parsePipeline()
		if err != nil {
			return nil, err
		}
		result.rest = append(result.rest, andOrPart{op: op, node: next})
	}
	if len(result.rest) == 0 {
		return first, nil
	}
	return result, nil
}

func (p *parser) parsePipeline() (node, error) {
	result := &pipelineNode{}
	if p.current().kind == tokenBang {
		result.negated = true
		p.take()
		p.skipNewlines()
	}
	command, err := p.parseCommand()
	if err != nil {
		return nil, err
	}
	result.commands = append(result.commands, command)
	for p.current().kind == tokenPipe {
		p.take()
		p.skipNewlines()
		command, err := p.parseCommand()
		if err != nil {
			return nil, err
		}
		result.commands = append(result.commands, command)
	}
	if len(result.commands) == 1 && !result.negated {
		return result.commands[0], nil
	}
	return result, nil
}

func (p *parser) parseCommand() (node, error) {
	p.depth++
	if p.depth > maxParserNesting {
		p.depth--
		return nil, &ResourceLimitError{Resource: "parser_nesting", Limit: maxParserNesting}
	}
	defer func() { p.depth-- }()
	tok := p.current()
	if tok.kind == tokenLBrace {
		return p.parseGroup(false)
	}
	if tok.kind == tokenLParen {
		return p.parseGroup(true)
	}
	if keyword, ok := p.keyword(); ok {
		switch keyword {
		case "if":
			return p.parseIf()
		case "while":
			return p.parseWhile(false)
		case "until":
			return p.parseWhile(true)
		case "for":
			return p.parseFor()
		case "case":
			return p.parseCase()
		case "function":
			return p.parseFunctionKeyword()
		case "then", "elif", "else", "fi", "do", "done", "in", "esac":
			return nil, p.errorAt(tok.pos, fmt.Sprintf("unexpected reserved word %q", keyword))
		}
	}
	if p.looksLikeFunction() {
		return p.parseFunction(false)
	}
	return p.parseSimple()
}

func (p *parser) parseSimple() (node, error) {
	result := &simpleNode{}
	for {
		switch p.current().kind {
		case tokenWord:
			result.words = append(result.words, p.take().word)
		case tokenRedirect:
			redirToken := p.take()
			if redirToken.op == "<<" || redirToken.op == "<<-" {
				result.redirs = append(result.redirs, redirect{
					fd: redirToken.fd, op: redirToken.op, target: redirToken.word,
					inline: redirToken.heredoc, stripTabs: redirToken.stripTabs, expandInline: redirToken.heredocExpand,
				})
				continue
			}
			if p.current().kind != tokenWord {
				return nil, p.errorAt(redirToken.pos, fmt.Sprintf("redirection %s requires a target", redirToken.op))
			}
			result.redirs = append(result.redirs, redirect{fd: redirToken.fd, op: redirToken.op, target: p.take().word})
		default:
			if len(result.words) == 0 && len(result.redirs) == 0 {
				return nil, p.unexpected("a command")
			}
			return result, nil
		}
	}
}

func (p *parser) parseCase() (node, error) {
	start := p.take()
	if p.current().kind != tokenWord {
		return nil, p.errorAt(start.pos, "case requires a value")
	}
	result := &caseNode{value: p.take().word}
	p.skipNewlines()
	if !p.consumeKeyword("in") {
		return nil, p.errorAt(start.pos, "case requires in")
	}
	p.skipSeparators()
	for !p.consumeKeyword("esac") {
		if p.current().kind == tokenEOF {
			return nil, p.errorAt(start.pos, "unterminated case; expected esac")
		}
		clause := caseClause{}
		if p.current().kind == tokenLParen {
			p.take()
		}
		for {
			if p.current().kind != tokenWord {
				return nil, p.unexpected("a case pattern")
			}
			clause.patterns = append(clause.patterns, p.take().word)
			if p.current().kind != tokenPipe {
				break
			}
			p.take()
		}
		if p.current().kind != tokenRParen {
			return nil, p.unexpected(") after case patterns")
		}
		p.take()
		p.skipSeparators()
		body, err := p.parseList(words("esac"), tokenCaseEnd, tokenEOF)
		if err != nil {
			return nil, err
		}
		clause.body = body
		result.clauses = append(result.clauses, clause)
		if p.current().kind == tokenCaseEnd {
			p.take()
			p.skipSeparators()
			continue
		}
		if !p.consumeKeyword("esac") {
			return nil, p.unexpected(";; or esac")
		}
		return result, nil
	}
	return result, nil
}

func (p *parser) parseGroup(subshell bool) (node, error) {
	open := p.take()
	closeKind := tokenRBrace
	if subshell {
		closeKind = tokenRParen
	}
	body, err := p.parseList(nil, closeKind)
	if err != nil {
		return nil, err
	}
	if p.current().kind != closeKind {
		return nil, p.errorAt(open.pos, fmt.Sprintf("unterminated group opened with %s", open.label))
	}
	p.take()
	return &groupNode{body: body, subshell: subshell}, nil
}

func (p *parser) parseIf() (node, error) {
	start := p.take()
	condition, err := p.parseList(words("then"), tokenEOF)
	if err != nil {
		return nil, err
	}
	if !p.consumeKeyword("then") {
		return nil, p.errorAt(start.pos, "if requires then")
	}
	p.skipSeparators()
	body, err := p.parseList(words("elif", "else", "fi"), tokenEOF)
	if err != nil {
		return nil, err
	}
	result := &ifNode{branches: []ifBranch{{condition: condition, body: body}}}
	for p.consumeKeyword("elif") {
		p.skipSeparators()
		condition, err = p.parseList(words("then"), tokenEOF)
		if err != nil {
			return nil, err
		}
		if !p.consumeKeyword("then") {
			return nil, p.unexpected("then")
		}
		p.skipSeparators()
		body, err = p.parseList(words("elif", "else", "fi"), tokenEOF)
		if err != nil {
			return nil, err
		}
		result.branches = append(result.branches, ifBranch{condition: condition, body: body})
	}
	if p.consumeKeyword("else") {
		p.skipSeparators()
		result.other, err = p.parseList(words("fi"), tokenEOF)
		if err != nil {
			return nil, err
		}
	}
	if !p.consumeKeyword("fi") {
		return nil, p.errorAt(start.pos, "unterminated if; expected fi")
	}
	return result, nil
}

func (p *parser) parseWhile(until bool) (node, error) {
	start := p.take()
	condition, err := p.parseList(words("do"), tokenEOF)
	if err != nil {
		return nil, err
	}
	if !p.consumeKeyword("do") {
		return nil, p.errorAt(start.pos, "loop requires do")
	}
	p.skipSeparators()
	body, err := p.parseList(words("done"), tokenEOF)
	if err != nil {
		return nil, err
	}
	if !p.consumeKeyword("done") {
		return nil, p.errorAt(start.pos, "unterminated loop; expected done")
	}
	return &whileNode{condition: condition, body: body, until: until}, nil
}

func (p *parser) parseFor() (node, error) {
	start := p.take()
	if p.current().kind != tokenWord {
		return nil, p.errorAt(start.pos, "for requires a variable name")
	}
	name, ok := p.current().word.plain()
	if !ok || !validName(name) {
		return nil, p.errorAt(p.current().pos, "invalid for variable name")
	}
	p.take()
	result := &forNode{name: name}
	if p.consumeKeyword("in") {
		for p.current().kind == tokenWord {
			result.words = append(result.words, p.take().word)
		}
	}
	if !p.isSeparator() {
		return nil, p.errorAt(start.pos, "for header must end with ';' or newline")
	}
	p.skipSeparators()
	if !p.consumeKeyword("do") {
		return nil, p.errorAt(start.pos, "for requires do")
	}
	p.skipSeparators()
	body, err := p.parseList(words("done"), tokenEOF)
	if err != nil {
		return nil, err
	}
	if !p.consumeKeyword("done") {
		return nil, p.errorAt(start.pos, "unterminated for; expected done")
	}
	result.body = body
	return result, nil
}

func (p *parser) parseFunctionKeyword() (node, error) {
	p.take()
	return p.parseFunction(true)
}

func (p *parser) parseFunction(keyword bool) (node, error) {
	if p.current().kind != tokenWord {
		return nil, p.unexpected("a function name")
	}
	name, ok := p.current().word.plain()
	if !ok || !validName(name) {
		return nil, p.errorAt(p.current().pos, "invalid function name")
	}
	p.take()
	if !keyword || p.current().kind == tokenLParen {
		if p.current().kind != tokenLParen || p.peek(1).kind != tokenRParen {
			return nil, p.unexpected("() after function name")
		}
		p.take()
		p.take()
	}
	p.skipNewlines()
	if p.current().kind != tokenLBrace && p.current().kind != tokenLParen {
		return nil, p.unexpected("a grouped function body")
	}
	body, err := p.parseCommand()
	if err != nil {
		return nil, err
	}
	return &functionNode{name: name, body: body}, nil
}

func (p *parser) looksLikeFunction() bool {
	if p.current().kind != tokenWord || p.peek(1).kind != tokenLParen || p.peek(2).kind != tokenRParen {
		return false
	}
	name, ok := p.current().word.plain()
	return ok && validName(name)
}

func (p *parser) atStop(stopWords map[string]bool, stopKinds ...tokenKind) bool {
	for _, kind := range stopKinds {
		if p.current().kind == kind {
			return true
		}
	}
	if keyword, ok := p.keyword(); ok && stopWords[keyword] {
		return true
	}
	return false
}

func (p *parser) keyword() (string, bool) {
	if p.current().kind != tokenWord {
		return "", false
	}
	return p.current().word.plain()
}

func (p *parser) consumeKeyword(expected string) bool {
	keyword, ok := p.keyword()
	if !ok || keyword != expected {
		return false
	}
	p.take()
	return true
}

func (p *parser) isSeparator() bool {
	return p.current().kind == tokenSemicolon || p.current().kind == tokenNewline
}

func (p *parser) skipSeparators() {
	for p.isSeparator() {
		p.take()
	}
}

func (p *parser) skipNewlines() {
	for p.current().kind == tokenNewline {
		p.take()
	}
}

func (p *parser) current() token { return p.peek(0) }

func (p *parser) peek(ahead int) token {
	if p.index+ahead >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.index+ahead]
}

func (p *parser) take() token {
	tok := p.current()
	if p.index < len(p.tokens)-1 {
		p.index++
	}
	return tok
}

func (p *parser) unexpected(expected string) error {
	tok := p.current()
	return p.errorAt(tok.pos, fmt.Sprintf("expected %s, found %s", expected, tok.label))
}

func (p *parser) errorAt(pos position, message string) error {
	return &SyntaxError{Line: pos.line, Column: pos.column, Message: message}
}

func words(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
