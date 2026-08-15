package geo

import (
	"testing"

	"github.com/oschwald/geoip2-golang"
)

const testDBPath = "testdata/GeoIP2-City-Test.mmdb"

// knownTestIP is one of MaxMind's own fixture entries: London, GB.
const knownTestIP = "81.2.69.142"

func TestLookupReturnsZeroValueWhenNoReaderSet(t *testing.T) {
	SetReader(nil)
	got := Lookup(knownTestIP)
	if got != (Location{}) {
		t.Errorf("expected zero Location with no reader set, got %+v", got)
	}
}

func TestLookupReturnsZeroValueForUnparseableIP(t *testing.T) {
	r, err := geoip2.Open(testDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	SetReader(r)
	defer SetReader(nil)

	got := Lookup("not-an-ip")
	if got != (Location{}) {
		t.Errorf("expected zero Location for unparseable IP, got %+v", got)
	}
}

func TestLookupReturnsPopulatedLocationForKnownIP(t *testing.T) {
	r, err := geoip2.Open(testDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	SetReader(r)
	defer SetReader(nil)

	got := Lookup(knownTestIP)
	want := Location{City: "London", Region: "England", Country: "GB", Latitude: 51.5142, Longitude: -0.0931}
	if got != want {
		t.Errorf("Lookup(%q) = %+v, want %+v", knownTestIP, got, want)
	}
}

func TestLookupReturnsZeroValueForIPNotInDatabase(t *testing.T) {
	r, err := geoip2.Open(testDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	SetReader(r)
	defer SetReader(nil)

	// 0.0.0.0 has no city record in MaxMind's test fixture. geoip2's City()
	// returns a nil error with an all-zero-value City struct in this case
	// (not an error) — Lookup's field-by-field copy naturally yields a zero
	// Location either way, so no special-casing is needed in Lookup itself.
	got := Lookup("0.0.0.0")
	if got != (Location{}) {
		t.Errorf("expected zero Location for an address with no city record, got %+v", got)
	}
}
