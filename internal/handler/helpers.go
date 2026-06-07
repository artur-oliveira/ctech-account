package handler

import (
	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/validate"
	"github.com/gofiber/fiber/v3"
)

// parseBody decodes JSON from the request body and validates the struct.
// Returns a *apierror.Problem (as error) on failure so the caller can return it directly
// and Fiber's error handler will send the RFC 7807 response.
func parseBody[T any](c fiber.Ctx, dst *T) error {
	if err := c.Bind().JSON(dst); err != nil {
		return apierror.InvalidRequest("Request body is malformed or contains invalid JSON.", c.Path())
	}
	if err := validate.Struct(*dst); err != nil {
		ve, _ := validate.IsValidationError(err)
		return apierror.ValidationFailed(ve.Detail(), c.Path())
	}
	return nil
}
