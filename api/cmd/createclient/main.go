// Command createclient manually provisions a confidential first-party OAuth
// client for the client_credentials (machine-to-machine) grant.
//
//	AWS_REGION=... TABLE_PREFIX=production_ go run ./cmd/createclient \
//	  -client-id wallet-worker -name "Wallet worker" \
//	  -scopes internal:account:kyc
//
// The client secret is printed once after the DynamoDB write succeeds.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/database"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

const maxSSMParameterNameLength = 1011

var ssmPathPattern = regexp.MustCompile(`^/[A-Za-z0-9_.\-/]+$`)

type ssmParameterWriter interface {
	PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

func main() {
	clientID := flag.String("client-id", "", "stable OAuth client_id (required)")
	name := flag.String("name", "", "human-readable client name (required)")
	scopeList := flag.String("scopes", "", "comma-separated service scopes (required)")
	ssmPathClient := flag.String("ssm-path-client", "", "optional SSM path for the client_id (String)")
	ssmPathSecret := flag.String("ssm-path-secret", "", "optional SSM path for the client_secret (SecureString)")
	flag.Parse()

	if flag.NArg() != 0 {
		log.Fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}
	requestedScopes, err := parseScopes(*scopeList)
	if err != nil {
		log.Fatal(err)
	}
	if err := oauthclient.ValidateM2MInput(*clientID, *name, requestedScopes); err != nil {
		log.Fatalf("invalid arguments: %v", err)
	}
	if err := validateSSMPaths(*ssmPathClient, *ssmPathSecret); err != nil {
		log.Fatalf("invalid arguments: %v", err)
	}

	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT")
	}
	tablePrefix = strings.TrimSuffix(tablePrefix, "_")
	if tablePrefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}

	ctx := context.Background()
	db, err := database.New(ctx, os.Getenv("AWS_REGION"))
	if err != nil {
		log.Fatalf("dynamodb client: %v", err)
	}
	cacheClient, err := cache.New(os.Getenv("VALKEY_URL"))
	if err != nil {
		log.Fatalf("valkey client: %v", err)
	}
	catalog := scopes.NewCatalogService(scopes.NewRepository(db, tablePrefix), cacheClient)
	service := oauthclient.NewOperatorService(oauthclient.NewRepository(db, tablePrefix), catalog)

	created, secret, err := service.CreateM2M(ctx, *clientID, *name, requestedScopes)
	if err != nil {
		log.Fatalf("creating M2M client: %v", err)
	}

	if *ssmPathClient != "" || *ssmPathSecret != "" {
		awsCfg, cfgErr := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
		if cfgErr != nil {
			printCreatedCredentials(created.ID(), secret)
			log.Fatalf("client created, but loading AWS config for SSM failed: %v", cfgErr)
		}
		if storeErr := storeCredentials(ctx, ssm.NewFromConfig(awsCfg), *ssmPathClient, *ssmPathSecret, created.ID(), secret); storeErr != nil {
			printCreatedCredentials(created.ID(), secret)
			log.Fatalf("client created, but storing credentials in SSM failed: %v", storeErr)
		}
	}

	fmt.Printf("client_id: %s\n", created.ID())
	if *ssmPathClient != "" {
		fmt.Printf("client_id stored in SSM: %s\n", *ssmPathClient)
	}
	if *ssmPathSecret != "" {
		fmt.Printf("client_secret stored in SSM: %s\n", *ssmPathSecret)
	} else {
		fmt.Printf("client_secret: %s\n", secret)
		fmt.Println("Store client_secret securely; it cannot be recovered.")
	}
}

func validateSSMPaths(paths ...string) error {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if len(path) > maxSSMParameterNameLength || !ssmPathPattern.MatchString(path) || strings.Contains(path, "//") {
			return fmt.Errorf("invalid SSM parameter path %q", path)
		}
		lower := strings.ToLower(strings.TrimPrefix(path, "/"))
		if lower == "aws" || strings.HasPrefix(lower, "aws/") || lower == "ssm" || strings.HasPrefix(lower, "ssm/") {
			return fmt.Errorf("SSM parameter path %q uses a reserved prefix", path)
		}
	}
	return nil
}

func storeCredentials(ctx context.Context, writer ssmParameterWriter, clientPath, secretPath, clientID, secret string) error {
	// Store the secret first: if the second write fails, the sensitive value is
	// already safe and the recovery output remains available to the operator.
	if secretPath != "" {
		if _, err := writer.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(secretPath),
			Value:     aws.String(secret),
			Type:      types.ParameterTypeSecureString,
			Overwrite: aws.Bool(false),
		}); err != nil {
			return fmt.Errorf("storing client_secret at %q: %w", secretPath, err)
		}
	}
	if clientPath != "" {
		if _, err := writer.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(clientPath),
			Value:     aws.String(clientID),
			Type:      types.ParameterTypeString,
			Overwrite: aws.Bool(false),
		}); err != nil {
			return fmt.Errorf("storing client_id at %q: %w", clientPath, err)
		}
	}
	return nil
}

func printCreatedCredentials(clientID, secret string) {
	fmt.Printf("client_id: %s\n", clientID)
	fmt.Printf("client_secret: %s\n", secret)
	fmt.Println("Store client_secret securely; it cannot be recovered from the OAuth client table.")
}

func parseScopes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("-scopes is required")
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		scope := strings.TrimSpace(part)
		if scope == "" {
			return nil, fmt.Errorf("-scopes contains an empty value")
		}
		result = append(result, scope)
	}
	return result, nil
}
