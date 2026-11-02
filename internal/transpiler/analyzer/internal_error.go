package analyzer

import (
	"errors"
	"fmt"
)

// InternalError is a defect of the transpiler met while reading File, such
// as a clean parse that galop's converter did not convert to a native tree.
// No reader can work around it, and the file is not at fault, so it fails
// the analysis that met it: it is never dropped, nor reported as the file's
// syntax error.
type InternalError struct {
	File string
	Err  error
}

func (e *InternalError) Error() string { return fmt.Sprintf("internal error: %s: %v", e.File, e.Err) }

func (e *InternalError) Unwrap() error { return e.Err }

// asInternal is err's InternalError, or nil.
func asInternal(err error) *InternalError {
	var ie *InternalError
	if errors.As(err, &ie) {
		return ie
	}
	return nil
}
