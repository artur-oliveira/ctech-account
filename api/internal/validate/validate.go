package validate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var v *validator.Validate

func init() {
	v = validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" || name == "" {
			return fld.Name
		}
		return name
	})
}

// Struct validates s and returns a human-readable error on failure, or nil.
func Struct(s any) error {
	if err := v.Struct(s); err != nil {
		if ve, ok := errors.AsType[validator.ValidationErrors](err); ok {
			return &ValidationError{errs: ve}
		}
		return err
	}
	return nil
}

// ValidationError wraps validator.ValidationErrors with a readable Detail() output.
type ValidationError struct {
	errs validator.ValidationErrors
}

func (e *ValidationError) Error() string { return e.Detail() }

func (e *ValidationError) Detail() string {
	msgs := make([]string, 0, len(e.errs))
	for _, fe := range e.errs {
		msgs = append(msgs, fieldMessage(fe))
	}
	return strings.Join(msgs, "; ")
}

func fieldMessage(fe validator.FieldError) string {
	field := fe.Field()
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "email":
		return fmt.Sprintf("%s must be a valid email address", field)
	case "min":
		return fmt.Sprintf("%s must be at least %s characters long", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s characters long", field, fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, fe.Param())
	case "url":
		return fmt.Sprintf("%s must be a valid URL", field)
	case "gte":
		return fmt.Sprintf("%s must be greater than or equal to %s", field, fe.Param())
	case "lte":
		return fmt.Sprintf("%s must be less than or equal to %s", field, fe.Param())
	default:
		return fmt.Sprintf("%s failed validation (%s)", field, fe.Tag())
	}
}

// IsValidationError reports whether err is a ValidationError.
func IsValidationError(err error) (*ValidationError, bool) {
	if ve, ok := errors.AsType[*ValidationError](err); ok {
		return ve, true
	}
	return nil, false
}
