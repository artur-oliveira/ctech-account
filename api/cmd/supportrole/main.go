// Command supportrole provisions the operator-only support_ticket role.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.aoctech.app/account/api/internal/database"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	prefix := strings.TrimSuffix(firstNonEmpty(os.Getenv("TABLE_PREFIX"), os.Getenv("ENVIRONMENT")), "_")
	if prefix == "" {
		log.Fatal("TABLE_PREFIX (or ENVIRONMENT) is required")
	}
	db, err := database.New(context.Background(), os.Getenv("AWS_REGION"))
	if err != nil {
		log.Fatalf("dynamodb client: %v", err)
	}
	svc := userDomain.NewService(userDomain.NewRepository(db, prefix))
	switch os.Args[1] {
	case "set":
		runSet(svc, os.Args[2:])
	case "revoke":
		runRevoke(svc, os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func runSet(svc *userDomain.Service, args []string) {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	role := fs.String("role", "", "agent|manager|admin")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("parsing set arguments: %v", err)
	}
	if fs.NArg() != 1 {
		log.Fatal("usage: supportrole set <user_id> -role agent|manager|admin")
	}
	switch *role {
	case userDomain.SupportRoleAgent, userDomain.SupportRoleManager, userDomain.SupportRoleAdmin:
	default:
		log.Fatalf("invalid role %q", *role)
	}
	id := fs.Arg(0)
	if _, err := svc.GetByID(context.Background(), id); err != nil {
		if errors.Is(err, userDomain.ErrNotFound) {
			log.Fatalf("user %s not found", id)
		}
		log.Fatalf("looking up user: %v", err)
	}
	if err := svc.SetSupportRole(context.Background(), id, *role); err != nil {
		log.Fatalf("setting support role: %v", err)
	}
	fmt.Printf("user %s support_role=%s\n", id, *role)
}

func runRevoke(svc *userDomain.Service, args []string) {
	if len(args) != 1 {
		log.Fatal("usage: supportrole revoke <user_id>")
	}
	if err := svc.SetSupportRole(context.Background(), args[0], ""); err != nil {
		log.Fatalf("revoking support role: %v", err)
	}
	fmt.Printf("user %s support_role revoked\n", args[0])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func usage() { fmt.Fprintln(os.Stderr, "usage: supportrole <set|revoke> <user_id> [flags]") }
