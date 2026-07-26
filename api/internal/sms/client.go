// Package sms sends one-time verification codes via AWS SNS direct-to-phone
// Publish — no SNS topic is created or needed for this pattern. Mirrors
// internal/email's SES wrapper shape.
package sms

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"gopkg.aoctech.app/api-commons/awsconfig"
)

type Client struct {
	sns *sns.Client
}

func New(ctx context.Context, region string) (*Client, error) {
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Client{sns: sns.NewFromConfig(cfg)}, nil
}

// SendOTP publishes a phone-verification code directly to phoneE164.
func (c *Client) SendOTP(ctx context.Context, phoneE164, code string) error {
	_, err := c.sns.Publish(ctx, &sns.PublishInput{
		PhoneNumber: aws.String(phoneE164),
		Message:     aws.String(fmt.Sprintf("Your verification code is %s. Valid for 10 minutes.", code)),
	})
	return err
}
