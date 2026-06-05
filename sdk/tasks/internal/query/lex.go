package query

import "strings"

// tokenKind identifies the syntactic category of a token.
type tokenKind int

const (
	tokEOF       tokenKind = iota
	tokWord                // bareword [A-Za-z0-9_:./@-]+
	tokNumber              // decimal integer
	tokString              // double-quoted string (already unescaped in .Val)
	tokLParen              // (
	tokRParen              // )
	tokNot                 // !
	tokAnd                 // &&
	tokOr                  // ||
	tokEqEq                // ==
	tokNotEq               // !=
	tokLT                  // <
	tokLE                  // <=
	tokGT                  // >
	tokGE                  // >=
	tokTilde               // ~
	tokError               // lexer error
)

// token is a single lexical token.
type token struct {
	Kind tokenKind
	Val  string // raw text (string tokens already have escapes resolved)
	Pos  int    // byte offset in the original expression
}

// lexer tokenizes a filter expression.
type lexer struct {
	src    string
	pos    int
	tokens []token
	err    *ParseError
}

// isWordChar reports whether b is in the bareword character set: [A-Za-z0-9_:./@-].
func isWordChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '_' || b == ':' || b == '.' || b == '/' || b == '@' || b == '-'
}

// isDigit reports whether b is an ASCII decimal digit.
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// tokenize returns all tokens for src or a *ParseError on failure.
func tokenize(src string) ([]token, *ParseError) {
	l := &lexer{src: src}
	for l.err == nil {
		tok := l.next()
		l.tokens = append(l.tokens, tok)
		if tok.Kind == tokEOF || tok.Kind == tokError {
			break
		}
	}
	if l.err != nil {
		return nil, l.err
	}
	return l.tokens, nil
}

// next reads the next token from l.src at l.pos.
func (l *lexer) next() token {
	// Skip whitespace.
	for l.pos < len(l.src) && isWhitespace(l.src[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{Kind: tokEOF, Pos: l.pos}
	}

	start := l.pos
	ch := l.src[l.pos]

	switch {
	case ch == '(':
		l.pos++
		return token{Kind: tokLParen, Val: "(", Pos: start}
	case ch == ')':
		l.pos++
		return token{Kind: tokRParen, Val: ")", Pos: start}
	case ch == '~':
		l.pos++
		return token{Kind: tokTilde, Val: "~", Pos: start}
	case ch == '!' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '=':
		l.pos += 2
		return token{Kind: tokNotEq, Val: "!=", Pos: start}
	case ch == '!':
		l.pos++
		return token{Kind: tokNot, Val: "!", Pos: start}
	case ch == '&' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '&':
		l.pos += 2
		return token{Kind: tokAnd, Val: "&&", Pos: start}
	case ch == '|' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '|':
		l.pos += 2
		return token{Kind: tokOr, Val: "||", Pos: start}
	case ch == '=' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '=':
		l.pos += 2
		return token{Kind: tokEqEq, Val: "==", Pos: start}
	case ch == '<' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '=':
		l.pos += 2
		return token{Kind: tokLE, Val: "<=", Pos: start}
	case ch == '<':
		l.pos++
		return token{Kind: tokLT, Val: "<", Pos: start}
	case ch == '>' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '=':
		l.pos += 2
		return token{Kind: tokGE, Val: ">=", Pos: start}
	case ch == '>':
		l.pos++
		return token{Kind: tokGT, Val: ">", Pos: start}
	case ch == '"':
		return l.lexString(start)
	case isDigit(ch):
		return l.lexNumber(start)
	case isWordChar(ch):
		return l.lexWord(start)
	default:
		l.err = parseErr(start, "unexpected character %q", ch)
		return token{Kind: tokError, Pos: start}
	}
}

func (l *lexer) lexString(start int) token {
	l.pos++ // consume opening "
	var sb strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '"' {
			l.pos++ // consume closing "
			return token{Kind: tokString, Val: sb.String(), Pos: start}
		}
		if ch == '\\' {
			l.pos++
			if l.pos >= len(l.src) {
				l.err = parseErr(l.pos, "unexpected end of string after backslash")
				return token{Kind: tokError, Pos: start}
			}
			switch l.src[l.pos] {
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				l.err = parseErr(l.pos, "invalid escape \\%c", l.src[l.pos])
				return token{Kind: tokError, Pos: start}
			}
			l.pos++
			continue
		}
		sb.WriteByte(ch)
		l.pos++
	}
	l.err = parseErr(start, "unterminated string")
	return token{Kind: tokError, Pos: start}
}

func (l *lexer) lexNumber(start int) token {
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.pos++
	}
	return token{Kind: tokNumber, Val: l.src[start:l.pos], Pos: start}
}

func (l *lexer) lexWord(start int) token {
	for l.pos < len(l.src) && isWordChar(l.src[l.pos]) {
		l.pos++
	}
	return token{Kind: tokWord, Val: l.src[start:l.pos], Pos: start}
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
