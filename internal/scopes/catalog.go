package scopes

// ScopeEntry is one grantable scope with human-readable descriptions.
// Descriptions live on the identity-provider side on purpose: a client-supplied
// description could lie about what a scope allows.
type ScopeEntry struct {
	Scope         string `json:"scope"          dynamodbav:"scope"`
	Description   string `json:"description"    dynamodbav:"description"`    // English
	DescriptionPT string `json:"description_pt" dynamodbav:"description_pt"` // Brazilian Portuguese
}

// ServiceScopes groups the scopes a platform service exposes.
type ServiceScopes struct {
	Service string `json:"service" dynamodbav:"sk"` // scope prefix ("account", "dfe") or "identity"
	Name    string `json:"name"    dynamodbav:"name"`
	// Audience is the aud claim value the service expects in access tokens
	// (its SERVICE_AUDIENCE config). Empty for identity/account — the IdP's own
	// audience is always included at signing time.
	Audience string       `json:"-"      dynamodbav:"audience,omitempty"`
	Scopes   []ScopeEntry `json:"scopes" dynamodbav:"scopes"`
	// Internal hides the service from GET /v1.0/scopes and the consent UI, and
	// makes its scopes non-grantable through self-service creation endpoints —
	// they are assigned to first-party confidential clients via seed only.
	Internal bool `json:"-" dynamodbav:"internal,omitempty"`
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
			{OpenID, "Confirm the user's identity (user ID)", "Confirmar a identidade do usuário (ID de usuário)"},
			{Profile, "See name and profile picture", "Ver nome e foto de perfil"},
			{Email, "See the email address", "Ver o endereço de e-mail"},
			{KYC, "See the identity verification level", "Ver o nível de verificação de identidade"},
		},
	},
	{
		Service: "account",
		Name:    "CTech Account",
		Scopes: []ScopeEntry{
			{"account:profile:read", "Read profile data", "Ler dados do perfil"},
			{"account:profile:write", "Update profile data", "Atualizar dados do perfil"},
			{"account:sessions:read", "List active sessions", "Listar sessões ativas"},
			{"account:sessions:revoke", "Revoke sessions", "Revogar sessões"},
		},
	},
	// dfe resources mirror ctech-dfe's RBAC permission names (verb.resource with
	// verbs get/list/create/update/delete). A scope's action maps mechanically:
	// read → get.* + list.*, write → create.* + update.* + delete.* — so dfe can
	// intersect token scopes with its per-organization RBAC.
	{
		Service:  "dfe",
		Name:     "CTech DFe",
		Audience: "https://dfe-api.aoctech.app",
		Scopes: []ScopeEntry{
			{"dfe:nfes:read", "Read NF-e documents and their events", "Consultar NF-e e seus eventos"},
			{"dfe:nfes:write", "Issue and cancel NF-e documents", "Emitir e cancelar NF-e"},
			{"dfe:nfces:read", "Read NFC-e documents and their events", "Consultar NFC-e e seus eventos"},
			{"dfe:nfces:write", "Issue and cancel NFC-e documents", "Emitir e cancelar NFC-e"},
			{"dfe:mdfes:read", "Read MDF-e documents and their events", "Consultar MDF-e e seus eventos"},
			{"dfe:mdfes:write", "Issue and cancel MDF-e documents", "Emitir e cancelar MDF-e"},
			{"dfe:organizations:read", "Read organizations", "Consultar organizações"},
			{"dfe:organizations:write", "Manage organizations", "Gerenciar organizações"},
			{"dfe:organization_certificates:read", "List digital certificates", "Listar certificados digitais"},
			{"dfe:organization_certificates:write", "Manage digital certificates", "Gerenciar certificados digitais"},
			{"dfe:organization_products:read", "Read products", "Consultar produtos"},
			{"dfe:organization_products:write", "Manage products", "Gerenciar produtos"},
			{"dfe:organization_persons:read", "Read persons (customers/suppliers)", "Consultar pessoas (clientes/fornecedores)"},
			{"dfe:organization_persons:write", "Manage persons (customers/suppliers)", "Gerenciar pessoas (clientes/fornecedores)"},
			{"dfe:organization_vehicles:read", "Read vehicles", "Consultar veículos"},
			{"dfe:organization_vehicles:write", "Manage vehicles", "Gerenciar veículos"},
		},
	},
	// Internal scopes are machine-to-machine only; each downstream service gets
	// its own catalog entry (Service: "internal:<service>") so AudiencesFor can
	// resolve the right aud claim per target — a single "internal" bucket
	// couldn't tell ctech-wallet's audience apart from another service's.
	{
		Service:  "internal:account",
		Name:     "Internal — CTech Account",
		Audience: "https://accounts-api.aoctech.app",
		Internal: true,
		Scopes: []ScopeEntry{
			{InternalAccountKYC, "Check KYC details", "Obter detalhes do KYC"},
		},
	},
	{
		Service:  "internal:wallet",
		Name:     "Internal — CTech Wallet",
		Audience: "https://wallet-api.aoctech.app",
		Internal: true,
		Scopes: []ScopeEntry{
			{InternalWalletConfirmDeposit, "Confirm deposits to real wallet", "Confirmar depósito na carteira"},
			{InternalWalletCredit, "Credit value for sandbox wallet", "Creditar valores na carteira virtual"},
			{InternalWalletDebit, "Debit value for sandbox wallet", "Debitar valores da carteira virtual"},
		},
	},
}

// DefaultCatalog returns the seed catalog for cmd/seedscopes and tests.
func DefaultCatalog() []ServiceScopes {
	return defaultCatalog
}
