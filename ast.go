package portablesh

type node interface{ shellNode() }

type listNode struct{ items []node }
type andOrNode struct {
	first node
	rest  []andOrPart
}
type andOrPart struct {
	op   tokenKind
	node node
}
type pipelineNode struct {
	commands []node
	negated  bool
}
type simpleNode struct {
	words  []word
	redirs []redirect
}
type ifNode struct {
	branches []ifBranch
	other    node
}
type ifBranch struct{ condition, body node }
type whileNode struct {
	condition node
	body      node
	until     bool
}
type forNode struct {
	name  string
	words []word
	body  node
}
type groupNode struct {
	body     node
	subshell bool
}
type functionNode struct {
	name string
	body node
}

func (*listNode) shellNode()     {}
func (*andOrNode) shellNode()    {}
func (*pipelineNode) shellNode() {}
func (*simpleNode) shellNode()   {}
func (*ifNode) shellNode()       {}
func (*whileNode) shellNode()    {}
func (*forNode) shellNode()      {}
func (*groupNode) shellNode()    {}
func (*functionNode) shellNode() {}

type redirect struct {
	fd     int
	op     string
	target word
}

type word struct {
	parts []wordPart
	pos   position
}

type wordPart struct {
	kind   partKind
	value  string
	quoted bool
}

type partKind uint8

const (
	partLiteral partKind = iota
	partParameter
	partCommand
	partArithmetic
)

func (w word) plain() (string, bool) {
	var value string
	for _, part := range w.parts {
		if part.kind != partLiteral || part.quoted {
			return "", false
		}
		value += part.value
	}
	return value, true
}

type position struct{ line, column int }
