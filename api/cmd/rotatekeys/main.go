// Command rotatekeys manages the versioned RS256 signing keys in SSM.
//
//	rotatekeys -env prod -init   # one-time: wrap legacy rsa-private-key into jwk/active (KID preserved)
//	rotatekeys -env prod         # manual rotation: new active key, old active becomes previous
//
// Instances reload keys from SSM hourly, so a rotation propagates without a
// deploy; the previous key stays in JWKS until the next rotation.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"gopkg.aoctech.app/account/api/internal/keystore"
)

func main() {
	env := flag.String("env", "", "environment (e.g. prod)")
	initMode := flag.Bool("init", false, "wrap legacy rsa-private-key parameter into jwk/active")
	flag.Parse()
	if *env == "" {
		log.Fatal("-env is required")
	}

	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("loading AWS config: %v", err)
	}
	client := ssm.NewFromConfig(awsCfg)
	store := keystore.NewStore(client, *env)

	if *initMode {
		if err := keystore.InitFromLegacy(ctx, store, client, time.Now()); err != nil {
			log.Fatalf("init: %v", err)
		}
		log.Println("legacy key wrapped into jwk/active (KID preserved)")
		return
	}

	newKey, err := keystore.Rotate(ctx, store, time.Now())
	if err != nil {
		log.Fatalf("rotate: %v", err)
	}
	log.Printf("rotated: new active kid=%s (instances pick it up within 1h; previous kid stays in JWKS)", newKey.KID)
}
