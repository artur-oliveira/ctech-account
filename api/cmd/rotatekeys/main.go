// Command rotatekeys manages the versioned RS256/ES256 signing keys in SSM.
//
//	rotatekeys -env prod -init            # one-time: wrap legacy rsa-private-key into jwk/active (KID preserved)
//	rotatekeys -env prod                  # manual rotation: new active key, same algorithm as the current active key
//	rotatekeys -env prod -alg ES256       # algorithm cutover: new active key on the given algorithm, old active becomes previous
//
// Instances reload keys from SSM every few minutes, so a rotation propagates
// without a deploy; the previous key stays in JWKS until the next rotation.
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
	algFlag := flag.String("alg", "", "signing algorithm for the new active key (RS256 or ES256); defaults to the current active key's algorithm")
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

	alg := *algFlag
	if alg == "" {
		active, _, loadErr := store.Load(ctx)
		if loadErr != nil {
			log.Fatalf("loading current active key: %v", loadErr)
		}
		alg = active.Alg
	} else if alg != keystore.AlgRS256 && alg != keystore.AlgES256 {
		log.Fatalf("invalid -alg %q: must be %s or %s", alg, keystore.AlgRS256, keystore.AlgES256)
	}

	newKey, err := keystore.Rotate(ctx, store, time.Now(), alg)
	if err != nil {
		log.Fatalf("rotate: %v", err)
	}
	log.Printf("rotated: new active kid=%s alg=%s (instances pick it up within 5m; previous kid stays in JWKS)", newKey.KID, newKey.Alg)
}
