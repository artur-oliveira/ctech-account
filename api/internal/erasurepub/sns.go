// Package erasurepub publishes account-deletion saga messages to the
// {env}-account-user-erasure SNS topic (saga protocol spec §3).
package erasurepub

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"gopkg.aoctech.app/api-commons/erasure"
)

type SNS struct {
	client   *sns.Client
	topicARN string
}

func New(client *sns.Client, topicARN string) *SNS { return &SNS{client: client, topicARN: topicARN} }

func (p *SNS) Publish(ctx context.Context, m erasure.Message) error {
	body, err := erasure.Encode(m)
	if err != nil {
		return err
	}
	_, err = p.client.Publish(ctx, &sns.PublishInput{TopicArn: aws.String(p.topicARN), Message: aws.String(string(body))})
	return err
}
