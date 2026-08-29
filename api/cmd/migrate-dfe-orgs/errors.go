package main

import (
	"errors"

	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

func isNotFound(err error) bool      { return errors.Is(err, orgDomain.ErrNotFound) }
func isAlreadyMember(err error) bool { return errors.Is(err, orgDomain.ErrAlreadyMember) }

func isCompanyNotFound(err error) bool { return errors.Is(err, companyDomain.ErrNotFound) }
