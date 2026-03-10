package parser

import (
	"fmt"
	"slices"
)

// ParamSpec describes one parameter's type constraint parsed from a function signature.
type ParamSpec struct {
	Types       []byte // accepted base types: b n s l a o f j x
	ContentType byte   // for 'a': element type constraint; 0 = any
	Optional    bool   // ? — param may be omitted
	Variadic    bool   // + — repeats; must be the last spec
}

// ParseSig parses and validates a raw function-signature string (the content
// between the outer < > brackets, as stored in Signature.Raw).
//
// Error codes:
//   - S0401 when a content-type specifier (<X>) is applied to a type other than
//     'a' (array) or 'f' (function).
//   - S0402 when a content-type specifier appears inside a union group (...),
//     or when the signature is otherwise malformed.
func ParseSig(raw string) ([]ParamSpec, error) {
	s := sigStripReturnType(raw)
	var specs []ParamSpec
	for i := 0; i < len(s); {
		var spec ParamSpec

		if s[i] == '(' {
			var err error
			if spec.Types, i, err = parseSigUnion(s, i+1); err != nil {
				return nil, err
			}
		} else {
			if !isValidSigType(s[i]) {
				return nil, parseSigError("S0402", fmt.Sprintf("unknown type specifier %q in signature", s[i]))
			}
			spec.Types = []byte{s[i]}
			i++
		}

		if i < len(s) && s[i] == '<' {
			var err error
			if spec.ContentType, i, err = parseSigContentType(s, i, spec.Types); err != nil {
				return nil, err
			}
		}

		for i < len(s) && (s[i] == '?' || s[i] == '+' || s[i] == '-') {
			switch s[i] {
			case '?':
				spec.Optional = true
			case '+':
				spec.Variadic = true
			}
			i++
		}

		specs = append(specs, spec)
	}
	return specs, nil
}

// parseSigUnion parses a union group "(type...)" starting after the '('.
func parseSigUnion(s string, i int) (types []byte, next int, _ error) {
	for i < len(s) && s[i] != ')' {
		if s[i] == '<' {
			return nil, 0, parseSigError("S0402", "content-type specifier '<' is not allowed inside a union type group")
		}
		if !isValidSigType(s[i]) {
			return nil, 0, parseSigError("S0402", fmt.Sprintf("unknown type specifier %q in signature", s[i]))
		}
		types = append(types, s[i])
		i++
	}
	if i >= len(s) {
		return nil, 0, parseSigError("S0402", "unclosed union type group in signature")
	}
	if len(types) == 0 {
		return nil, 0, parseSigError("S0402", "empty union type group in signature")
	}
	return types, i + 1, nil // +1 to consume ')'
}

// parseSigContentType parses a content-type specifier "<...>" for array/function types.
func parseSigContentType(s string, i int, types []byte) (contentType byte, next int, _ error) {
	for _, t := range types {
		if t != 'a' && t != 'f' {
			return 0, 0, parseSigError("S0401",
				fmt.Sprintf("content-type specifier '<' is not valid for type %q", t))
		}
	}
	i++ // consume '<'

	if slices.Contains(types, 'a') && i < len(s) && isValidSigType(s[i]) {
		contentType = s[i]
		i++
		if i < len(s) && s[i] == '<' {
			i = skipBrackets(s, i)
		}
	} else {
		i = skipBracketsKeepClose(s, i)
	}

	if i >= len(s) || s[i] != '>' {
		return 0, 0, parseSigError("S0402", "unclosed content-type specifier")
	}
	return contentType, i + 1, nil
}

// skipBrackets skips a nested "<...>" block including the opening '<', consuming the closing '>'.
func skipBrackets(s string, i int) int {
	depth := 1
	for i++; i < len(s) && depth > 0; i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
		}
	}
	return i
}

// skipBracketsKeepClose skips bracketed content but stops AT the closing '>' (does not consume it).
func skipBracketsKeepClose(s string, i int) int {
	depth := 1
	for i < len(s) && depth > 0 {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
		}
		if depth > 0 {
			i++
		}
	}
	return i
}

// sigStripReturnType removes the ":returntype" suffix from a raw signature string,
// finding the last ':' that is not inside a '()' union group or '<>' angle brackets.
func sigStripReturnType(s string) string {
	parenDepth := 0
	angleDepth := 0
	lastColon := -1
	for i := range len(s) {
		switch s[i] {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '<':
			angleDepth++
		case '>':
			angleDepth--
		case ':':
			if parenDepth == 0 && angleDepth == 0 {
				lastColon = i
			}
		}
	}
	if lastColon >= 0 {
		return s[:lastColon]
	}
	return s
}

func isValidSigType(c byte) bool {
	switch c {
	case 'b', 'n', 's', 'l', 'a', 'o', 'f', 'j', 'x':
		return true
	}
	return false
}

func parseSigError(code, msg string) error {
	return fmt.Errorf("JSONata error %s: %s", code, msg)
}
