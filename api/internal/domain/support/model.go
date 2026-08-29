package support

import "strings"

const (
	StatusOpen     = "open"
	StatusAnswered = "answered"
	StatusClosed   = "closed"

	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityUrgent   = "urgent"
	PriorityCritical = "critical"

	CategoryAccount = "account"
	CategoryKYC     = "kyc"
	CategoryWallet  = "wallet"
	CategoryDFe     = "dfe"
	CategoryBilling = "billing"
	CategoryPoker   = "poker"
	CategoryOther   = "other"

	AuthorUser   = "user"
	AuthorAgent  = "agent"
	AuthorSystem = "system"

	EscalationNone        = "none"
	EscalationSpecialist  = "specialist"
	EscalationEngineering = "engineering"

	metaSK          = "META"
	messageSKPrefix = "MSG_"
	noteSKPrefix    = "NOTE_"
)

// ValidCategories, ValidPriorities and ValidStatuses back both the
// go-playground/validator "oneof" tags in the request DTOs and any UI
// picker — the catalog is chosen, never free-typed (mirrors the OAuth scope
// catalog convention).
var ValidCategories = []string{CategoryAccount, CategoryKYC, CategoryWallet, CategoryDFe, CategoryBilling, CategoryPoker, CategoryOther}
var ValidPriorities = []string{PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent, PriorityCritical}
var ValidStatuses = []string{StatusOpen, StatusAnswered, StatusClosed}
var ValidEscalations = []string{EscalationNone, EscalationSpecialist, EscalationEngineering}

type Ticket struct {
	PK               string `dynamodbav:"pk" json:"id"`
	SK               string `dynamodbav:"sk" json:"-"`
	TicketNumber     int64  `dynamodbav:"ticket_number" json:"ticket_number"`
	UserID           string `dynamodbav:"user_id,omitempty" json:"user_id,omitempty"`
	AnonymousEmail   string `dynamodbav:"anonymous_email,omitempty" json:"-"`
	AnonymousToken   string `dynamodbav:"anonymous_token,omitempty" json:"-"`
	SubjectCategory  string `dynamodbav:"subject_category" json:"subject_category"`
	SubjectOther     string `dynamodbav:"subject_other,omitempty" json:"subject_other,omitempty"`
	Priority         string `dynamodbav:"priority" json:"priority"`
	Status           string `dynamodbav:"status" json:"status"`
	CreatedAt        string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at" json:"updated_at"`
	ClosedAt         string `dynamodbav:"closed_at,omitempty" json:"closed_at,omitempty"`
	LastMessageAt    string `dynamodbav:"last_message_at" json:"last_message_at"`
	RootSESMessageID string `dynamodbav:"root_ses_message_id,omitempty" json:"-"`
	LastSESMessageID string `dynamodbav:"last_ses_message_id,omitempty" json:"-"`
	NPSScore         int    `dynamodbav:"nps_score,omitempty" json:"nps_score,omitempty"`
	NPSMessage       string `dynamodbav:"nps_message,omitempty" json:"nps_message,omitempty"`
	NPSRequestedAt   string `dynamodbav:"nps_requested_at,omitempty" json:"nps_requested_at,omitempty"`
	EscalationLevel  string `dynamodbav:"escalation_level,omitempty" json:"escalation_level"`
	EscalatedAt      string `dynamodbav:"escalated_at,omitempty" json:"escalated_at,omitempty"`
	EscalatedBy      string `dynamodbav:"escalated_by,omitempty" json:"escalated_by,omitempty"`
}

// BuildPK returns the item-collection partition key for ticket id.
func BuildPK(id string) string { return "TICKET_" + id }

// ID strips the "TICKET_" prefix back off PK.
func (t *Ticket) ID() string { return strings.TrimPrefix(t.PK, "TICKET_") }

// IsAnonymous reports whether the ticket was submitted without a session.
func (t *Ticket) IsAnonymous() bool { return t.UserID == "" }

type Message struct {
	PK           string `dynamodbav:"pk" json:"-"`
	SK           string `dynamodbav:"sk" json:"-"`
	AuthorType   string `dynamodbav:"author_type" json:"author_type"`
	AuthorID     string `dynamodbav:"author_id,omitempty" json:"-"`
	Body         string `dynamodbav:"body" json:"body"`
	CreatedAt    string `dynamodbav:"created_at" json:"created_at"`
	SESMessageID string `dynamodbav:"ses_message_id,omitempty" json:"-"`
}

// InternalNote is visible only to support agents. It deliberately lives in a
// separate NOTE_ sort-key namespace so public thread reads cannot leak it.
type InternalNote struct {
	PK        string `dynamodbav:"pk" json:"-"`
	SK        string `dynamodbav:"sk" json:"id"`
	AuthorID  string `dynamodbav:"author_id" json:"author_id"`
	Body      string `dynamodbav:"body" json:"body"`
	CreatedAt string `dynamodbav:"created_at" json:"created_at"`
}

type MetricBucket struct {
	Period                string           `json:"period"`
	CreatedCount          int64            `json:"created_count"`
	ResolvedCount         int64            `json:"resolved_count"`
	AverageResolutionSecs float64          `json:"average_resolution_seconds"`
	TicketsByProduct      map[string]int64 `json:"tickets_by_product"`
}

// BuildMessageSK returns the sort key for a message created at createdAt
// (RFC3339Nano — nanosecond precision keeps same-instant messages ordered).
func BuildMessageSK(createdAt string) string { return messageSKPrefix + createdAt }
func BuildNoteSK(createdAt string) string    { return noteSKPrefix + createdAt }
