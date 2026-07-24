package keystore

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeSSM is a map-backed SSMAPI that records write order.
type fakeSSM struct {
	params   map[string]string
	putOrder []string
	getErr   error
}

func newFakeSSM() *fakeSSM {
	return &fakeSSM{params: map[string]string{}}
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.params[aws.ToString(in.Name)]
	if !ok {
		return nil, &types.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: aws.String(v)}}, nil
}

func (f *fakeSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	name := aws.ToString(in.Name)
	if in.Type != types.ParameterTypeSecureString {
		panic("keys must be SecureString")
	}
	f.params[name] = aws.ToString(in.Value)
	f.putOrder = append(f.putOrder, name)
	return &ssm.PutParameterOutput{}, nil
}

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	fake := newFakeSSM()
	store := NewStore(fake, "test")
	active, _ := Generate(time.Now())
	previous, _ := Generate(time.Now().Add(-time.Hour))

	if err := store.Save(context.Background(), active, previous); err != nil {
		t.Fatal(err)
	}
	// previous written before active (crash safety)
	if len(fake.putOrder) != 2 || fake.putOrder[0] != store.previousPath() || fake.putOrder[1] != store.activePath() {
		t.Errorf("write order: %v", fake.putOrder)
	}

	gotActive, gotPrevious, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotActive.KID != active.KID || gotPrevious == nil || gotPrevious.KID != previous.KID {
		t.Errorf("round trip: active=%s previous=%v", gotActive.KID, gotPrevious)
	}
}

func TestLoadMissingPreviousIsNil(t *testing.T) {
	fake := newFakeSSM()
	store := NewStore(fake, "test")
	active, _ := Generate(time.Now())
	if err := store.Save(context.Background(), active, nil); err != nil {
		t.Fatal(err)
	}
	got, previous, err := store.Load(context.Background())
	if err != nil || previous != nil {
		t.Fatalf("err=%v previous=%v", err, previous)
	}
	if got.KID != active.KID {
		t.Errorf("active kid mismatch")
	}
}

func TestLoadMissingActiveIsError(t *testing.T) {
	store := NewStore(newFakeSSM(), "test")
	if _, _, err := store.Load(context.Background()); err == nil {
		t.Error("expected error when active key is missing")
	}
}

func TestRotatePromotesActiveToPrevious(t *testing.T) {
	fake := newFakeSSM()
	store := NewStore(fake, "test")
	first, _ := Generate(time.Now().Add(-100 * 24 * time.Hour))
	_ = store.Save(context.Background(), first, nil)

	newKey, err := Rotate(context.Background(), store, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	active, previous, _ := store.Load(context.Background())
	if active.KID != newKey.KID || previous == nil || previous.KID != first.KID {
		t.Errorf("active=%s previous=%v", active.KID, previous)
	}
}

func TestInitFromLegacyRefusesWhenActiveExists(t *testing.T) {
	fake := newFakeSSM()
	store := NewStore(fake, "test")
	active, _ := Generate(time.Now())
	_ = store.Save(context.Background(), active, nil)

	if err := InitFromLegacy(context.Background(), store, fake, time.Now()); err == nil {
		t.Error("expected refusal when jwk/active exists")
	}
}

func TestInitFromLegacyWrapsPEMPreservingKID(t *testing.T) {
	fake := newFakeSSM()
	store := NewStore(fake, "test")

	legacy, _ := Generate(time.Now())
	j, _ := legacy.ToJSON()
	fake.params["/ctech-account/test/rsa-private-key"] = j.PEM

	if err := InitFromLegacy(context.Background(), store, fake, time.Now()); err != nil {
		t.Fatal(err)
	}
	active, previous, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if previous != nil {
		t.Error("init must not create a previous key")
	}
	wantKID, _ := DeriveKID(&legacy.Private.PublicKey)
	if active.KID != wantKID {
		t.Errorf("KID changed on wrap: %s != %s", active.KID, wantKID)
	}
}
