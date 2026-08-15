// Command migratescopes converts downstream legacy SERVICE catalog rows into
// v2 Resource Server manifests. It is dry-run unless -apply is supplied.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

func main() {
	apply := flag.Bool("apply", false, "write v2 resources (default is dry-run)")
	flag.Parse()
	ctx := context.Background()
	prefix := os.Getenv("TABLE_PREFIX")
	if prefix == "" {
		prefix = os.Getenv("ENVIRONMENT")
	}
	prefix = strings.TrimSuffix(prefix, "_")
	if prefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}
	db, err := database.New(ctx, os.Getenv("AWS_REGION"))
	if err != nil {
		log.Fatal(err)
	}
	cacheClient, err := cache.New(os.Getenv("VALKEY_URL"))
	if err != nil {
		log.Fatal(err)
	}
	repo := scopes.NewRepository(db, prefix)
	legacy, err := repo.LoadCatalog(ctx)
	if err != nil {
		log.Fatal(err)
	}
	type candidate struct {
		name, audience string
		entries        []scopes.ScopeDefinition
	}
	resources := make(map[string]*candidate)
	for _, service := range legacy {
		id := service.Service
		visibility := scopes.VisibilityPublic
		if strings.HasPrefix(id, scopes.InternalServicePrefix+":") {
			id = strings.TrimPrefix(id, scopes.InternalServicePrefix+":")
			visibility = scopes.VisibilityInternal
		}
		if id == scopes.IdentityService || id == "account" {
			continue
		}
		c := resources[id]
		if c == nil {
			c = &candidate{name: strings.TrimPrefix(service.Name, "Internal — "), audience: service.Audience}
			resources[id] = c
		}
		if c.audience == "" {
			c.audience = service.Audience
		}
		for _, entry := range service.Scopes {
			c.entries = append(c.entries, scopes.ScopeDefinition{Scope: entry.Scope, Description: entry.Description, DescriptionPT: entry.DescriptionPT, Visibility: visibility, Status: scopes.StatusActive})
		}
	}
	ids := make([]string, 0, len(resources))
	for id := range resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	registry := scopes.NewRegistryService(repo, cacheClient)
	for _, id := range ids {
		c := resources[id]
		manifest := scopes.Manifest{SchemaVersion: scopes.ManifestSchemaV1, ResourceServerID: id, DisplayName: c.name, Scopes: c.entries}
		if err := scopes.ValidateManifest(manifest); err != nil {
			log.Fatalf("%s: %v", id, err)
		}
		log.Printf("%s: %d scopes, audience=%s, publisher=scope-publisher-%s", id, len(c.entries), c.audience, id)
		if !*apply {
			continue
		}
		resource, getErr := registry.Get(ctx, id)
		if getErr != nil {
			resource, err = registry.Provision(ctx, id, c.name, c.audience, "scope-publisher-"+id)
			if err != nil {
				log.Fatalf("provisioning %s: %v", id, err)
			}
		}
		_, _, err = registry.Publish(ctx, resource.PublisherClientID, manifest, resource.Revision, "ctech-account", "legacy-migration")
		if err != nil {
			log.Fatalf("publishing %s: %v", id, err)
		}
	}
	if !*apply {
		log.Printf("dry-run only; rerun with -apply to write")
	}
}
