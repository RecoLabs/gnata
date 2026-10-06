package evaluator

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

// NewSignedBuiltin wraps the builtin name with its jsonata-js signature and
// arity, the number of parameters its jsonata-js implementation declares.
// validate turns on the arity and type checks of processCallArgs.
func NewSignedBuiltin(name string, fn EnvAwareBuiltin, sig string, arity int, validate bool) (*SignedBuiltin, error) {
	sb := &SignedBuiltin{Name: name, Fn: fn, Sig: sig, Arity: arity}
	if sig == "" {
		return sb, nil
	}
	specs, err := parser.ParseSig(sig)
	if err != nil {
		return nil, err
	}
	sb.Context = newContextSig(specs)
	if validate {
		sb.ParsedSig = specs
	}
	return sb, nil
}

// checkCallArgs prepares the arguments of a call to fn as jsonata-js's
// validateArguments does: it fills a missing context argument from focus,
// then applies processCallArgs to a validated builtin or a typed lambda.
func checkCallArgs(fn any, args []any, focus any) (coercedArgs []any, returnUndefined bool, err error) {
	var (
		contextSig *ContextSig
		specs      []parser.ParamSpec
		validate   bool
	)
	switch f := fn.(type) {
	case *SignedBuiltin:
		contextSig, specs, validate = f.Context, f.ParsedSig, f.ParsedSig != nil
	case *Lambda:
		contextSig, specs, validate = f.Context, f.ParsedSig, f.Sig != ""
	default:
		return args, false, nil
	}
	var isContext contextArgs
	if contextSig != nil {
		if args, isContext, err = contextSig.Inject(args, focus); err != nil {
			return nil, false, err
		}
	}
	if !validate || plainlyValid(specs, args) {
		return args, false, nil
	}
	return processCallArgs(specs, args, isContext)
}

// plainlyValid reports whether processCallArgs would pass args through
// unchanged without checking them: one defined argument per parameter, each
// of a type its parameter accepts, with no array, content type or '+' to
// coerce or count.
func plainlyValid(specs []parser.ParamSpec, args []any) bool {
	if len(args) != len(specs) {
		return false
	}
	for i, spec := range specs {
		if args[i] == nil || spec.Variadic || spec.ContentType != 0 || sigContainsType(spec.Types, 'a') ||
			!sigArgMatchesTypes(args[i], spec.Types) {
			return false
		}
	}
	return true
}

// processCallArgs handles three pre-call concerns for typed lambdas:
//
//  1. Nil propagation: if any non-optional typed arg is nil (undefined) and
//     'l' (null) is not one of its accepted types, the call returns undefined
//     — matching JSONata's "undefined propagates through functions" semantics.
//
//  2. Singleton coercion: when a spec expects a<X> and the arg is a single X
//     value (not an array), the arg is wrapped in a one-element array [arg].
//
//  3. Argument type validation: delegates to validateCallArgs, which returns
//     T0410 on base-type mismatch or arity errors, and T0412 on array
//     content-type violations. ContextSig has already filled a missing
//     context argument ('-'); isContext marks those, which jsonata-js passes
//     as they are, so they are neither propagated nor coerced.
//
// It returns (coercedArgs, returnUndefined, err).
// When returnUndefined is true the caller must return (nil, nil) immediately.
func processCallArgs(specs []parser.ParamSpec, args []any, isContext contextArgs) (coercedArgs []any, returnUndefined bool, err error) {
	coerced, cloned := args, false
	for i, spec := range specs {
		if spec.Variadic {
			break // variadic args are not nil-propagated or individually coerced here
		}
		if i >= len(coerced) {
			break
		}
		if isContext.has(i) {
			continue
		}
		arg := coerced[i]

		// Nil propagation: undefined arg for a typed param → whole call returns undefined.
		if arg == nil && !sigArgMatchesTypes(nil, spec.Types) {
			return nil, true, nil
		}

		// Singleton coercion: 'a' param with a non-array value → [arg].
		// For a<X>: only coerce when the arg matches the element type X.
		// For plain a: coerce any non-nil, non-array value.
		if sigContainsType(spec.Types, 'a') && arg != nil && !sigArgMatchesTypes(arg, []byte{'a'}) {
			if spec.ContentType == 0 || sigArgMatchesTypes(arg, []byte{spec.ContentType}) {
				if !cloned {
					coerced, cloned = slices.Clone(args), true
				}
				coerced[i] = []any{arg}
			}
		}
	}

	if err := validateCallArgs(specs, coerced, isContext); err != nil {
		return nil, false, err
	}
	return coerced, false, nil
}

// validateCallArgs checks that args satisfy the compiled parameter specs.
// It returns T0410 on a base-type mismatch or arity error, and T0412 when
// an array content-type constraint is violated. An argument isContext marks
// is the context value, which ContextSig has already checked.
func validateCallArgs(specs []parser.ParamSpec, args []any, isContext contextArgs) error {
	checkArg := func(spec parser.ParamSpec, ai int) error {
		if isContext.has(ai) {
			return nil
		}
		return validateOneCallArg(spec, args[ai], ai+1)
	}
	si := 0 // spec index
	ai := 0 // arg index

	for si < len(specs) {
		spec := specs[si]

		if spec.Variadic {
			// Variadic spec: consume args up to maxConsume, stopping on type
			// mismatch so subsequent mandatory specs can claim the remaining args.
			mandatoryAfter := 0
			for k := si + 1; k < len(specs); k++ {
				if !specs[k].Optional && !specs[k].Context {
					mandatoryAfter++
				}
			}
			maxConsume := len(args) - mandatoryAfter
			first := ai
			for ai < maxConsume && !isContext.has(ai) {
				if err := checkArg(spec, ai); err != nil {
					break
				}
				ai++
			}
			// '+' means one or more, as in the jsonata-js regex.
			if ai == first && !spec.Optional {
				if ai < len(args) {
					if err := checkArg(spec, ai); err != nil {
						return err
					}
				}
				return arityError(ai+1, "too few arguments")
			}
			si++
			continue
		}

		if ai >= len(args) {
			if !spec.Optional {
				return arityError(ai+1, "too few arguments")
			}
			si++
			continue
		}

		if err := checkArg(spec, ai); err != nil {
			if spec.Optional {
				si++
				continue
			}
			return err
		}
		ai++
		si++
	}

	// Extra args beyond all specs → T0410 (too many arguments).
	if ai < len(args) {
		return arityError(ai+1, "too many arguments")
	}

	return nil
}

func arityError(pos int, reason string) error {
	return &JSONataError{
		Code:    "T0410",
		Message: fmt.Sprintf("argument %d does not match function signature: %s", pos, reason),
	}
}

// validateOneCallArg checks a single argument against one parameter spec.
func validateOneCallArg(spec parser.ParamSpec, arg any, pos int) error {
	hasContent := spec.ContentType != 0

	if !sigArgMatchesTypes(arg, spec.Types) {
		if hasContent {
			// When the spec has a content type (e.g. a<n>), any base-type failure
			// is reported as T0412 ("must be an array of X").
			return &JSONataError{
				Code:    "T0412",
				Message: fmt.Sprintf("argument %d must be an array of %c", pos, spec.ContentType),
			}
		}
		return &JSONataError{
			Code:    "T0410",
			Message: fmt.Sprintf("argument %d does not match function signature", pos),
		}
	}

	// If there is a content type and the arg is an array, validate every element.
	if hasContent {
		if arr, ok := arg.([]any); ok {
			contentTypes := []byte{spec.ContentType}
			for _, elem := range arr {
				if !sigArgMatchesTypes(elem, contentTypes) {
					return &JSONataError{
						Code:    "T0412",
						Message: fmt.Sprintf("argument %d must be an array of %c", pos, spec.ContentType),
					}
				}
			}
		}
	}

	return nil
}

// sigArgMatchesTypes returns true if arg satisfies at least one of the type chars.
func sigArgMatchesTypes(arg any, types []byte) bool {
	for _, t := range types {
		if sigTypeMatches(arg, t) {
			return true
		}
	}
	return false
}

// sigTypeMatches checks if arg satisfies a single type character.
func sigTypeMatches(arg any, t byte) bool {
	switch t {
	case 'x': // anything — including functions
		return true
	case 'j': // any JSON value (not a function)
		switch arg.(type) {
		case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
			return false
		}
		return true
	case 'n':
		switch arg.(type) {
		case float64, json.Number:
			return true
		}
		return false
	case 's':
		_, ok := arg.(string)
		return ok
	case 'b':
		_, ok := arg.(bool)
		return ok
	case 'l':
		return arg == nil || IsNull(arg)
	case 'a':
		_, ok := arg.([]any)
		return ok
	case 'o':
		return IsMap(arg)
	case 'f':
		switch arg.(type) {
		case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
			return true
		}
		return false
	case 'u': // union of primitives: Boolean, Number, String, or Null
		switch arg.(type) {
		case bool, float64, string, json.Number:
			return true
		}
		return IsNull(arg)
	}
	return false
}

// sigContainsType returns true if types contains the given type char.
func sigContainsType(types []byte, t byte) bool {
	return slices.Contains(types, t)
}
