// Package registry looks a CNPJ up in a public register so a person does not
// retype the Receita Federal.
//
// It is a convenience and never a gate: every failure returns "no answer", and
// the caller lets the person type the names. A lookup that could block a
// registration would make a third party's uptime our own.
package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// cnpjaEndpoint is the open, unauthenticated tier. The canonical CNPJ is
// appended.
const cnpjaEndpoint = "https://open.cnpja.com/office/"

// maxBody caps what is read from a third party we do not control.
const maxBody = 64 << 10

// Names is what a register can tell us that a person would otherwise type.
type Names struct {
	LegalName string
	TradeName string
}

// Lookup has no error return, deliberately. Every caller's response to every
// failure is the same — let the person type the names — and an error return
// would eventually tempt one of them into treating an outage as fatal.
type Lookup interface {
	Names(ctx context.Context, cnpj string) (Names, bool)
}

type cnpja struct {
	client   *http.Client
	endpoint string
}

// NewCNPJA builds the lookup. The call is made from the API, never the browser:
// a static export calling a third party directly would hand that party the
// customer's IP and leave no audit trail.
func NewCNPJA(client *http.Client) Lookup {
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	return &cnpja{client: client, endpoint: cnpjaEndpoint}
}

type cnpjaResponse struct {
	Company struct {
		Name string `json:"name"`
	} `json:"company"`
	Alias string `json:"alias"`
}

func (l *cnpja) Names(ctx context.Context, cnpj string) (Names, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.endpoint+cnpj, nil)
	if err != nil {
		return Names{}, false
	}
	req.Header.Set("Accept", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return Names{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Names{}, false
	}
	var body cnpjaResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return Names{}, false
	}
	legal := strings.TrimSpace(body.Company.Name)
	if legal == "" {
		return Names{}, false
	}
	return Names{LegalName: legal, TradeName: strings.TrimSpace(body.Alias)}, true
}
