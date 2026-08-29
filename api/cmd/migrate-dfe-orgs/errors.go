package main

import (
	"errors"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

func isNotFound(err error) bool      { return errors.Is(err, orgDomain.ErrNotFound) }
func isAlreadyMember(err error) bool { return errors.Is(err, orgDomain.ErrAlreadyMember) }
