package main

import "testing"

func TestValidateSSMPaths(t *testing.T) {
	if err := validateSSMPaths(
		"/ctech-account/dev/scope-publishers/dfe/client-id",
		"/ctech-account/dev/scope-publishers/dfe/client-secret",
	); err != nil {
		t.Fatalf("valid paths: %v", err)
	}
	for _, path := range []string{"relative/path", "/aws/secret", "/ctech-account//secret", "/ctech account/secret"} {
		if err := validateSSMPaths(path); err == nil {
			t.Errorf("path %q must be rejected", path)
		}
	}
}
