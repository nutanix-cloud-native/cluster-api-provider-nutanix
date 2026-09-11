/*
Copyright 2026 Nutanix

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package simulator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// This file implements the subset of OData $filter that CAPX sends:
//
//	name eq 'foo'
//	key eq 'k' and value eq 'v'
//	name eq 'x' and clusterExtId eq 'y'
//	entitiesAffected/any(a:a/extId eq 'u') and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED') and (operation eq 'kImageDelete')
//
// Grammar:
//
//	expr    := and ( 'or' and )*
//	and     := unary ( 'and' unary )*
//	unary   := 'not' unary | primary
//	primary := '(' expr ')' | operand [ cmpop operand ]
//	operand := string | number | true | false | null | path | path '/any(' ident ':' expr ')' | path '/all(' ident ':' expr ')'
//
// Entities are evaluated as their JSON representation, so property names are
// the JSON field names of the SDK models and enums compare as their string
// spelling (the enum type prefix of a typed literal is ignored).

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokLParen
	tokRParen
	tokColon
	tokComma
)

type token struct {
	kind tokenKind
	text string
}

type lexer struct {
	in  string
	pos int
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.in) && l.in[l.pos] == ' ' {
		l.pos++
	}
	if l.pos >= len(l.in) {
		return token{kind: tokEOF}, nil
	}
	c := l.in[l.pos]
	switch {
	case c == '(':
		l.pos++
		return token{kind: tokLParen, text: "("}, nil
	case c == ')':
		l.pos++
		return token{kind: tokRParen, text: ")"}, nil
	case c == ':':
		l.pos++
		return token{kind: tokColon, text: ":"}, nil
	case c == ',':
		l.pos++
		return token{kind: tokComma, text: ","}, nil
	case c == '\'':
		return l.stringLiteral()
	case c == '-' || (c >= '0' && c <= '9'):
		return l.number(), nil
	case isIdentStart(c):
		return l.identOrEnum()
	default:
		return token{}, fmt.Errorf("unexpected character %q at %d", c, l.pos)
	}
}

func (l *lexer) stringLiteral() (token, error) {
	// l.in[l.pos] == '\''
	l.pos++
	var sb strings.Builder
	for l.pos < len(l.in) {
		c := l.in[l.pos]
		if c == '\'' {
			if l.pos+1 < len(l.in) && l.in[l.pos+1] == '\'' {
				sb.WriteByte('\'')
				l.pos += 2
				continue
			}
			l.pos++
			return token{kind: tokString, text: sb.String()}, nil
		}
		sb.WriteByte(c)
		l.pos++
	}
	return token{}, fmt.Errorf("unterminated string literal")
}

func (l *lexer) number() token {
	start := l.pos
	l.pos++
	for l.pos < len(l.in) && (l.in[l.pos] == '.' || (l.in[l.pos] >= '0' && l.in[l.pos] <= '9')) {
		l.pos++
	}
	return token{kind: tokNumber, text: l.in[start:l.pos]}
}

func (l *lexer) identOrEnum() (token, error) {
	start := l.pos
	for l.pos < len(l.in) && isIdentChar(l.in[l.pos]) {
		l.pos++
	}
	ident := l.in[start:l.pos]
	// A typed enum literal: Namespace.Type'VALUE'. The type prefix is dropped
	// and the value compares as a plain string.
	if l.pos < len(l.in) && l.in[l.pos] == '\'' && strings.Contains(ident, ".") {
		return l.stringLiteral()
	}
	return token{kind: tokIdent, text: ident}, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c == '.' || c == '/' || (c >= '0' && c <= '9')
}

// node is an evaluable filter expression.
type node interface {
	eval(root map[string]any, scope map[string]any) (any, error)
}

type literalNode struct{ value any }

func (n literalNode) eval(map[string]any, map[string]any) (any, error) { return n.value, nil }

type pathNode struct{ segments []string }

func (n pathNode) eval(root map[string]any, scope map[string]any) (any, error) {
	var cur any = root
	segs := n.segments
	if v, ok := scope[segs[0]]; ok {
		cur = v
		segs = segs[1:]
	}
	for _, seg := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil, nil
		}
	}
	return cur, nil
}

type lambdaNode struct {
	collection pathNode
	variable   string
	all        bool
	body       node
}

func (n lambdaNode) eval(root map[string]any, scope map[string]any) (any, error) {
	coll, err := n.collection.eval(root, scope)
	if err != nil {
		return nil, err
	}
	items, _ := coll.([]any)
	inner := make(map[string]any, len(scope)+1)
	for k, v := range scope {
		inner[k] = v
	}
	for _, item := range items {
		inner[n.variable] = item
		res, err := n.body.eval(root, inner)
		if err != nil {
			return nil, err
		}
		if truthy(res) != n.all {
			return !n.all, nil
		}
	}
	return n.all, nil
}

type binaryNode struct {
	op          string
	left, right node
}

func (n binaryNode) eval(root map[string]any, scope map[string]any) (any, error) {
	l, err := n.left.eval(root, scope)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "and":
		if !truthy(l) {
			return false, nil
		}
		r, err := n.right.eval(root, scope)
		return truthy(r), err
	case "or":
		if truthy(l) {
			return true, nil
		}
		r, err := n.right.eval(root, scope)
		return truthy(r), err
	}
	r, err := n.right.eval(root, scope)
	if err != nil {
		return nil, err
	}
	return compare(n.op, l, r)
}

type notNode struct{ inner node }

func (n notNode) eval(root map[string]any, scope map[string]any) (any, error) {
	v, err := n.inner.eval(root, scope)
	return !truthy(v), err
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func compare(op string, l, r any) (bool, error) {
	switch op {
	case "eq":
		return valuesEqual(l, r), nil
	case "ne":
		return !valuesEqual(l, r), nil
	}
	lf, lok := toFloat(l)
	rf, rok := toFloat(r)
	if !lok || !rok {
		ls, lok := l.(string)
		rs, rok := r.(string)
		if !lok || !rok {
			return false, nil
		}
		return orderResult(op, strings.Compare(ls, rs)), nil
	}
	switch {
	case lf < rf:
		return orderResult(op, -1), nil
	case lf > rf:
		return orderResult(op, 1), nil
	default:
		return orderResult(op, 0), nil
	}
}

func orderResult(op string, c int) bool {
	switch op {
	case "gt":
		return c > 0
	case "ge":
		return c >= 0
	case "lt":
		return c < 0
	case "le":
		return c <= 0
	}
	return false
}

func valuesEqual(l, r any) bool {
	if lf, ok := toFloat(l); ok {
		rf, ok := toFloat(r)
		return ok && lf == rf
	}
	switch lv := l.(type) {
	case string:
		rv, ok := r.(string)
		return ok && lv == rv
	case bool:
		rv, ok := r.(bool)
		return ok && lv == rv
	case nil:
		return r == nil
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

type parser struct {
	lex  *lexer
	peek token
}

// parseFilter parses an OData $filter expression.
func parseFilter(input string) (node, error) {
	p := &parser{lex: &lexer{in: input}}
	if err := p.advance(); err != nil {
		return nil, err
	}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek.kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek.text)
	}
	return n, nil
}

func (p *parser) advance() error {
	t, err := p.lex.next()
	if err != nil {
		return err
	}
	p.peek = t
	return nil
}

func (p *parser) acceptIdent(word string) (bool, error) {
	if p.peek.kind == tokIdent && p.peek.text == word {
		return true, p.advance()
	}
	return false, nil
}

func (p *parser) expect(kind tokenKind, what string) error {
	if p.peek.kind != kind {
		return fmt.Errorf("expected %s, got %q", what, p.peek.text)
	}
	return p.advance()
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		ok, err := p.acceptIdent("or")
		if err != nil || !ok {
			return left, err
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: "or", left: left, right: right}
	}
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		ok, err := p.acceptIdent("and")
		if err != nil || !ok {
			return left, err
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: "and", left: left, right: right}
	}
}

func (p *parser) parseUnary() (node, error) {
	ok, err := p.acceptIdent("not")
	if err != nil {
		return nil, err
	}
	if ok {
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notNode{inner: inner}, nil
	}
	return p.parsePrimary()
}

var comparisonOps = map[string]bool{"eq": true, "ne": true, "gt": true, "ge": true, "lt": true, "le": true}

func (p *parser) parsePrimary() (node, error) {
	if p.peek.kind == tokLParen {
		if err := p.advance(); err != nil {
			return nil, err
		}
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	if p.peek.kind == tokIdent && comparisonOps[p.peek.text] {
		op := p.peek.text
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		return binaryNode{op: op, left: left, right: right}, nil
	}
	return left, nil
}

func (p *parser) parseOperand() (node, error) {
	t := p.peek
	switch t.kind {
	case tokString:
		return literalNode{value: t.text}, p.advance()
	case tokNumber:
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", t.text)
		}
		return literalNode{value: f}, p.advance()
	case tokIdent:
		switch t.text {
		case "true":
			return literalNode{value: true}, p.advance()
		case "false":
			return literalNode{value: false}, p.advance()
		case "null":
			return literalNode{value: nil}, p.advance()
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		return p.pathOrLambda(t.text)
	default:
		return nil, fmt.Errorf("unexpected token %q", t.text)
	}
}

func (p *parser) pathOrLambda(path string) (node, error) {
	segments := strings.Split(path, "/")
	last := segments[len(segments)-1]
	if (last == "any" || last == "all") && p.peek.kind == tokLParen && len(segments) > 1 {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.peek.kind != tokIdent {
			return nil, fmt.Errorf("expected lambda variable, got %q", p.peek.text)
		}
		variable := p.peek.text
		if err := p.advance(); err != nil {
			return nil, err
		}
		if err := p.expect(tokColon, "':'"); err != nil {
			return nil, err
		}
		body, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		return lambdaNode{
			collection: pathNode{segments: segments[:len(segments)-1]},
			variable:   variable,
			all:        last == "all",
			body:       body,
		}, nil
	}
	return pathNode{segments: segments}, nil
}

// filterMatcher evaluates a parsed $filter against entities.
type filterMatcher struct {
	expr node
}

// newFilterMatcher parses filter; an empty filter matches everything.
func newFilterMatcher(filter string) (*filterMatcher, error) {
	if strings.TrimSpace(filter) == "" {
		return &filterMatcher{}, nil
	}
	expr, err := parseFilter(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid $filter %q: %w", filter, err)
	}
	return &filterMatcher{expr: expr}, nil
}

// matches reports whether entity (any JSON-marshallable value) satisfies the
// filter.
func (m *filterMatcher) matches(entity any) (bool, error) {
	if m.expr == nil {
		return true, nil
	}
	raw, err := json.Marshal(entity)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, err
	}
	res, err := m.expr.eval(doc, nil)
	if err != nil {
		return false, err
	}
	return truthy(res), nil
}
