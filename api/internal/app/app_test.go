package app

import (
	"testing"

	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/config"
)

// The container wires every dependency the route table needs: a missing or
// duplicate provider fails here instead of at boot.
func TestModulesGraphIsComplete(t *testing.T) {
	if err := fx.ValidateApp(Modules(&config.Config{})); err != nil {
		t.Fatal(err)
	}
}
