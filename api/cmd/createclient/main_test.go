package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type parameterWriterStub struct {
	inputs []*ssm.PutParameterInput
	errAt  int
}

func (w *parameterWriterStub) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	w.inputs = append(w.inputs, input)
	if w.errAt == len(w.inputs) {
		return nil, errors.New("put failed")
	}
	return &ssm.PutParameterOutput{}, nil
}

func TestParseScopes(t *testing.T) {
	got, err := parseScopes(" internal:account:kyc,internal:wallet:credit ")
	if err != nil {
		t.Fatalf("parseScopes: %v", err)
	}
	want := []string{"internal:account:kyc", "internal:wallet:credit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseScopesRejectsMissingAndEmptyValues(t *testing.T) {
	for _, input := range []string{"", "  ", "internal:account:kyc,"} {
		if _, err := parseScopes(input); err == nil {
			t.Fatalf("parseScopes(%q) returned no error", input)
		}
	}
}

func TestValidateSSMPaths(t *testing.T) {
	if err := validateSSMPaths("/ctech/wallet/client-id", "/ctech/wallet/client_secret"); err != nil {
		t.Fatalf("valid paths: %v", err)
	}
	for _, path := range []string{"relative/path", "/ctech//secret", "/aws/secret", "/ssm/secret", "/ctech/a secret"} {
		if err := validateSSMPaths(path); err == nil {
			t.Fatalf("validateSSMPaths(%q) returned no error", path)
		}
	}
}

func TestStoreCredentialsUsesCorrectSSMTypes(t *testing.T) {
	writer := &parameterWriterStub{}
	if err := storeCredentials(context.Background(), writer, "/client", "/secret", "worker", "raw-secret"); err != nil {
		t.Fatalf("storeCredentials: %v", err)
	}
	if len(writer.inputs) != 2 {
		t.Fatalf("writes = %d, want 2", len(writer.inputs))
	}
	secret, client := writer.inputs[0], writer.inputs[1]
	if aws.ToString(secret.Name) != "/secret" || aws.ToString(secret.Value) != "raw-secret" || secret.Type != types.ParameterTypeSecureString {
		t.Fatalf("unexpected secret input: %+v", secret)
	}
	if aws.ToString(client.Name) != "/client" || aws.ToString(client.Value) != "worker" || client.Type != types.ParameterTypeString {
		t.Fatalf("unexpected client input: %+v", client)
	}
	if aws.ToBool(secret.Overwrite) || aws.ToBool(client.Overwrite) {
		t.Fatal("credentials must not overwrite existing parameters")
	}
}

func TestStoreCredentialsStopsAfterFailure(t *testing.T) {
	writer := &parameterWriterStub{errAt: 1}
	if err := storeCredentials(context.Background(), writer, "/client", "/secret", "worker", "raw-secret"); err == nil {
		t.Fatal("expected SSM error")
	}
	if len(writer.inputs) != 1 || aws.ToString(writer.inputs[0].Name) != "/secret" {
		t.Fatalf("writes = %+v, want only secret attempt", writer.inputs)
	}
}
