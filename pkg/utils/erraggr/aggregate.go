package erraggr

import (
	"fmt"
	"strings"
)

type ErrAggregator interface {
	Error() string
	Errors() []error
}

type errAggregate struct {
	errs []error
}

func NewErrAggregator(errs []error) ErrAggregator {
	if len(errs) == 0 {
		return nil
	}
	return &errAggregate{errs: errs}
}

func (e *errAggregate) Unwrap() []error {
	return e.errs
}

func (e *errAggregate) Errors() []error {
	return e.errs
}

func (e *errAggregate) Error() string {
	if e == nil || len(e.errs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d errors occur: \n", len(e.errs)))
	for i, err := range e.errs {
		sb.WriteString(fmt.Sprintf("*%d - %s", i, err.Error()))
		if i < len(e.errs)-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}
