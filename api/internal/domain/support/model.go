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

	metaSK          = "META"
	messageSKPrefix = "MSG_"
)

// ValidCategories, ValidPriorities and ValidStatuses back both the
// go-playground/validator "oneof" tags in the request DTOs and any UI
// picker — the catalog is chosen, never free-typed (mirrors the OAuth scope
// catalog convention).
var ValidCategories = []string{CategoryAccount, CategoryKYC, CategoryWallet, CategoryDFe, CategoryBilling, CategoryPoker, CategoryOther}
var ValidPriorities = []string{PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent, PriorityCritical}
var ValidStatuses = []string{StatusOpen, StatusAnswered, StatusClosed}

type Ticket struct {
	PK               string `dynamodbav:"pk"`
	SK               string `dynamodbav:"sk"`
	TicketNumber     int64  `dynamodbav:"ticket_number"`
	UserID           string `dynamodbav:"user_id,omitempty"`
	AnonymousEmail   string `dynamodbav:"anonymous_email,omitempty"`
	AnonymousToken   string `dynamodbav:"anonymous_token,omitempty"`
	SubjectCategory  string `dynamodbav:"subject_category"`
	SubjectOther     string `dynamodbav:"subject_other,omitempty"`
	Priority         string `dynamodbav:"priority"`
	Status           string `dynamodbav:"status"`
	CreatedAt        string `dynamodbav:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at"`
	ClosedAt         string `dynamodbav:"closed_at,omitempty"`
	LastMessageAt    string `dynamodbav:"last_message_at"`
	RootSESMessageID string `dynamodbav:"root_ses_message_id,omitempty"`
	LastSESMessageID string `dynamodbav:"last_ses_message_id,omitempty"`
	NPSScore         int    `dynamodbav:"nps_score,omitempty"`
	NPSMessage       string `dynamodbav:"nps_message,omitempty"`
	NPSRequestedAt   string `dynamodbav:"nps_requested_at,omitempty"`
}

// BuildPK returns the item-collection partition key for ticket id.
func BuildPK(id string) string { return "TICKET_" + id }

// ID strips the "TICKET_" prefix back off PK.
func (t *Ticket) ID() string { return strings.TrimPrefix(t.PK, "TICKET_") }

// IsAnonymous reports whether the ticket was submitted without a session.
func (t *Ticket) IsAnonymous() bool { return t.UserID == "" }

type Message struct {
	PK           string `dynamodbav:"pk"`
	SK           string `dynamodbav:"sk"`
	AuthorType   string `dynamodbav:"author_type"`
	AuthorID     string `dynamodbav:"author_id,omitempty"`
	Body         string `dynamodbav:"body"`
	CreatedAt    string `dynamodbav:"created_at"`
	SESMessageID string `dynamodbav:"ses_message_id,omitempty"`
}

// BuildMessageSK returns the sort key for a message created at createdAt
// (RFC3339Nano — nanosecond precision keeps same-instant messages ordered).
func BuildMessageSK(createdAt string) string { return messageSKPrefix + createdAt }
