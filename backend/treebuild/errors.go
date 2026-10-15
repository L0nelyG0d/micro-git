package treebuild

import "errors"

// Sentinel kinds of validation failure. Check with errors.Is; get the path
// with errors.As into *Error.
var (
	ErrInvalidPath   = errors.New("invalid path")
	ErrDuplicatePath = errors.New("duplicate path")
	ErrBadChange     = errors.New("change must have content or delete")
	ErrFileTooLarge  = errors.New("file too large")
	ErrNotUTF8Text   = errors.New("file is not valid UTF-8 text")
	ErrPathConflict  = errors.New("path conflicts with existing file or directory")
	ErrNoChanges     = errors.New("no changes")
)

// Error is a typed validation error. Kind is one of the sentinels above and
// Path is the offending path (empty for ErrNoChanges). Error() renders
// "<kind>: <path>" (just "no changes" without a path), which is the message
// in plan.md "API contracts > Writing commits".
type Error struct {
	Kind error
	Path string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return e.Kind.Error()
	}
	return e.Kind.Error() + ": " + e.Path
}

// Is makes errors.Is(err, ErrInvalidPath) etc. work.
func (e *Error) Is(target error) bool { return target == e.Kind }

// Unwrap returns the kind.
func (e *Error) Unwrap() error { return e.Kind }

func newErr(kind error, path string) error { return &Error{Kind: kind, Path: path} }
