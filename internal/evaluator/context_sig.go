package evaluator

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/recolabs/gnata/internal/parser"
)

// maxContextParams bounds the parameters of a ContextSig so matching can
// record them in a fixed-size array; builtin signatures have at most four.
const maxContextParams = 8

// ContextSig finds the argument a direct call leaves to the context value,
// the '-' marker in a jsonata-js signature such as "s-nn?:s". It follows the
// argument matching of jsonata-js's signature.validate: each argument maps to
// a type symbol, the symbols are matched against the parameters the way its
// backtracking regex does, and a context parameter that matches nothing takes
// the focus. Every builtin signature puts its optional parameters last, so the
// focus is inserted rather than every parameter re-aligned.
//
// https://github.com/jsonata-js/jsonata/blob/v2.1.0/src/signature.js
type ContextSig struct {
	params []contextParam
}

type contextParam struct {
	symbols  string // accepted type symbols, including 'm' for undefined where allowed
	optional bool   // '?', or '-' when the argument is left to the context
	variadic bool   // '+'
	context  bool   // '-'
}

// newContextSig compiles the parameters of a jsonata-js signature. It
// returns nil when no parameter defaults to the context.
func newContextSig(specs []parser.ParamSpec) (*ContextSig, error) {
	if len(specs) > maxContextParams {
		return nil, fmt.Errorf("signature has more than %d parameters", maxContextParams)
	}
	if !slices.ContainsFunc(specs, func(spec parser.ParamSpec) bool { return spec.Context }) {
		return nil, nil
	}
	params := make([]contextParam, len(specs))
	for i, spec := range specs {
		params[i] = contextParam{
			symbols:  paramSymbols(spec.Types),
			optional: spec.Optional || spec.Context,
			variadic: spec.Variadic,
			context:  spec.Context,
		}
	}
	return &ContextSig{params: params}, nil
}

// paramSymbols returns the argument symbols a parameter of the given types
// accepts, as the character classes jsonata-js builds for its regex.
func paramSymbols(types []byte) string {
	if len(types) > 1 {
		return string(types) + "m"
	}
	switch t := types[0]; t {
	case 'a', 'x':
		return "asnblfom"
	case 'j':
		return "asnblom"
	case 'f':
		return "f"
	case 'u':
		return "bnslm"
	default:
		return string(t) + "m"
	}
}

// Inject returns args with the focus inserted where the signature takes the
// context value. It returns args unchanged when no context argument is
// missing or when args do not match the signature, leaving the builtin to
// report the mismatch. It raises T0411 when the focus has the wrong type.
func (s *ContextSig) Inject(args []any, focus any) ([]any, error) {
	var counts [maxContextParams]int
	if !s.match(args, 0, 0, &counts) {
		return args, nil
	}
	argIndex := 0
	for i, param := range s.params {
		if counts[i] > 0 || !param.context {
			argIndex += counts[i]
			continue
		}
		if strings.IndexByte(param.symbols, focusSymbol(focus)) < 0 {
			return nil, &JSONataError{
				Code:    "T0411",
				Message: fmt.Sprintf("context value is not a compatible type with argument %d", argIndex+1),
			}
		}
		return slices.Insert(slices.Clone(args), argIndex, focus), nil
	}
	return args, nil
}

// match reports whether args[argIndex:] match params[paramIndex:], recording
// how many arguments each parameter takes. Like the greedy jsonata-js regex,
// a parameter first takes as many arguments as it can, then backtracks.
func (s *ContextSig) match(args []any, argIndex, paramIndex int, counts *[maxContextParams]int) bool {
	if paramIndex == len(s.params) {
		return argIndex == len(args)
	}
	param := s.params[paramIndex]
	most := 0
	for argIndex+most < len(args) && (most == 0 || param.variadic) &&
		strings.IndexByte(param.symbols, sigSymbol(args[argIndex+most])) >= 0 {
		most++
	}
	least := 1
	if param.optional {
		least = 0
	}
	for n := most; n >= least; n-- {
		counts[paramIndex] = n
		if s.match(args, argIndex+n, paramIndex+1, counts) {
			return true
		}
	}
	return false
}

// isRegexValue reports whether m has exactly the keys of the map a regex
// literal evaluates to, so input data with a "pattern" field stays an object.
func isRegexValue(m map[string]any) bool {
	_, hasPattern := m["pattern"].(string)
	_, hasFlags := m["flags"].(string)
	return hasPattern && hasFlags && len(m) == 2
}

// focusSymbol is sigSymbol for the focus, where a map is always an object:
// input data can have exactly a regex's keys, and builtins given a regex as
// the context treat its map as an object anyway.
func focusSymbol(focus any) byte {
	if _, isMap := focus.(map[string]any); isMap {
		return 'o'
	}
	return sigSymbol(focus)
}

// sigSymbol returns the jsonata-js signature symbol for a value. A regex,
// which gnata represents as a pattern map, is a function in jsonata-js.
func sigSymbol(v any) byte {
	switch val := v.(type) {
	case string:
		return 's'
	case float64, json.Number:
		return 'n'
	case bool:
		return 'b'
	case JSONNull:
		return 'l'
	case []any, ConsArray, *Sequence:
		return 'a'
	case *OrderedMap:
		return 'o'
	case map[string]any:
		if isRegexValue(val) {
			return 'f'
		}
		return 'o'
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		return 'f'
	}
	return 'm'
}
