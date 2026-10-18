package evaluator

import "math/bits"

// maxInjectCombinations bounds the argument type combinations MayInject
// tries; past it, it assumes the context may be injected.
const maxInjectCombinations = 1 << 12

// ArgSymbols returns the symbols an argument may have at run time: the
// literal's own symbol for a known one ('s', 'n', 'b', 'l', 'a', 'o', 'f'),
// or any symbol, undefined included, for 0.
func ArgSymbols(symbol byte) uint8 {
	if bit := symbolBit(symbol); bit != 0 {
		return bit
	}
	return allSymbols
}

// MayInject reports whether Inject could insert the context value for a
// call whose argument i has one of the symbols in argSymbols[i] (see
// ArgSymbols). It tries every combination the way Inject matches it.
func (s *Signature) MayInject(argSymbols []uint8) bool {
	if !s.hasContext || !s.fits(len(argSymbols)) || !s.variadic && len(argSymbols) >= len(s.params) {
		// Every argument fills a parameter in order, or none can match.
		return false
	}
	combinations := 1
	for _, set := range argSymbols {
		combinations *= bits.OnesCount8(set)
		if combinations > maxInjectCombinations {
			return true
		}
	}
	symbols := make([]uint8, len(argSymbols))
	counts := make([]int, len(s.params))
	var try func(i int) bool
	try = func(i int) bool {
		if i == len(symbols) {
			return s.injects(symbols, counts)
		}
		for set := argSymbols[i]; set != 0; set &= set - 1 {
			symbols[i] = set & -set
			if try(i + 1) {
				return true
			}
		}
		return false
	}
	return try(0)
}

// injects reports whether Inject inserts the context value for arguments
// with the given symbols.
func (s *Signature) injects(symbols []uint8, counts []int) bool {
	if s.fillsInOrderSymbols(symbols) {
		return false
	}
	var ok bool
	if len(symbols) == 0 {
		ok = s.zeroMatches
		clear(counts)
	} else {
		counts, ok = s.matchSymbols(symbols, counts)
	}
	if !ok {
		return false
	}
	for i, param := range s.params {
		if counts[i] == 0 && param.context {
			return true
		}
	}
	return false
}

// fillsInOrderSymbols is fillsInOrder(args, false), for arguments with the
// given symbols.
// Its callers have already handled as many arguments as parameters or more.
func (s *Signature) fillsInOrderSymbols(symbols []uint8) bool {
	if s.variadic {
		return false
	}
	for i, symbol := range symbols {
		if s.params[i].lazy || s.params[i].symbols&symbol == 0 {
			return false
		}
	}
	for _, param := range s.params[len(symbols):] {
		if param.context || !param.optional {
			return false
		}
	}
	return true
}
