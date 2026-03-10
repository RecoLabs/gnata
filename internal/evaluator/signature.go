package evaluator

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

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
//     content-type violations.
//
// It returns (coercedArgs, returnUndefined, err).
// When returnUndefined is true the caller must return (nil, nil) immediately.
func processCallArgs(specs []parser.ParamSpec, args []any) (coercedArgs []any, returnUndefined bool, err error) {
	coerced := slices.Clone(args)

	for i, spec := range specs {
		if spec.Variadic {
			break // variadic args are not nil-propagated or individually coerced here
		}
		if i >= len(coerced) {
			break
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
				coerced[i] = []any{arg}
			}
		}
	}

	if err := validateCallArgs(specs, coerced); err != nil {
		return nil, false, err
	}
	return coerced, false, nil
}

// validateCallArgs checks that args satisfy the compiled parameter specs.
// It returns T0410 on a base-type mismatch or arity error, and T0412 when
// an array content-type constraint is violated.
func validateCallArgs(specs []parser.ParamSpec, args []any) error {
	si := 0 // spec index
	ai := 0 // arg index

	for si < len(specs) {
		spec := specs[si]

		if spec.Variadic {
			// Variadic spec: validate every remaining arg against it.
			for ai < len(args) {
				if err := validateOneCallArg(spec, args[ai], ai+1); err != nil {
					return err
				}
				ai++
			}
			si++
			continue
		}

		if ai >= len(args) {
			// No arg for this spec position.
			if !spec.Optional {
				return &JSONataError{
					Code:    "T0410",
					Message: fmt.Sprintf("argument %d does not match function signature: too few arguments", ai+1),
				}
			}
			si++
			continue
		}

		if err := validateOneCallArg(spec, args[ai], ai+1); err != nil {
			return err
		}
		ai++
		si++
	}

	// Extra args beyond all specs → T0410 (too many arguments).
	if ai < len(args) {
		return &JSONataError{
			Code:    "T0410",
			Message: fmt.Sprintf("argument %d does not match function signature: too many arguments", ai+1),
		}
	}

	return nil
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
		return arg == nil
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
	}
	return false
}

// sigContainsType returns true if types contains the given type char.
func sigContainsType(types []byte, t byte) bool {
	return slices.Contains(types, t)
}
