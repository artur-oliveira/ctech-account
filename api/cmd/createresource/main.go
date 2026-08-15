// Command createresource provisions a Resource Server and its dedicated
// confidential scope publisher. It is an operator-only bootstrap command.
package main

import (
	"context"
	"errors"
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

func main() {
	id := flag.String("id", "", "resource server id (required)")
	name := flag.String("name", "", "display name (required)")
	audience := flag.String("audience", "", "immutable HTTPS audience (required)")
	publisherID := flag.String("publisher-client-id", "", "publisher OAuth client_id (required)")
	clientPath := flag.String("ssm-path-client", "", "SSM String path for client_id")
	secretPath := flag.String("ssm-path-secret", "", "SSM SecureString path for client_secret")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}
	if *id == "" || *name == "" || *audience == "" || *publisherID == "" {
		log.Fatal("-id, -name, -audience and -publisher-client-id are required")
	}
	if err := validateSSMPaths(*clientPath, *secretPath); err != nil {
		log.Fatalf("invalid arguments: %v", err)
	}

	prefix := tablePrefix()
	ctx := context.Background()
	db, err := database.New(ctx, os.Getenv("AWS_REGION"))
	if err != nil {
		log.Fatalf("dynamodb client: %v", err)
	}
	cacheClient, err := cache.New(os.Getenv("VALKEY_URL"))
	if err != nil {
		log.Fatalf("valkey client: %v", err)
	}
	repo := scopes.NewRepository(db, prefix)
	clientRepo := oauthclient.NewRepository(db, prefix)
	operator := oauthclient.NewOperatorService(clientRepo, scopes.NewCatalogService(repo, cacheClient))

	created, secret, err := operator.CreateResourcePublisher(ctx, *publisherID, *name+" scope publisher", *id)
	if err != nil {
		if !errors.Is(err, oauthclient.ErrClientAlreadyExists) {
			log.Fatalf("creating publisher: %v", err)
		}
		created, err = clientRepo.GetByID(ctx, *publisherID)
		if err != nil || created.ManagedResourceID != *id || !created.HasScope(scopes.InternalAccountScopeRegistryWrite) {
			log.Fatalf("existing publisher client is not bound to resource %q", *id)
		}
		log.Printf("publisher %q already exists; secret will not be returned", *publisherID)
	}
	if secret != "" && (*clientPath != "" || *secretPath != "") {
		cfg, cfgErr := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
		if cfgErr != nil {
			log.Fatalf("publisher created, loading SSM config: %v", cfgErr)
		}
		writer := ssm.NewFromConfig(cfg)
		if *secretPath != "" {
			_, err = writer.PutParameter(ctx, &ssm.PutParameterInput{Name: aws.String(*secretPath), Value: aws.String(secret), Type: types.ParameterTypeSecureString, Overwrite: aws.Bool(false)})
			if err != nil {
				log.Fatalf("publisher created, storing secret: %v", err)
			}
		}
		if *clientPath != "" {
			_, err = writer.PutParameter(ctx, &ssm.PutParameterInput{Name: aws.String(*clientPath), Value: aws.String(created.ID()), Type: types.ParameterTypeString, Overwrite: aws.Bool(false)})
			if err != nil {
				log.Fatalf("publisher created, storing client id: %v", err)
			}
		}
	}

	registry := scopes.NewRegistryService(repo, cacheClient)
	resource, err := registry.Provision(ctx, *id, *name, *audience, *publisherID)
	if err != nil {
		if !errors.Is(err, scopes.ErrResourceAlreadyExists) {
			log.Fatalf("provisioning resource: %v", err)
		}
		resource, err = registry.Get(ctx, *id)
		if err != nil || resource.Audience != *audience || resource.PublisherClientID != *publisherID {
			log.Fatalf("existing resource %q does not match requested audience/publisher", *id)
		}
		log.Printf("resource server %q already exists", *id)
	}
	fmt.Printf("resource_server_id: %s\npublisher_client_id: %s\n", resource.ID(), created.ID())
	if secret != "" && *secretPath == "" {
		fmt.Printf("publisher_client_secret: %s\nStore it securely; it cannot be recovered.\n", secret)
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

func tablePrefix() string {
	prefix := os.Getenv("TABLE_PREFIX")
	if prefix == "" {
		prefix = os.Getenv("ENVIRONMENT")
	}
	prefix = strings.TrimSuffix(prefix, "_")
	if prefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}
	return prefix
}
