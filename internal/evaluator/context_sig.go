package evaluator

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/recolabs/gnata/internal/parser"
)

// ContextSig finds the arguments a call leaves to the context value, the '-'
// marker in a jsonata-js signature such as "s-nn?:s". It follows the argument
// matching of jsonata-js's signature.validate: each argument maps to a type
// symbol, the symbols are matched against the parameters the way its greedy
// backtracking regex does, and a context parameter that matches nothing takes
// the focus. Optional parameters that match nothing are left out rather than
// padded with undefined.
//
// https://github.com/jsonata-js/jsonata/blob/v2.2.2/src/signature.js
type ContextSig struct {
	params   []contextParam
	variadic bool // some parameter is '+'
	// zeroArgs are the symbols of the context parameters, which take the
	// focus in a call without arguments; nil when such a call does not match.
	zeroArgs []uint8
}

type contextParam struct {
	symbols  uint8 // accepted type symbols (see symbolBit), including 'm' for undefined where allowed
	optional bool  // '?', or '-' when the argument is left to the context
	variadic bool  // '+'
	lazy     bool  // '+' with '-', which jsonata-js's regex makes "+?"
	context  bool  // '-'
}

// newContextSig compiles the parameters of a jsonata-js signature. It
// returns nil when no parameter defaults to the context.
func newContextSig(specs []parser.ParamSpec) *ContextSig {
	if !slices.ContainsFunc(specs, func(spec parser.ParamSpec) bool { return spec.Context }) {
		return nil
	}
	params := make([]contextParam, len(specs))
	for i, spec := range specs {
		params[i] = contextParam{
			symbols: paramSymbols(spec.Types),
			// jsonata-js appends '?' for '-', which makes a '+' parameter lazy
			// rather than optional.
			optional: spec.Optional || (spec.Context && !spec.Variadic),
			variadic: spec.Variadic,
			lazy:     spec.Variadic && spec.Context,
			context:  spec.Context,
		}
	}
	sig := &ContextSig{params: params, variadic: slices.ContainsFunc(params, func(p contextParam) bool { return p.variadic })}
	if !slices.ContainsFunc(params, func(p contextParam) bool { return !p.optional }) {
		for _, param := range params {
			if param.context {
				sig.zeroArgs = append(sig.zeroArgs, param.symbols)
			}
		}
	}
	return sig
}

// contextArgs marks the arguments Inject filled from the context.
type contextArgs struct {
	low  uint64 // arguments 0 to 63
	high []bool // arguments from 64 on, for absurdly wide signatures
}

func (c *contextArgs) set(i int) {
	if i < 64 {
		c.low |= 1 << i
		return
	}
	for len(c.high) <= i-64 {
		c.high = append(c.high, false)
	}
	c.high[i-64] = true
}

func (c *contextArgs) has(i int) bool {
	if i < 64 {
		return c.low&(1<<i) != 0
	}
	return i-64 < len(c.high) && c.high[i-64]
}

// sigSymbols are jsonata-js's type symbols, in the order of their bits;
// sigSymbol returns only these.
const sigSymbols = "asnblfom"

// allSymbols has the bit of every symbol in sigSymbols.
const allSymbols uint8 = 1<<len(sigSymbols) - 1

// symbolBit returns the bit for a jsonata-js type symbol, so the symbols a
// parameter accepts fit in one byte.
func symbolBit(symbol byte) uint8 {
	return 1 << strings.IndexByte(sigSymbols, symbol)
}

// paramSymbols returns the argument symbols a parameter of the given types
// accepts, as the character classes jsonata-js builds for its regex: every
// type also accepts undefined ('m'), except a function.
func paramSymbols(types []byte) uint8 {
	if len(types) == 1 {
		switch types[0] {
		case 'a', 'x':
			return allSymbols
		case 'j':
			return allSymbols &^ symbolBit('f')
		case 'f':
			return symbolBit('f')
		case 'u':
			return symbolBit('b') | symbolBit('n') | symbolBit('s') | symbolBit('l') | symbolBit('m')
		}
	}
	bits := symbolBit('m')
	for _, t := range types {
		if strings.IndexByte(sigSymbols, t) >= 0 {
			bits |= symbolBit(t)
		}
	}
	return bits
}

// smallMatch bounds the arguments and parameters whose matching tables fit
// in stack buffers; every builtin call fits, so matching does not allocate.
const smallMatch = 8

// maxMatchCells bounds a matching table, so an absurdly wide lambda
// signature cannot allocate without limit on every call; a call needing a
// larger table is left to signature validation unfilled.
const maxMatchCells = 1 << 20

// scratch returns buf resized to n, allocating only when buf is too small.
func scratch[T any](buf []T, n int) []T {
	if n > cap(buf) {
		return make([]T, n)
	}
	return buf[:n]
}

// Inject returns args with the focus inserted where the signature takes the
// context value, and which of the returned arguments are the focus. It
// returns args unchanged when no context argument is missing or when args
// do not match the signature, leaving the function to report the mismatch.
// It raises T0411 when the focus has the wrong type.
func (s *ContextSig) Inject(args []any, focus any) ([]any, contextArgs, error) {
	if len(args) == 0 {
		return s.injectAlone(focus)
	}
	if !s.variadic && (len(args) >= len(s.params) || s.fillsInOrder(args)) {
		return args, contextArgs{}, nil
	}
	return s.injectMatched(args, focus)
}

// injectMatched is Inject without its shortcuts, matching args against the
// signature.
func (s *ContextSig) injectMatched(args []any, focus any) ([]any, contextArgs, error) {
	var countsBuf [smallMatch]int
	counts, ok := s.match(args, scratch(countsBuf[:], len(s.params)))
	if !ok {
		return args, contextArgs{}, nil
	}
	missing := 0
	for i, param := range s.params {
		if counts[i] == 0 && param.context {
			missing++
		}
	}
	if missing == 0 {
		return args, contextArgs{}, nil
	}
	injected := make([]any, 0, len(args)+missing)
	var isContext contextArgs
	argIndex := 0
	for i, param := range s.params {
		if counts[i] == 0 && param.context {
			if err := focusError(param.symbols, focus, argIndex); err != nil {
				return nil, contextArgs{}, err
			}
			isContext.set(len(injected))
			injected = append(injected, focus)
			continue
		}
		injected = append(injected, args[argIndex:argIndex+counts[i]]...)
		argIndex += counts[i]
	}
	return injected, isContext, nil
}

// fillsInOrder reports whether args, at most one per parameter, match the
// parameters in order and every parameter after them is optional and not
// left to the context. The jsonata-js regex, taking each argument greedily,
// then matches args that way, so the call needs no context. A signature
// without '+' with as many arguments as parameters never does either: each
// parameter takes one, or args do not match.
func (s *ContextSig) fillsInOrder(args []any) bool {
	for i, arg := range args {
		if s.params[i].symbols&symbolBit(sigSymbol(arg)) == 0 {
			return false
		}
	}
	for _, param := range s.params[len(args):] {
		if param.context || !param.optional {
			return false
		}
	}
	return true
}

// injectAlone is Inject for a call without arguments, which matches only
// when every parameter is optional, and then passes the focus to every
// context parameter.
func (s *ContextSig) injectAlone(focus any) ([]any, contextArgs, error) {
	if len(s.zeroArgs) == 0 {
		return nil, contextArgs{}, nil
	}
	injected := make([]any, len(s.zeroArgs))
	var isContext contextArgs
	for i, symbols := range s.zeroArgs {
		if err := focusError(symbols, focus, 0); err != nil {
			return nil, contextArgs{}, err
		}
		injected[i] = focus
		isContext.set(i)
	}
	return injected, isContext, nil
}

// focusError raises T0411 when focus cannot take a context parameter
// accepting symbols, the argument after argIndex arguments.
func focusError(symbols uint8, focus any, argIndex int) error {
	if symbols&symbolBit(focusSymbol(focus)) != 0 {
		return nil
	}
	return &JSONataError{
		Code:    "T0411",
		Message: fmt.Sprintf("context value is not a compatible type with argument %d", argIndex+1),
	}
}

// match appends to counts how many arguments each parameter takes, choosing
// as the jsonata-js regex does: each parameter takes as many arguments as it
// can (as few, for a lazy one) while the rest still match. A table of which
// suffixes of args match which suffixes of the parameters replaces
// backtracking.
func (s *ContextSig) match(args []any, counts []int) ([]int, bool) {
	var symbolsBuf [smallMatch]uint8
	symbols := scratch(symbolsBuf[:], len(args))
	for i, arg := range args {
		symbols[i] = symbolBit(sigSymbol(arg))
	}
	if !s.variadic {
		return s.matchFixed(symbols, counts)
	}
	nArgs, nParams := len(symbols), len(s.params)
	width := nArgs + 1
	if (nParams+1)*width > maxMatchCells {
		return nil, false
	}
	// reach[p*width+a] reports whether args[a:] match params[p:]; filling it
	// is linear in params × args.
	var reachBuf [(smallMatch + 1) * (smallMatch + 1)]bool
	reach := scratch(reachBuf[:], (nParams+1)*width)
	reach[nParams*width+nArgs] = true
	for p := nParams - 1; p >= 0; p-- {
		param := s.params[p]
		for a := nArgs; a >= 0; a-- {
			matched := param.optional && reach[(p+1)*width+a]
			if a < nArgs && param.symbols&symbols[a] != 0 {
				// Take args[a], then stop or, for '+', take more.
				matched = matched || reach[(p+1)*width+a+1] || (param.variadic && reach[p*width+a+1])
			}
			reach[p*width+a] = matched
		}
	}
	if !reach[0] {
		return nil, false
	}
	counts = counts[:0]
	a := 0
	for p, param := range s.params {
		most := 0
		for a+most < nArgs && (most == 0 || param.variadic) && param.symbols&symbols[a+most] != 0 {
			most++
		}
		least := 1
		if param.optional {
			least = 0
		}
		n := -1
		for take := most; take >= least; take-- {
			if reach[(p+1)*width+a+take] && (n < 0 || param.lazy) {
				n = take
			}
		}
		counts = append(counts, n)
		a += n
	}
	return counts, true
}

// matchFixed is match for a signature without '+', where each parameter
// takes at most one argument. Its table is indexed by how many parameters
// were skipped, at most len(params) - len(args), so a wide signature called
// with most of its arguments stays linear.
func (s *ContextSig) matchFixed(symbols []uint8, counts []int) ([]int, bool) {
	nArgs, nParams := len(symbols), len(s.params)
	skips := nParams - nArgs
	if skips < 0 {
		return nil, false
	}
	width := skips + 1
	if (nParams+1)*width > maxMatchCells {
		return nil, false
	}
	// reach[p*width+k] reports whether params[p:] match the arguments left
	// after skipping k of params[:p], that is args[p-k:].
	var reachBuf [(smallMatch + 1) * (smallMatch + 1)]bool
	reach := scratch(reachBuf[:], (nParams+1)*width)
	reach[nParams*width+skips] = true
	for p := nParams - 1; p >= 0; p-- {
		param := s.params[p]
		for k := 0; k <= skips && k <= p; k++ {
			a := p - k
			if a > nArgs {
				continue
			}
			matched := a < nArgs && param.symbols&symbols[a] != 0 && reach[(p+1)*width+k]
			if !matched && param.optional && k < skips {
				matched = reach[(p+1)*width+k+1]
			}
			reach[p*width+k] = matched
		}
	}
	if !reach[0] {
		return nil, false
	}
	counts = counts[:0]
	k := 0
	for p, param := range s.params {
		a := p - k
		if a < nArgs && param.symbols&symbols[a] != 0 && reach[(p+1)*width+k] {
			counts = append(counts, 1)
			continue
		}
		counts = append(counts, 0)
		k++
	}
	return counts, true
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
