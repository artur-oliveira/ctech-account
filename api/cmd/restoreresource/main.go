// Command restoreresource restores a previous immutable manifest revision as
// a new current revision.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

func main() {
	id := flag.String("id", "", "resource server id")
	revision := flag.Int64("revision", 0, "history revision to restore")
	expected := flag.Int64("expected-revision", -1, "current revision guard")
	flag.Parse()
	if *id == "" || *revision < 1 || *expected < 0 {
		log.Fatal("-id, -revision (>0) and -expected-revision are required")
	}
	prefix := os.Getenv("TABLE_PREFIX")
	if prefix == "" {
		prefix = os.Getenv("ENVIRONMENT")
	}
	prefix = strings.TrimSuffix(prefix, "_")
	if prefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}
	ctx := context.Background()
	db, err := database.New(ctx, os.Getenv("AWS_REGION"))
	if err != nil {
		log.Fatal(err)
	}
	cacheClient, err := cache.New(os.Getenv("VALKEY_URL"))
	if err != nil {
		log.Fatal(err)
	}
	resource, err := scopes.NewRegistryService(scopes.NewRepository(db, prefix), cacheClient).Restore(ctx, *id, *revision, *expected, "operator")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("restored %s revision %d as new revision %d", *id, *revision, resource.Revision)
}
