package searchql

import (
	"errors"
	"strings"
)

type tokenKind int

const (
	tokEnd tokenKind = iota
	tokTerm
	tokOr
	tokAnd
	tokNot
	tokLParen
	tokRParen
)

// token is one lexed element. For tokTerm, field is the recognized field name
// (empty for a bare keyword or phrase) and text is the value with quoting
// resolved. raw is what the user typed, and exists only so an error message
// can quote it back at them.
type token struct {
	kind    tokenKind
	field   string
	text    string
	raw     string
	negated bool
}

// chunk is one run of a token: the lexer keeps quoted and unquoted stretches
// apart so that `title:"a:b"` finds the field colon and `"a:b"` does not.
type chunk struct {
	text   string
	quoted bool
}

// lex splits input on whitespace and parentheses, resolves quoting, and
// classifies each token.
func lex(input string) ([]token, error) {
	var tokens []token
	for pos := 0; pos < len(input); {
		switch c := input[pos]; {
		case isSpace(c):
			pos++
		case c == '(':
			tokens = append(tokens, token{kind: tokLParen, raw: "("})
			pos++
		case c == ')':
			tokens = append(tokens, token{kind: tokRParen, raw: ")"})
			pos++
		default:
			chunks, next, err := lexChunk(input, pos)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, classify(chunks, input[pos:next]))
			pos = next
		}
	}
	return tokens, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// lexChunk reads one whitespace-delimited token starting at pos and returns
// where it ended. Only ASCII bytes are inspected, which is safe because every
// byte of a multi-byte rune is >= 0x80 and so matches none of the delimiters.
func lexChunk(input string, pos int) ([]chunk, int, error) {
	var chunks []chunk
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			chunks = append(chunks, chunk{text: plain.String()})
			plain.Reset()
		}
	}
	for pos < len(input) {
		c := input[pos]
		if isSpace(c) || c == '(' || c == ')' {
			break
		}
		if c != '"' {
			plain.WriteByte(c)
			pos++
			continue
		}
		flush()
		pos++
		var quoted strings.Builder
		closed := false
		for pos < len(input) {
			if input[pos] == '\\' && pos+1 < len(input) && (input[pos+1] == '"' || input[pos+1] == '\\') {
				quoted.WriteByte(input[pos+1])
				pos += 2
				continue
			}
			if input[pos] == '"' {
				pos++
				closed = true
				break
			}
			quoted.WriteByte(input[pos])
			pos++
		}
		if !closed {
			return nil, 0, errors.New("unclosed quote")
		}
		chunks = append(chunks, chunk{text: quoted.String(), quoted: true})
	}
	flush()
	return chunks, pos, nil
}

// classify decides what one token is: an operator, a field term, or a keyword.
//
// The order matters. A leading '-' is stripped first because it can precede
// any of the three. Operators are recognized only unquoted, unnegated and in
// upper case: release titles are full of the words "or", "and" and "not", and
// a language that swallowed them could not search for them at all.
func classify(chunks []chunk, raw string) token {
	negated := false
	if len(chunks) > 0 && !chunks[0].quoted && strings.HasPrefix(chunks[0].text, "-") &&
		(len(chunks[0].text) > 1 || len(chunks) > 1) {
		negated = true
		chunks[0].text = chunks[0].text[1:]
		if chunks[0].text == "" {
			chunks = chunks[1:]
		}
	}

	text := joinChunks(chunks)
	if !negated && len(chunks) == 1 && !chunks[0].quoted {
		switch text {
		case "OR":
			return token{kind: tokOr, raw: raw}
		case "AND":
			return token{kind: tokAnd, raw: raw}
		case "NOT":
			return token{kind: tokNot, raw: raw}
		}
	}

	if name, value, ok := splitField(chunks); ok {
		return token{kind: tokTerm, field: name, text: value, raw: raw, negated: negated}
	}
	return token{kind: tokTerm, text: text, raw: raw, negated: negated}
}

func joinChunks(chunks []chunk) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.text)
	}
	return b.String()
}

// splitField reports the field term a token spells, if it spells one.
//
// It fails, leaving the caller with a literal keyword, for three separate
// reasons, and all three are ordinary input rather than mistakes: there is no
// colon outside quotes, the name before the colon is not a field this language
// knows ("Re:Zero"), or the value is empty ("title:"). Each of those is
// something a user might genuinely want to search for as text.
func splitField(chunks []chunk) (name, value string, ok bool) {
	var before strings.Builder
	for i, c := range chunks {
		if c.quoted {
			// A quote opened before any colon: the token is a phrase, and a
			// colon inside it is part of the text.
			return "", "", false
		}
		colon := strings.IndexByte(c.text, ':')
		if colon < 0 {
			before.WriteString(c.text)
			continue
		}
		before.WriteString(c.text[:colon])
		field := strings.ToLower(before.String())
		if !knownFields[field] {
			return "", "", false
		}
		rest := c.text[colon+1:] + joinChunks(chunks[i+1:])
		if rest == "" {
			return "", "", false
		}
		return field, rest, true
	}
	return "", "", false
}
