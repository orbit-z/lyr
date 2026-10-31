package lyr

// For generated code. ErrorAt wraps every return of a frontmatter body:
// return ErrorAt("src/pages/x.lyr:14:5", err). It prefixes a failure
// with the .lyr position of its return, so the production 500 log line
// says where the error came from. ErrNotFound and *RedirectError are control
// flow and pass through unchanged, as does an error that already carries a
// position (one returned from a component's Render). Only a non-nil failure
// allocates.
func ErrorAt(pos string, err error) error {
	if err == nil {
		return nil
	}
	switch err.(type) {
	case *posError, *RedirectError:
		return err
	}
	if err == ErrNotFound {
		return err
	}
	return &posError{pos: pos, err: err}
}

// posError is an error returned at a .lyr position.
type posError struct {
	pos string // "src/pages/x.lyr:L:C"
	err error
}

func (e *posError) Error() string { return e.pos + ": " + e.err.Error() }

func (e *posError) Unwrap() error { return e.err }
