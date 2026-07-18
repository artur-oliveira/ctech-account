// Command kyc is the operator tool for manual KYC review: KYC has no admin
// UI, so a reviewer lists pending submissions, opens the presigned document
// URLs in a browser, and approves or rejects from here.
//
//	AWS_REGION=... TABLE_PREFIX=production KYC_DOCUMENTS_BUCKET=... go run ./cmd/kyc list
//	... go run ./cmd/kyc show <user_id>
//	... go run ./cmd/kyc approve <user_id> [-note "looks good"]
//	... go run ./cmd/kyc reject <user_id> -reason "blurry photo"
//
// TABLE_PREFIX falls back to ENVIRONMENT (same rule as the API config).
// KYC_DOCUMENTS_BUCKET is required for `show` (presigned document URLs); list,
// approve, and reject work without it.
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
	auditDomain "gopkg.aoctech.app/account/api/internal/domain/audit"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/storage"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

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
	userRepo := userDomain.NewRepository(db, tablePrefix)
	kycRepo := kycDomain.NewRepository(db, tablePrefix, userRepo)

	var presigner kycDomain.Presigner
	if bucket := os.Getenv("KYC_DOCUMENTS_BUCKET"); bucket != "" {
		s3Cli, err := storage.NewS3(ctx, region, bucket)
		if err != nil {
			log.Fatalf("initializing document storage: %v", err)
		}
		presigner = s3Cli
	}
	kycSvc := kycDomain.NewService(kycRepo, presigner)
	auditSvc := auditDomain.NewService(auditDomain.NewRepository(db, tablePrefix))

	switch os.Args[1] {
	case "list":
		runList(ctx, kycSvc)
	case "show":
		runShow(ctx, kycSvc, os.Args[2:])
	case "approve":
		runReview(ctx, kycSvc, auditSvc, kycDomain.DecisionApprove, os.Args[2:])
	case "reject":
		runReview(ctx, kycSvc, auditSvc, kycDomain.DecisionReject, os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kyc <list|show|approve|reject> [args]")
}

func runList(ctx context.Context, kycSvc *kycDomain.Service) {
	users, err := kycSvc.ListPendingKYC(ctx)
	if err != nil {
		log.Fatalf("listing pending kyc: %v", err)
	}
	if len(users) == 0 {
		fmt.Println("no submissions pending review")
		return
	}
	fmt.Printf("%-36s  %-30s  %s\n", "user_id", "legal_name", "submitted_at")
	for _, u := range users {
		fmt.Printf("%-36s  %-30s  %s\n", u.ID(), u.LegalName, u.KYCSubmittedAt)
	}
}

func runShow(ctx context.Context, kycSvc *kycDomain.Service, args []string) {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	fs.Parse(args)
	userID := fs.Arg(0)
	if userID == "" {
		log.Fatal("usage: kyc show <user_id>")
	}

	u, err := kycSvc.GetUser(ctx, userID)
	if err != nil {
		log.Fatalf("fetching user: %v", err)
	}
	fmt.Printf("user_id:      %s\n", u.ID())
	fmt.Printf("legal_name:   %s\n", u.LegalName)
	fmt.Printf("cpf:          %s\n", u.CPF)
	fmt.Printf("birth_date:   %s\n", u.BirthDate)
	fmt.Printf("address:      %+v\n", u.Address)
	fmt.Printf("doc_status:   %s\n", u.KYCDocStatus)
	fmt.Printf("submitted_at: %s\n", u.KYCSubmittedAt)

	urls, err := kycSvc.DocumentURLs(ctx, userID)
	if errors.Is(err, kycDomain.ErrInvalidMethod) {
		log.Fatal("KYC_DOCUMENTS_BUCKET is not set — cannot presign document URLs")
	}
	if err != nil {
		log.Fatalf("fetching document urls: %v", err)
	}
	fmt.Println("documents:")
	for _, d := range urls {
		fmt.Printf("  %-14s uploaded_at=%s\n    %s\n", d.Type, d.UploadedAt, d.URL)
	}
}

func runReview(ctx context.Context, kycSvc *kycDomain.Service, auditSvc *auditDomain.Service, decision string, args []string) {
	fs := flag.NewFlagSet(decision, flag.ExitOnError)
	note := fs.String("note", "", "operator note (approve only, not persisted)")
	reason := fs.String("reason", "", "rejection reason shown to the user (required for reject)")
	fs.Parse(args)
	userID := fs.Arg(0)
	if userID == "" {
		log.Fatalf("usage: kyc %s <user_id> [-reason \"...\"]", decision)
	}
	if decision == kycDomain.DecisionReject && *reason == "" {
		log.Fatal("-reason is required to reject a submission")
	}

	reasonArg := *reason
	if decision == kycDomain.DecisionApprove {
		reasonArg = ""
	}

	if err := kycSvc.Review(ctx, userID, decision, reasonArg); err != nil {
		log.Fatalf("%s: %v", decision, err)
	}

	event := auditDomain.EventKYCVerified
	if decision == kycDomain.DecisionReject {
		event = auditDomain.EventKYCRejected
	}
	auditSvc.Record(ctx, auditDomain.Entry{
		UserID: userID,
		Type:   event,
		Metadata: map[string]string{
			"reviewer": "cli",
			"note":     *note,
		},
	})

	fmt.Printf("%s: %s\n", decision, userID)
}
