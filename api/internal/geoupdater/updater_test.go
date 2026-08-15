package geoupdater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/geo"
)

// buildFixtureTarGz packs the geo package's test-fixture .mmdb into a
// tar.gz matching the shape of MaxMind's real download response (one
// directory entry containing a versioned dir, then the .mmdb file inside it
// — the extractor must find the .mmdb by extension, not by a fixed path).
func buildFixtureTarGz(t *testing.T) []byte {
	t.Helper()
	mmdbBytes, err := os.ReadFile("../geo/testdata/GeoIP2-City-Test.mmdb")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "GeoLite2-City_20260101/GeoLite2-City.mmdb"
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(mmdbBytes)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(mmdbBytes); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUpdateDownloadsExtractsValidatesAndSwaps(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	fixture := buildFixtureTarGz(t)
	var gotAuthUser, gotAuthPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthUser, gotAuthPass, _ = r.BasicAuth()
		w.Write(fixture)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := Config{
		DBPath:      filepath.Join(dir, "GeoLite2-City.mmdb"),
		AccountID:   "acct123",
		LicenseKey:  "key456",
		downloadURL: srv.URL,
		HTTPClient:  srv.Client(),
	}

	if err := update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if gotAuthUser != "acct123" || gotAuthPass != "key456" {
		t.Errorf("expected basic auth acct123:key456, got %s:%s", gotAuthUser, gotAuthPass)
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		t.Errorf("expected file at %s: %v", cfg.DBPath, err)
	}

	loc := geo.Lookup("81.2.69.142")
	if loc.City != "London" {
		t.Errorf("expected geo.Lookup to use the newly swapped reader, got %+v", loc)
	}
}

func TestUpdateRejectsInvalidArchive(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a tar.gz"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := Config{
		DBPath:      filepath.Join(dir, "GeoLite2-City.mmdb"),
		AccountID:   "acct123",
		LicenseKey:  "key456",
		downloadURL: srv.URL,
		HTTPClient:  srv.Client(),
	}

	if err := update(context.Background(), cfg); err == nil {
		t.Error("expected an error for a non-tar.gz response")
	}
	if _, err := os.Stat(cfg.DBPath); !os.IsNotExist(err) {
		t.Error("a failed update must not leave a partial file at DBPath")
	}
}

func TestMaybeUpdateSkipsWhenFileIsFresh(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "GeoLite2-City.mmdb")
	if err := os.WriteFile(dbPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	cfg := Config{
		DBPath: dbPath, AccountID: "a", LicenseKey: "k",
		downloadURL: srv.URL, HTTPClient: srv.Client(),
		StaleAfter: 7 * 24 * time.Hour,
		Now:        time.Now,
	}
	if err := maybeUpdate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("must not download when the existing file is younger than StaleAfter")
	}
}

func TestStartupOpensExistingFileWithoutDownloading(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "GeoLite2-City.mmdb")
	fixture, err := os.ReadFile("../geo/testdata/GeoIP2-City-Test.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	Startup(context.Background(), Config{DBPath: dbPath, downloadURL: srv.URL, HTTPClient: srv.Client()})

	if called {
		t.Error("Startup must not download when a file already exists at DBPath")
	}
	if loc := geo.Lookup("81.2.69.142"); loc.City != "London" {
		t.Errorf("expected Startup to load the existing file into geo.reader, got %+v", loc)
	}
}

func TestStartupDegradesGeoWhenNoFileAndDownloadFails(t *testing.T) {
	geo.SetReader(nil)
	defer geo.SetReader(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dir := t.TempDir()
	Startup(context.Background(), Config{
		DBPath:      filepath.Join(dir, "GeoLite2-City.mmdb"),
		AccountID:   "bad", LicenseKey: "bad",
		downloadURL: srv.URL, HTTPClient: srv.Client(),
	})

	// Must not panic or block forever; geo lookups just stay disabled.
	if loc := geo.Lookup("81.2.69.142"); loc != (geo.Location{}) {
		t.Errorf("expected geo disabled after failed startup download, got %+v", loc)
	}
}
