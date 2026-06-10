package geo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const lookupTimeout = 3 * time.Second

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	City      string  `json:"cityName"`
	Region    string  `json:"regionName"`
}

var httpClient = &http.Client{Timeout: lookupTimeout}

// Lookup fetches geolocation data for a single IP address.
// Returns an error if the API is unreachable or returns unexpected data.
func Lookup(ip string) (*Location, error) {
	resp, err := httpClient.Get("https://free.freeipapi.com/api/json/" + ip)
	if err != nil {
		return nil, fmt.Errorf("geo lookup request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geo lookup status: %d", resp.StatusCode)
	}

	var loc Location
	if err := json.NewDecoder(resp.Body).Decode(&loc); err != nil {
		return nil, fmt.Errorf("geo lookup decode: %w", err)
	}
	return &loc, nil
}
