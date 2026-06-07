package user

import "strings"

const tablePrefix = "ctech_users"

type User struct {
	PK           string `dynamodbav:"pk"`
	Email        string `dynamodbav:"email"`
	PasswordHash string `dynamodbav:"password_hash"`
	FirstName    string `dynamodbav:"first_name"`
	LastName     string `dynamodbav:"last_name"`
	DisplayName  string `dynamodbav:"display_name,omitempty"`
	AvatarURL    string `dynamodbav:"avatar_url,omitempty"`
	EmailVerified bool  `dynamodbav:"email_verified"`
	IsEnabled    bool   `dynamodbav:"is_enabled"`
	CreatedAt    string `dynamodbav:"created_at"`
	UpdatedAt    string `dynamodbav:"updated_at"`
}

func BuildPK(userID string) string {
	return "USER_" + userID
}

func (u *User) ID() string {
	return strings.TrimPrefix(u.PK, "USER_")
}

func (u *User) FullName() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

func (u *User) DisplayOrFullName() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.FullName()
}
