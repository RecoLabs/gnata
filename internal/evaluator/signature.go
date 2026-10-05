package evaluator

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/recolabs/gnata/internal/parser"
)

// NewSignedBuiltin wraps the builtin name with its jsonata-js signature and
// arity, the number of parameters its jsonata-js implementation declares.
// validate turns on jsonata-js's argument validation; otherwise the
// signature only fills arguments left to the context.
func NewSignedBuiltin(name string, fn EnvAwareBuiltin, sig string, arity int, validate bool) (*SignedBuiltin, error) {
	sb := &SignedBuiltin{Name: name, Fn: fn, Sig: sig, Arity: arity}
	if sig == "" {
		return sb, nil
	}
	specs, err := parser.ParseSig(sig)
	if err != nil {
		return nil, err
	}
	sb.Signature = compileSignature(specs)
	sb.Validate = validate
	return sb, nil
}

// checkCallArgs prepares the arguments of a call to fn as jsonata-js's
// validateArguments does: a typed lambda's or validated builtin's signature
// validates them, and any other signature fills a missing context argument
// from focus.
func checkCallArgs(fn any, args []any, focus any) ([]any, error) {
	var (
		sig      *Signature
		validate bool
	)
	switch f := fn.(type) {
	case *SignedBuiltin:
		sig, validate = f.Signature, f.Validate
	case *Lambda:
		sig, validate = f.Signature, true
	}
	switch {
	case sig == nil:
		return args, nil
	case validate:
		return sig.Validate(args, focus)
	default:
		return sig.Inject(args, focus)
	}
}

// Signature is a compiled jsonata-js function signature, such as
// "s-nn?:s". It follows jsonata-js's signature.validate: each argument maps
// to a type symbol, and the symbols are matched against the parameters the
// way its greedy backtracking regex does. Inject only fills arguments left to
// the context value (the '-' marker); Validate also checks and coerces the
// arguments.
//
// https://github.com/jsonata-js/jsonata/blob/v2.2.2/src/signature.js
type Signature struct {
	params     []sigParam
	variadic   bool // some parameter is '+'
	hasContext bool // some parameter is '-'
}

type sigParam struct {
	symbols  uint8 // accepted type symbols (see symbolBit), including 'm' for undefined where allowed
	optional bool  // '?', or '-', which jsonata-js turns into '?'
	variadic bool  // '+'
	lazy     bool  // '+' with '?' or '-', which jsonata-js's regex makes "+?"
	context  bool  // '-'
	array    bool  // type 'a', whose non-array arguments are wrapped in an array
	subtype  byte  // the content type of an array, or 0
}

// compileSignature compiles the parameters of a jsonata-js signature.
func compileSignature(specs []parser.ParamSpec) *Signature {
	params := make([]sigParam, len(specs))
	for i, spec := range specs {
		lazy := spec.Optional || spec.Context
		params[i] = sigParam{
			symbols:  paramSymbols(spec.Types),
			optional: lazy && !spec.Variadic,
			variadic: spec.Variadic,
			lazy:     lazy && spec.Variadic,
			context:  spec.Context,
			array:    len(spec.Types) == 1 && spec.Types[0] == 'a',
			subtype:  spec.ContentType,
		}
	}
	return &Signature{
		params:     params,
		variadic:   slices.ContainsFunc(params, func(p sigParam) bool { return p.variadic }),
		hasContext: slices.ContainsFunc(params, func(p sigParam) bool { return p.context }),
	}
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
// larger table is passed on unfilled and unvalidated.
const maxMatchCells = 1 << 20

// scratch returns buf resized to n, allocating only when buf is too small.
func scratch[T any](buf []T, n int) []T {
	if n > cap(buf) {
		return make([]T, n)
	}
	return buf[:n]
}

// Inject returns args with the focus inserted where the signature takes the
// context value. It returns args unchanged when no context argument is
// missing or when args do not match the signature, leaving the function to
// report the mismatch. It raises T0411 when the focus has the wrong type.
func (s *Signature) Inject(args []any, focus any) ([]any, error) {
	if !s.hasContext || !s.fits(len(args)) {
		return args, nil
	}
	var countsBuf [smallMatch]int
	counts, ok := s.match(args, scratch(countsBuf[:], len(s.params)))
	if !ok {
		return args, nil
	}
	var injected []any
	argIndex := 0
	for i, param := range s.params {
		if counts[i] == 0 && param.context {
			if err := checkFocus(param, focus, argIndex); err != nil {
				return nil, err
			}
			if injected == nil {
				injected = copyPrefix(args, argIndex)
			}
			injected = append(injected, focus)
			continue
		}
		if injected != nil {
			injected = append(injected, args[argIndex:argIndex+counts[i]]...)
		}
		argIndex += counts[i]
	}
	if injected == nil {
		return args, nil
	}
	return injected, nil
}

// Validate returns the arguments jsonata-js's signature.validate passes to
// the function: the focus where a '-' parameter matches nothing, a non-array
// argument of an 'a' parameter wrapped in an array, and otherwise args by
// position, so a parameter that matches nothing still takes the next
// argument. It raises T0410 when args do not match the signature, T0411 when
// the focus has the wrong type, and T0412 when an array has the wrong
// content type.
func (s *Signature) Validate(args []any, focus any) ([]any, error) {
	if !s.fits(len(args)) {
		return args, nil
	}
	var countsBuf [smallMatch]int
	counts, ok := s.match(args, scratch(countsBuf[:], len(s.params)))
	if !ok {
		return nil, s.mismatchError(args)
	}
	// The symbols the regex matched are at symbolIndex, but jsonata-js
	// takes the arguments by position, at argIndex, which runs ahead once a
	// parameter that matched nothing has taken one. out stays nil while the
	// arguments passed are args[:argIndex], where an index past the end
	// stands for a trailing undefined, which no function can tell apart from
	// a missing argument.
	var out []any
	symbolIndex, argIndex := 0, 0
	pass := func(v any, unchanged bool) {
		if out == nil {
			if unchanged {
				argIndex++
				return
			}
			out = copyPrefix(args, argIndex)
		}
		out = append(out, v)
		argIndex++
	}
	for i, param := range s.params {
		if counts[i] == 0 && param.context {
			if err := checkFocus(param, focus, argIndex); err != nil {
				return nil, err
			}
			if out == nil {
				out = copyPrefix(args, argIndex)
			}
			out = append(out, focus)
			continue
		}
		if counts[i] == 0 {
			var arg any
			if argIndex < len(args) {
				arg = args[argIndex]
			}
			pass(arg, true)
			continue
		}
		for _, matched := range args[symbolIndex : symbolIndex+counts[i]] {
			symbol := sigSymbol(matched)
			var arg any
			if argIndex < len(args) {
				arg = args[argIndex]
			}
			if !param.array {
				pass(arg, true)
				continue
			}
			value, err := param.arrayArg(arg, symbol, counts[i] == 1, argIndex)
			if err != nil {
				return nil, err
			}
			pass(value, symbol == 'a' || symbol == 'm' && arg == nil)
		}
		symbolIndex += counts[i]
	}
	if out == nil {
		return args, nil
	}
	return out, nil
}

// copyPrefix returns a copy of args[:n], with undefined for indexes past the
// end of args, with room for the rest of args.
func copyPrefix(args []any, n int) []any {
	out := append(make([]any, 0, max(len(args), n)+1), args[:min(n, len(args))]...)
	for len(out) < n {
		out = append(out, nil)
	}
	return out
}

// arrayArg returns the argument an 'a' parameter passes for arg, whose
// matched symbol is symbol: undefined stays undefined, an array is checked
// against the content type as jsonata-js does (every item has the type of
// the first), and anything else becomes a one-item array. single reports
// whether the parameter matched just this argument. jsonata-js checks the
// content of whatever value is at the argument's position, which after a
// skipped parameter can be a string or an object with a length; only an
// array is checked here.
func (param sigParam) arrayArg(arg any, symbol byte, single bool, argIndex int) (any, error) {
	switch symbol {
	case 'm':
		return nil, nil
	case 'a':
		if items, isArray := AsArray(arg); isArray && param.subtype != 0 && len(items) > 0 {
			first := sigSymbol(items[0])
			if first != param.subtype || slices.ContainsFunc(items, func(item any) bool { return sigSymbol(item) != first }) {
				return nil, contentTypeError(param, argIndex)
			}
		}
		return arg, nil
	}
	if param.subtype != 0 && (!single || symbol != param.subtype) {
		return nil, contentTypeError(param, argIndex)
	}
	return []any{arg}, nil
}

// checkFocus raises T0411 when focus cannot fill the context parameter at
// argument index argIndex.
func checkFocus(param sigParam, focus any, argIndex int) error {
	if param.symbols&symbolBit(focusSymbol(focus)) != 0 {
		return nil
	}
	return &JSONataError{
		Code:    "T0411",
		Message: fmt.Sprintf("context value is not a compatible type with argument %d", argIndex+1),
	}
}

func contentTypeError(param sigParam, argIndex int) error {
	return &JSONataError{
		Code:    "T0412",
		Message: fmt.Sprintf("argument %d must be an array of %c", argIndex+1, param.subtype),
	}
}

// mismatchError reports the argument jsonata-js blames when args do not
// match: it matches ever longer prefixes of the parameters against the start
// of the arguments, and blames the one after the longest prefix's match.
// Whether a prefix matches only gets less likely as it grows, so a binary
// search finds the longest.
func (s *Signature) mismatchError(args []any) error {
	symbols := argSymbols(args, nil)
	longest := 0
	for low, high := 1, len(s.params); low <= high; {
		mid := (low + high) / 2
		if _, ok := s.solve(symbols, mid, false, nil); ok {
			longest, low = mid, mid+1
		} else {
			high = mid - 1
		}
	}
	taken, _ := s.solve(symbols, longest, false, nil)
	return &JSONataError{
		Code:    "T0410",
		Message: fmt.Sprintf("argument %d does not match function signature", sum(taken)+1),
	}
}

func sum(counts []int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

// argSymbols returns the type symbol of each argument, in buf when it is
// large enough.
func argSymbols(args []any, buf []uint8) []uint8 {
	symbols := scratch(buf, len(args))
	for i, arg := range args {
		symbols[i] = symbolBit(sigSymbol(arg))
	}
	return symbols
}

// fits reports whether matching nArgs arguments needs a table within
// maxMatchCells.
func (s *Signature) fits(nArgs int) bool {
	nParams := len(s.params)
	if s.variadic {
		return (nParams+1)*(nArgs+1) <= maxMatchCells
	}
	return nArgs > nParams || (nParams+1)*(nParams-nArgs+1) <= maxMatchCells
}

// match returns how many arguments each parameter takes, choosing as the
// jsonata-js regex does: each parameter takes as many arguments as it can
// (as few, for a lazy one) while the rest still match.
func (s *Signature) match(args []any, counts []int) ([]int, bool) {
	var symbolsBuf [smallMatch]uint8
	symbols := argSymbols(args, symbolsBuf[:])
	if !s.variadic {
		return s.matchFixed(symbols, counts)
	}
	return s.solve(symbols, len(s.params), true, counts)
}

// solve matches the arguments with the given symbols against the first
// nParams parameters, as the regex of those parameters does: anchored, it
// must take every argument; unanchored, as many from the start as it takes.
// A table of which suffixes of the arguments match which suffixes of the
// parameters replaces backtracking.
func (s *Signature) solve(symbols []uint8, nParams int, anchored bool, counts []int) ([]int, bool) {
	nArgs := len(symbols)
	width := nArgs + 1
	if (nParams+1)*width > maxMatchCells {
		return nil, false
	}
	// reach[p*width+a] reports whether args[a:] match params[p:nParams];
	// filling it is linear in params × args.
	var reachBuf [(smallMatch + 1) * (smallMatch + 1)]bool
	reach := scratch(reachBuf[:], (nParams+1)*width)
	for a := range width {
		reach[nParams*width+a] = !anchored || a == nArgs
	}
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
	for p := range nParams {
		param := s.params[p]
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
func (s *Signature) matchFixed(symbols []uint8, counts []int) ([]int, bool) {
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
