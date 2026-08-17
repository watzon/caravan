// Package searchql is the query language the release search box speaks.
//
// A query is free text with optional field terms — `title:"Dune" year:2021
// -cam` — and it compiles into two different things, because the two halves of
// a search cannot be made to agree. An indexer takes one string of free text
// and answers with whatever it thinks matches; there is no protocol for "not a
// cam" or "1080p only". So the language sends upstream what an indexer can act
// on, and applies everything else to the results after they arrive.
//
// That split is the whole design, and it is visible in the API:
// UpstreamQueries reports what goes out, Matches decides what stays. A term
// that only narrows results never widens the fan-out, and a term the indexer
// already handled is not re-enforced locally — an indexer legitimately matches
// on more than the release title, and re-checking free text here would throw
// away results the user asked for.
package searchql

import (
	"errors"
	"strings"
)

// Field names the language knows. Anything else is not an error: "Re:Zero" is
// a title, not a malformed field term, and there is no way to tell the two
// apart except by knowing the field list — so an unrecognized name leaves the
// whole token a literal keyword (see lexer.term).
const (
	fieldTitle    = "title"
	fieldSite     = "site"
	fieldYear     = "year"
	fieldSeason   = "season"
	fieldEpisode  = "episode"
	fieldDate     = "date"
	fieldQuality  = "quality"
	fieldSource   = "source"
	fieldCodec    = "codec"
	fieldAudio    = "audio"
	fieldBitDepth = "bitdepth"
	fieldGroup    = "group"
	fieldEdition  = "edition"
	fieldIndexer  = "indexer"
	fieldIs       = "is"
)

var knownFields = map[string]bool{
	fieldTitle: true, fieldSite: true, fieldYear: true, fieldSeason: true,
	fieldEpisode: true, fieldDate: true, fieldQuality: true, fieldSource: true,
	fieldCodec: true, fieldAudio: true, fieldBitDepth: true, fieldGroup: true,
	fieldEdition: true, fieldIndexer: true, fieldIs: true,
}

// is: values. Everything else is a term that matches nothing rather than a
// parse error, for the same reason a bad year is: a filter that matches
// nothing is visible in an empty result list, while an error would refuse the
// whole search over one mistyped word.
const (
	isProper     = "proper"
	isRepack     = "repack"
	isSeasonPack = "seasonpack"
)

// node is one element of the parsed expression: a term, a negation, or a
// conjunction/disjunction of others.
type node interface{ isNode() }

// termNode is a single test. field is empty for a bare keyword or phrase, in
// which case text is the words as written.
type termNode struct {
	field string
	text  string
}

// notNode negates its child. Both `NOT x` and `-x` produce one, so the rest of
// the package has a single shape to handle.
type notNode struct{ child node }

// andNode and orNode are flattened: `a b c` is one andNode with three
// children, not a chain. Nothing downstream cares about the associativity, and
// flat lists keep String and the branch expansion readable.
type andNode struct{ kids []node }
type orNode struct{ kids []node }

func (*termNode) isNode() {}
func (*notNode) isNode()  {}
func (*andNode) isNode()  {}
func (*orNode) isNode()   {}

// Query is a parsed search expression.
type Query struct{ root node }

// Parse reads a query expression.
//
// It fails only on input that has no reading at all: an unclosed quote or
// parenthesis, an operator with nothing to operate on, or empty parentheses.
// Everything else parses, including tokens that look like mistakes — an
// unknown field name, a colon with no value, a lowercase "or" — because each
// of those is also a legitimate thing to search for. The messages are written
// for the search box, not for a log.
func Parse(input string) (*Query, error) {
	tokens, err := lex(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, errors.New("empty query")
	}
	p := &parser{tokens: tokens}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if !p.done() {
		// parseOr stops at the first token it cannot continue with, and inside
		// a balanced expression that is always a surplus ')'.
		return nil, errors.New("unexpected closing parenthesis")
	}
	return &Query{root: root}, nil
}

// String re-serializes the query in canonical form: implicit AND for
// adjacency, `NOT` for both spellings of negation, and quotes only where they
// are needed to parse back to the same expression. Parse(q.String()) always
// yields an equal query.
func (q *Query) String() string { return render(q.root) }

func render(n node) string {
	switch t := n.(type) {
	case *termNode:
		if t.field == "" {
			return quoteText(t.text)
		}
		return t.field + ":" + quoteValue(t.text)
	case *notNode:
		return "NOT " + grouped(t.child)
	case *andNode:
		parts := make([]string, 0, len(t.kids))
		for _, kid := range t.kids {
			// Only OR needs bracketing here: it binds looser than adjacency.
			if _, isOr := kid.(*orNode); isOr {
				parts = append(parts, "("+render(kid)+")")
				continue
			}
			parts = append(parts, render(kid))
		}
		return strings.Join(parts, " ")
	case *orNode:
		parts := make([]string, 0, len(t.kids))
		for _, kid := range t.kids {
			parts = append(parts, render(kid))
		}
		return strings.Join(parts, " OR ")
	}
	return ""
}

// grouped renders a child of NOT, which binds tighter than either connective
// and so has to bracket one.
func grouped(n node) string {
	switch n.(type) {
	case *andNode, *orNode:
		return "(" + render(n) + ")"
	}
	return render(n)
}

// quoteText spells a bare keyword. It quotes more eagerly than quoteValue
// because a keyword sits where the parser is still looking for operators and
// punctuation: an unquoted `OR`, `-x` or `a(b` would come back as something
// else entirely.
//
// A colon only forces quotes when what precedes it is a field name, which is
// the same test the lexer makes. "Re:Zero" stays as the user typed it.
func quoteText(s string) string {
	if s == "" || s == "OR" || s == "AND" || s == "NOT" || strings.HasPrefix(s, "-") ||
		strings.ContainsAny(s, " \t\n\r\v\f\"()") {
		return quote(s)
	}
	if colon := strings.IndexByte(s, ':'); colon >= 0 && knownFields[strings.ToLower(s[:colon])] {
		return quote(s)
	}
	return s
}

// quoteValue spells a field value, which needs quoting only for what would end
// the token early.
func quoteValue(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n\r\v\f\"()") {
		return quote(s)
	}
	return s
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// parser turns the token stream into a tree. Precedence is the conventional
// one: OR is loosest, adjacency and AND are the same level, NOT is tightest.
type parser struct {
	tokens []token
	pos    int
}

func (p *parser) done() bool { return p.pos >= len(p.tokens) }

func (p *parser) peek() token {
	if p.done() {
		return token{kind: tokEnd}
	}
	return p.tokens[p.pos]
}

func (p *parser) parseOr() (node, error) {
	first, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	kids := []node{first}
	for p.peek().kind == tokOr {
		p.pos++
		next, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		kids = append(kids, next)
	}
	if len(kids) == 1 {
		return kids[0], nil
	}
	return &orNode{kids: kids}, nil
}

func (p *parser) parseAnd() (node, error) {
	first, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	kids := []node{first}
	for {
		switch p.peek().kind {
		case tokAnd:
			p.pos++
		case tokTerm, tokNot, tokLParen:
			// Adjacency is AND with the operator left out.
		default:
			if len(kids) == 1 {
				return kids[0], nil
			}
			return &andNode{kids: kids}, nil
		}
		next, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		kids = append(kids, next)
	}
}

func (p *parser) parseUnary() (node, error) {
	tok := p.peek()
	switch tok.kind {
	case tokNot:
		p.pos++
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &notNode{child: child}, nil
	case tokLParen:
		p.pos++
		if p.peek().kind == tokRParen {
			return nil, errors.New("empty parentheses")
		}
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, errors.New("unclosed parenthesis")
		}
		p.pos++
		return inner, nil
	case tokTerm:
		p.pos++
		var built node = &termNode{field: tok.field, text: tok.text}
		if tok.negated {
			built = &notNode{child: built}
		}
		return built, nil
	case tokRParen:
		return nil, errors.New("unexpected closing parenthesis")
	case tokEnd:
		if p.pos == 0 {
			return nil, errors.New("empty query")
		}
		return nil, errors.New("query ends with " + quote(p.tokens[p.pos-1].raw))
	default:
		return nil, errors.New("unexpected " + quote(tok.raw))
	}
}
