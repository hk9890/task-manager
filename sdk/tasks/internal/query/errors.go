package query

import "fmt"

// ParseError is returned when the filter expression cannot be parsed.
// Pos is the byte offset in the expression string where the error was detected.
type ParseError struct {
	Pos     int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at byte %d: %s", e.Pos, e.Message)
}

// parseErr constructs a *ParseError at the given byte offset.
func parseErr(pos int, format string, args ...interface{}) *ParseError {
	return &ParseError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}
