// Command createpublicclient manually provisions a first-party PUBLIC OAuth
// client (Authorization Code + PKCE grant, no client secret) — e.g. a native
// app or CLI whose redirect_uri is a fixed localhost loopback rather than an
// HTTPS SPA origin. Idempotent: re-running with the same client_id reconciles
// name/redirect_uri/scopes onto the existing client instead of failing.
//
//	AWS_REGION=... TABLE_PREFIX=production go run ./cmd/createpublicclient \
//	  -client-id poker-cli -name "CTech Poker CLI" \
//	  -redirect-uri http://127.0.0.1:51789/callback \
//	  -scopes poker:rooms:read,poker:players:read,poker:sessions:read,poker:hands:read,poker:achievements:read,poker:stats:read
//
// Unlike cmd/createclient (confidential M2M, client_credentials), this client
// gets no secret — a public client authenticates only via PKCE at the token
// endpoint. See ctech-poker/cli/CLAUDE.md for the poker-cli client this backs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/database"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

func main() {
	clientID := flag.String("client-id", "", "stable OAuth client_id (required)")
	name := flag.String("name", "", "human-readable client name (required)")
	redirectURI := flag.String("redirect-uri", "", "exact redirect_uri (required) — https, or http for localhost/127.*")
	scopeList := flag.String("scopes", "", "comma-separated allowed scopes (required)")
	flag.Parse()

	if flag.NArg() != 0 {
		log.Fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}
	requestedScopes, err := parseScopes(*scopeList)
	if err != nil {
		log.Fatal(err)
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

	client, changed, err := service.EnsureFirstPartyPublicClient(ctx, *clientID, *name, *redirectURI, requestedScopes)
	if err != nil {
		log.Fatalf("creating public client: %v", err)
	}

	fmt.Printf("client_id: %s\n", client.ID())
	fmt.Printf("client_type: public (no secret — PKCE only)\n")
	fmt.Printf("redirect_uris: %s\n", strings.Join(client.RedirectURIs, ", "))
	fmt.Printf("allowed_scopes: %s\n", strings.Join(client.AllowedScopes, ", "))
	if changed {
		fmt.Println("client created or updated")
	} else {
		fmt.Println("client already matched the requested name/redirect_uri/scopes — no change")
	}
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
