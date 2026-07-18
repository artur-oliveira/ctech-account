// Command seedscopes writes the in-repo scope catalog seed
// (internal/scopes/catalog.go) to the {env}_ctech_scopes DynamoDB table and
// invalidates the Valkey catalog cache, making new scopes grantable
// immediately.
//
//	AWS_REGION=... TABLE_PREFIX=production VALKEY_URL=... go run ./cmd/seedscopes
//
// TABLE_PREFIX falls back to ENVIRONMENT (same rule as the API config).
// VALKEY_URL is optional — without it the cache expires on its own TTL (5 min).
package main

import (
	"context"
	"log"
	"os"
	"strings"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

func main() {
	ctx := context.Background()

	region := os.Getenv("AWS_REGION")
	tablePrefix := os.Getenv("TABLE_PREFIX")
	if tablePrefix == "" {
		tablePrefix = os.Getenv("ENVIRONMENT")
	}
	tablePrefix = strings.TrimSuffix(tablePrefix, "_")
	if tablePrefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}

	db, err := database.New(ctx, region)
	if err != nil {
		log.Fatalf("dynamodb client: %v", err)
	}
	repo := scopes.NewRepository(db, tablePrefix)

	for _, svc := range scopes.DefaultCatalog() {
		if err := repo.PutService(ctx, svc); err != nil {
			log.Fatalf("seeding service %q: %v", svc.Service, err)
		}
		log.Printf("seeded service %q (%d scopes, internal=%v)", svc.Service, len(svc.Scopes), svc.Internal)
	}

	valkeyClient, err := cache.New(os.Getenv("VALKEY_URL"))
	if err != nil || !valkeyClient.Enabled() {
		log.Println("valkey unavailable — catalog cache expires within 5 minutes")
		return
	}
	if err := valkeyClient.Delete(ctx, scopes.CatalogCacheKey); err != nil {
		log.Printf("warning: cache invalidation failed: %v (expires within 5 minutes)", err)
		return
	}
	log.Println("catalog cache invalidated")
}
