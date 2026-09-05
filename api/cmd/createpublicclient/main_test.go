package main

import (
	"reflect"
	"testing"
)

func TestParseScopes(t *testing.T) {
	got, err := parseScopes(" poker:rooms:read,poker:players:read ")
	if err != nil {
		t.Fatalf("parseScopes: %v", err)
	}
	want := []string{"poker:rooms:read", "poker:players:read"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseScopesRejectsMissingAndEmptyValues(t *testing.T) {
	for _, input := range []string{"", "  ", "poker:rooms:read,"} {
		if _, err := parseScopes(input); err == nil {
			t.Fatalf("parseScopes(%q) returned no error", input)
		}
	}
}
