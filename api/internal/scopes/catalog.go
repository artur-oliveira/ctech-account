package scopes

// ScopeEntry is one grantable scope with human-readable descriptions.
// Descriptions live on the identity-provider side on purpose: a client-supplied
// description could lie about what a scope allows.
type ScopeEntry struct {
	Scope         string `json:"scope"          dynamodbav:"scope"`
	Description   string `json:"description"    dynamodbav:"description"`    // English
	DescriptionPT string `json:"description_pt" dynamodbav:"description_pt"` // Brazilian Portuguese
	// M2M is ScopeDefinition.M2M carried into the catalog. Serialized in JSON
	// (not json:"-") because the Valkey catalog cache round-trips it.
	M2M bool `json:"m2m,omitempty" dynamodbav:"m2m,omitempty"`
}

// ServiceScopes groups the scopes a platform service exposes.
type ServiceScopes struct {
	Service string `json:"service" dynamodbav:"sk"` // scope prefix ("account", "dfe") or "identity"
	Name    string `json:"name"    dynamodbav:"name"`
	// Audience is the aud claim value the service expects in access tokens
	// (its SERVICE_AUDIENCE config). Empty for identity/account — the IdP's own
	// audience is always included at signing time. Serialized in JSON so the
	// Valkey catalog cache round-trips it (json:"-" here previously dropped it,
	// which both emptied AudiencesFor from cache and — because ValidateGrantable
	// skips Internal services — made cached internal M2M scopes grantable).
	Audience string       `json:"audience,omitempty" dynamodbav:"audience,omitempty"`
	Scopes   []ScopeEntry `json:"scopes" dynamodbav:"scopes"`
	// Internal hides the service from GET /v1.0/scopes and the consent UI, and
	// makes its scopes non-grantable through self-service creation endpoints —
	// they are assigned to first-party confidential clients via seed only.
	Internal bool `json:"internal,omitempty" dynamodbav:"internal,omitempty"`
}

// PublicServiceScopes is the catalog shape exposed by GET /v1.0/scopes. It
// deliberately omits Audience (an internal routing claim) and the Internal
// flag — those are stripped here rather than via json:"-" on ServiceScopes,
// because the cache needs them.
type PublicServiceScopes struct {
	Service string       `json:"service"`
	Name    string       `json:"name"`
	Scopes  []ScopeEntry `json:"scopes"`
}

// ToPublic projects a ServiceScopes into its public, client-safe form.
func (s ServiceScopes) ToPublic() PublicServiceScopes {
	return PublicServiceScopes{Service: s.Service, Name: s.Name, Scopes: s.Scopes}
}

// IdentityService is the pseudo-service grouping OIDC scopes. Valid only for
// OAuth clients (human sign-in) — never for API keys.
const IdentityService = "identity"

// InternalServicePrefix groups machine-to-machine scopes. Internal services are
// hidden from the public catalog and only assignable to first-party
// confidential clients via seed — never through the self-service API.
const InternalServicePrefix = "internal"

// InternalAccountKYC lets a service check the KYC of a user
const InternalAccountKYC = "internal:account:kyc"

// InternalAccountErasureAck lets a deletion-saga participant report its result.
const InternalAccountErasureAck = "internal:account:erasure-ack"

// InternalAccountErasureSubject lets a participant read the CPF of a user being
// purged, to match records keyed by CPF (dfe e-CPF certificates). Purging only.
const InternalAccountErasureSubject = "internal:account:erasure-subject"

// InternalAccountCompanyActor lets a product ask whether a person may act for a
// company — the reach half of ctech-billing ADR 0023, where the platform owns
// who may reach a company and the product owns what they may do there.
//
// It answers reach and the organization it found, never a role: a role here
// would be the platform holding a product's vocabulary, which is the argument
// that ADR settles.
const InternalAccountCompanyActor = "internal:account:company-actor"

// InternalAccountOrgMember lets a product ask whether a person belongs to an
// organization and with which ladder role — the membership half of ctech-billing
// ADR 0025 (a finance space is selected by the browser and authorized here).
//
// Separate from InternalAccountCompanyActor on purpose: that one answers reach
// for a company and carries no role; this one answers membership of a workspace
// and carries the role. A client that needs one must not be able to ask the
// other.
const InternalAccountOrgMember = "internal:account:org-member"

// InternalAccountUserOrganizations lets a product list every organization a
// person belongs to, with their role in each, to build a space switcher. It is a
// wider grant than InternalAccountOrgMember (enumerate a user's workspaces vs
// check one membership), so it is its own scope: a client that only needs to
// authorize a request must not also be able to enumerate.
const InternalAccountUserOrganizations = "internal:account:user-organizations"

// InternalAccountOAuthClientRead lets a product read one OAuth client's owner,
// granted scopes and whether it is active, never its secret, so it can check
// that the person connecting a client to the product owns it (ctech-billing
// console self-service spec § 6.3, D5).
//
// Its own scope: reading any client by id is not a membership question, and a
// client that resolves memberships must not gain it by sharing a grant.
const InternalAccountOAuthClientRead = "internal:account:oauth-client-read"

// InternalAccountUserContactRead lets a product read one person's verified
// contact (e-mail, first name, whether the e-mail is verified) to send them a
// message that must reach the account's own address, never one a requester typed
// (ctech-billing merchant tenant closure, spec 2026-10-17). Its own scope: it
// reads a person's e-mail, which no membership check needs.
const InternalAccountUserContactRead = "internal:account:user-contact-read"

// InternalAccountScopeRegistryWrite authorizes a dedicated resource-server
// publisher to reconcile the scope manifest bound to its OAuth client. It is
// an Account-owned root permission and therefore remains in the built-in
// catalog even after downstream service scopes move to their own repositories.
const InternalAccountScopeRegistryWrite = "internal:account:scope-registry:write"

// InternalWalletConfirmDeposit lets a service confirm a deposit (currently via Pix)
const InternalWalletConfirmDeposit = "internal:wallet:confirm-deposit"

// InternalWalletCredit lets a service permission to credit values on sandbox wallet
const InternalWalletCredit = "internal:wallet:credit"

// InternalWalletDebit lets a service permission to debit values on sandbox wallet
const InternalWalletDebit = "internal:wallet:debit"

// FilterPublic strips internal services from a catalog listing.
func FilterPublic(services []ServiceScopes) []ServiceScopes {
	out := make([]ServiceScopes, 0, len(services))
	for _, s := range services {
		if !s.Internal {
			out = append(out, s)
		}
	}
	return out
}

// defaultCatalog is the seed catalog shipped with this repo. The runtime
// source of truth is the {env}_ctech_scopes DynamoDB table (see CatalogService);
// cmd/seedscopes writes this seed there. Keeping the seed in code preserves
// code review and versioning of scope codes and descriptions.
var defaultCatalog = []ServiceScopes{
	{
		Service: IdentityService,
		Name:    "Identity (OpenID Connect)",
		Scopes: []ScopeEntry{
			{Scope: OpenID, Description: "Confirm the user's identity (user ID)", DescriptionPT: "Confirmar a identidade do usuário (ID de usuário)"},
			{Scope: Profile, Description: "See name and profile picture", DescriptionPT: "Ver nome e foto de perfil"},
			{Scope: Email, Description: "See the email address", DescriptionPT: "Ver o endereço de e-mail"},
			{Scope: KYC, Description: "See the identity verification level", DescriptionPT: "Ver o nível de verificação de identidade"},
		},
	},
	// Account's API permissions are bootstrapped from the embedded v2 Resource
	// Server manifest. They intentionally do not remain duplicated here.
	// dfe resources mirror ctech-dfe's RBAC permission names (verb.resource with
	// verbs get/list/create/update/delete). A scope's action maps mechanically:
	// read → get.* + list.*, write → create.* + update.* + delete.* — so dfe can
	// intersect token scopes with its per-organization RBAC.
	{
		Service:  "dfe",
		Name:     "CTech DFe",
		Audience: "https://dfe.aoctech.app",
		Scopes: []ScopeEntry{
			{Scope: "dfe:nfes:read", Description: "Read NF-e documents and their events", DescriptionPT: "Consultar NF-e e seus eventos"},
			{Scope: "dfe:nfes:write", Description: "Issue and cancel NF-e documents", DescriptionPT: "Emitir e cancelar NF-e"},
			{Scope: "dfe:nfces:read", Description: "Read NFC-e documents and their events", DescriptionPT: "Consultar NFC-e e seus eventos"},
			{Scope: "dfe:nfces:write", Description: "Issue and cancel NFC-e documents", DescriptionPT: "Emitir e cancelar NFC-e"},
			{Scope: "dfe:mdfes:read", Description: "Read MDF-e documents and their events", DescriptionPT: "Consultar MDF-e e seus eventos"},
			{Scope: "dfe:mdfes:write", Description: "Issue and cancel MDF-e documents", DescriptionPT: "Emitir e cancelar MDF-e"},
			{Scope: "dfe:organizations:read", Description: "Read organizations", DescriptionPT: "Consultar organizações"},
			{Scope: "dfe:organizations:write", Description: "Manage organizations", DescriptionPT: "Gerenciar organizações"},
			{Scope: "dfe:organization_certificates:read", Description: "List digital certificates", DescriptionPT: "Listar certificados digitais"},
			{Scope: "dfe:organization_certificates:write", Description: "Manage digital certificates", DescriptionPT: "Gerenciar certificados digitais"},
			{Scope: "dfe:organization_products:read", Description: "Read products", DescriptionPT: "Consultar produtos"},
			{Scope: "dfe:organization_products:write", Description: "Manage products", DescriptionPT: "Gerenciar produtos"},
			{Scope: "dfe:organization_persons:read", Description: "Read persons (customers/suppliers)", DescriptionPT: "Consultar pessoas (clientes/fornecedores)"},
			{Scope: "dfe:organization_persons:write", Description: "Manage persons (customers/suppliers)", DescriptionPT: "Gerenciar pessoas (clientes/fornecedores)"},
			{Scope: "dfe:organization_vehicles:read", Description: "Read vehicles", DescriptionPT: "Consultar veículos"},
			{Scope: "dfe:organization_vehicles:write", Description: "Manage vehicles", DescriptionPT: "Gerenciar veículos"},
		},
	},
	// Internal scopes are machine-to-machine only; each downstream service gets
	// its own catalog entry (Service: "internal:<service>") so AudiencesFor can
	// resolve the right aud claim per target — a single "internal" bucket
	// couldn't tell ctech-wallet's audience apart from another service's.
	{
		Service:  "internal:account",
		Name:     "Internal — CTech Account bootstrap",
		Audience: "https://accounts.aoctech.app",
		Internal: true,
		Scopes: []ScopeEntry{
			// This root permission must exist before the Account Resource Server
			// can bootstrap. Once v2 is present, its manifest overrides this row.
			{Scope: InternalAccountScopeRegistryWrite, Description: "Publish a resource server scope manifest", DescriptionPT: "Publicar o manifesto de escopos de um servidor de recursos"},
		},
	},
	{
		Service:  "internal:wallet",
		Name:     "Internal — CTech Wallet",
		Audience: "https://wallet.aoctech.app",
		Internal: true,
		Scopes: []ScopeEntry{
			{Scope: InternalWalletConfirmDeposit, Description: "Confirm deposits to real wallet", DescriptionPT: "Confirmar depósito na carteira"},
			{Scope: InternalWalletCredit, Description: "Credit value for sandbox wallet", DescriptionPT: "Creditar valores na carteira virtual"},
			{Scope: InternalWalletDebit, Description: "Debit value for sandbox wallet", DescriptionPT: "Debitar valores da carteira virtual"},
			{Scope: "internal:wallet:balance", Description: "Check wallet balance", DescriptionPT: "Ver saldo da carteira"},
			{Scope: "internal:wallet:debit-real", Description: "Debit value for real wallet", DescriptionPT: "Debitar valores da carteira (Dinheiro real)"},
			{Scope: "internal:wallet:game-status", Description: "Check wallet game status", DescriptionPT: "Verifica status da carteira de jogos"},
			{Scope: "internal:wallet:game-hold", Description: "Hold value for game wallet", DescriptionPT: "Reserva os valores da carteira de jogos"},
			{Scope: "internal:wallet:game-cashout", Description: "Cashout value for real wallet", DescriptionPT: "Retorna valores para a carteira de jogos"},
			{Scope: "internal:wallet:hold", Description: "Hold, release and settle value in games", DescriptionPT: "Reserva, libera e liquida saldo em jogos"},
			{Scope: "internal:wallet:responsible-export", Description: "Export responsible-gambling state", DescriptionPT: "Exporta o estado de jogo responsável"},
			{Scope: "internal:wallet:sandbox-purchase", Description: "Purchases sandbox credits", DescriptionPT: "Compra créditos sandbox"},
			{Scope: "internal:wallet:product-purchase", Description: "Purchase product", DescriptionPT: "Compra de produtos"},
		},
	},
}

// DefaultCatalog returns the seed catalog for cmd/seedscopes and tests.
func DefaultCatalog() []ServiceScopes {
	return defaultCatalog
}
