package evaluator

import "github.com/recolabs/gnata/internal/parser"

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
