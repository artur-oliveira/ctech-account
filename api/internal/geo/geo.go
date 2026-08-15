package geo

import (
	"net"
	"sync/atomic"

	"github.com/oschwald/geoip2-golang"
)

// Location is the geo-enrichment attached to a session at login. Every field
// is the empty/zero value when lookup was impossible or failed — geo data is
// best-effort and must never fail a login.
type Location struct {
	City      string
	Region    string
	Country   string // ISO 3166-1 alpha-2, e.g. "BR". Absent from the old freeipapi-based lookup.
	Latitude  float64
	Longitude float64
}

// reader holds the current MaxMind database. nil until internal/geoupdater's
// Startup populates it (or forever, if MaxMind credentials are not configured
// — Lookup degrades to always returning a zero Location).
var reader atomic.Pointer[geoip2.Reader]

// SetReader atomically swaps the active database. Called only by
// internal/geoupdater. The previous reader (if any) is not explicitly closed:
// maxminddb-golang registers a runtime.SetFinalizer that unmaps it once no
// in-flight Lookup still holds a reference and it becomes unreachable, which
// is the only way to swap a memory-mapped reader without risking a concurrent
// in-flight Lookup reading from an already-unmapped region.
func SetReader(r *geoip2.Reader) {
	reader.Store(r)
}

// Lookup returns geolocation data for ip. Returns a zero Location if no
// database is loaded, ip doesn't parse, or the address has no city record —
// callers must treat every field as best-effort and never fail on a zero
// Location.
func Lookup(ip string) Location {
	r := reader.Load()
	if r == nil {
		return Location{}
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return Location{}
	}
	rec, err := r.City(parsed)
	if err != nil {
		return Location{}
	}
	var region string
	if len(rec.Subdivisions) > 0 {
		region = rec.Subdivisions[0].Names["en"]
	}
	return Location{
		City:      rec.City.Names["en"],
		Region:    region,
		Country:   rec.Country.IsoCode,
		Latitude:  rec.Location.Latitude,
		Longitude: rec.Location.Longitude,
	}
}
