// Command migrate-dfe-orgs imports ctech-dfe's organizations and their
// memberships into the platform organization model.
//
// It is dry-run by default, it is safe to run again, and it deletes nothing:
// ctech-dfe keeps every row it has, so the rollback for this migration is
// deleting the rows it wrote here.
//
//	migrate-dfe-orgs -dfe-table-prefix prod_dfe -table-prefix prod          # report only
//	migrate-dfe-orgs -dfe-table-prefix prod_dfe -table-prefix prod -apply   # write
//
// It exits non-zero when any organization needs a human, so a pipeline cannot
// report success over a partial migration.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

func main() {
	var (
		dfePrefix = flag.String("dfe-table-prefix", "", "table prefix of the ctech-dfe tables to read (e.g. prod_dfe)")
		prefix    = flag.String("table-prefix", "", "table prefix of the account tables to write (e.g. prod)")
		region    = flag.String("region", "us-east-1", "AWS region")
		apply     = flag.Bool("apply", false, "write. Without it nothing is written and the report is a rehearsal")
		only      = flag.String("org", "", "migrate a single dfe organization by its pk (e.g. CNPJ_11111111000191)")
	)
	flag.Parse()

	if *dfePrefix == "" || *prefix == "" {
		fmt.Fprintln(os.Stderr, "both -dfe-table-prefix and -table-prefix are required")
		os.Exit(2)
	}

	ctx := context.Background()
	if err := run(ctx, *region, *dfePrefix, *prefix, *only, *apply); err != nil {
		fmt.Fprintf(os.Stderr, "\nmigration failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, region, dfePrefix, prefix, only string, doApply bool) error {
	db, err := database.New(ctx, region)
	if err != nil {
		return fmt.Errorf("connecting to dynamodb: %w", err)
	}

	source := &dfeReader{
		orgs:        database.NewBase(db, dfePrefix, "organizations"),
		memberTable: database.NewBase(db, dfePrefix, "organization_users"),
		name:        database.TableName(dfePrefix, "organizations"),
	}
	repo := orgDomain.NewRepository(db, prefix)
	companies := companyDomain.NewRepository(db, prefix)
	users := userDomain.NewRepository(db, prefix)
	userExists := func(ctx context.Context, id string) (bool, error) {
		_, err := users.GetByID(ctx, id)
		if err == nil {
			return true, nil
		}
		if isUserNotFound(err) {
			return false, nil
		}
		return false, err
	}

	orgs, err := source.organizations(ctx, only)
	if err != nil {
		return err
	}
	if len(orgs) == 0 {
		return fmt.Errorf("no organizations found in %s", source.name)
	}

	mode := "DRY RUN — nothing will be written"
	if doApply {
		mode = "APPLY — writing"
	}
	fmt.Printf("%s\nreading %s, writing the %s_account_* tables\n\n", mode, source.name, prefix)

	var created, alreadyThere, needHuman int
	for _, org := range orgs {
		members, err := source.members(ctx, org.PK)
		if err != nil {
			return err
		}
		d, err := planOrg(ctx, org, members, userExists)
		if err != nil {
			return err
		}

		// The company half of the same row. A dfe organization was always a
		// company — its key is the CNPJ — and this is the pass that unfuses
		// them (ctech-billing ADR 0022).
		cd := planCompany(org, members, d)

		fmt.Printf("── %s  %q\n", d.SourceRef, d.DisplayName)
		for _, note := range append(d.Notes, cd.Notes...) {
			fmt.Printf("   note: %s\n", note)
		}
		// Either half refusing stops both. A workspace with no company cannot
		// issue anything, and a company with no workspace has nobody in it.
		if d.Action == actionReview || cd.Action == actionReview {
			needHuman++
			for _, r := range append(d.Review, cd.Review...) {
				fmt.Printf("   NEEDS A HUMAN: %s\n", r)
			}
			fmt.Println("   not migrated")
			continue
		}
		fmt.Printf("   owner %s, %d further member(s)\n", d.OwnerUserID, len(d.Members))
		fmt.Printf("   company %s (%s), %d actor(s)\n", cd.TaxID, cd.TaxIDKind, len(cd.Actors))
		if !doApply {
			created++
			continue
		}

		res, err := apply(ctx, repo, d)
		if err != nil {
			return err
		}
		cres, err := applyCompany(ctx, companies, res.OrganizationID, cd)
		if err != nil {
			return err
		}
		if cres.CreatedCompany {
			fmt.Printf("   created company %s with %d actor(s)\n", cres.CompanyID, cres.CreatedActors)
		} else {
			fmt.Printf("   company already imported as %s; %d actor edge(s) written\n", cres.CompanyID, cres.CreatedActors)
		}

		switch {
		case res.CreatedOrganization:
			created++
			fmt.Printf("   created %s with %d membership(s)\n", res.OrganizationID, res.CreatedMemberships+1)
		case res.CreatedMemberships > 0:
			alreadyThere++
			fmt.Printf("   already imported as %s; completed %d missing membership(s)\n", res.OrganizationID, res.CreatedMemberships)
		default:
			alreadyThere++
			fmt.Printf("   already imported as %s; nothing to do\n", res.OrganizationID)
		}
	}

	verb := "would migrate"
	if doApply {
		verb = "migrated"
	}
	fmt.Printf("\n%d %s, %d already there, %d need a human\n", created, verb, alreadyThere, needHuman)
	if needHuman > 0 {
		// Exit non-zero so nothing downstream reports success over a partial
		// migration. Resolve each one in dfe, then run again.
		return fmt.Errorf("%d organization(s) were not migrated and need a decision", needHuman)
	}
	return nil
}

func isUserNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), userDomain.ErrNotFound.Error())
}

// dfeReader reads ctech-dfe's two tables. It writes nothing there, ever.
type dfeReader struct {
	orgs        database.Base
	memberTable database.Base
	name        string
}

// organizations reads every dfe organization.
//
// A Scan, because dfe's organizations table has no index to query — and it does
// not need one: this runs once, against a table with a handful of rows, and a
// migration that misses an organization because it paginated badly is worse
// than one that reads the whole table.
func (r *dfeReader) organizations(ctx context.Context, only string) ([]dfeOrg, error) {
	if only != "" {
		item, err := r.orgs.GetItem(ctx, only)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", only, err)
		}
		if item == nil {
			return nil, fmt.Errorf("organization %s not found in %s", only, r.name)
		}
		return []dfeOrg{toDFEOrg(item)}, nil
	}

	var out []dfeOrg
	var start map[string]types.AttributeValue
	for {
		page, err := r.orgs.ScanRaw(ctx, &dynamodb.ScanInput{
			TableName:         &r.name,
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", r.name, err)
		}
		for _, item := range page.Items {
			out = append(out, toDFEOrg(item))
		}
		if len(page.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = page.LastEvaluatedKey
	}
}

func (r *dfeReader) members(ctx context.Context, orgPK string) ([]dfeMember, error) {
	res, err := r.memberTable.Query(ctx, database.QueryOpts{PK: orgPK, Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("reading members of %s: %w", orgPK, err)
	}
	out := make([]dfeMember, 0, len(res.Items))
	for _, item := range res.Items {
		out = append(out, dfeMember{
			UserID:      attrString(item, "user_id"),
			Name:        attrString(item, "name"),
			Role:        attrString(item, "role"),
			Permissions: attrStrings(item, "permissions"),
			InvitedBy:   attrString(item, "invited_by"),
			CreatedAt:   attrString(item, "created_at"),
		})
	}
	return out, nil
}

// toDFEOrg pulls the four fields this migration reads. The dfe row carries far
// more — the whole fiscal person — and none of it comes along.
func toDFEOrg(item map[string]types.AttributeValue) dfeOrg {
	return dfeOrg{
		PK:          attrString(item, "pk"),
		Name:        attrString(item, "name"),
		OwnerUserID: attrString(item, "owner_user_id"),
		CreatedAt:   attrString(item, "created_at"),
	}
}

func attrString(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func attrStrings(item map[string]types.AttributeValue, key string) []string {
	raw, ok := item[key]
	if !ok {
		return nil
	}
	var out []string
	if err := attributevalue.Unmarshal(raw, &out); err != nil {
		// Unreadable rather than absent. Returning one entry keeps the
		// permission check tripping, so the row lands in front of a human
		// instead of being imported as if it had none.
		return []string{"<unreadable>"}
	}
	return out
}
