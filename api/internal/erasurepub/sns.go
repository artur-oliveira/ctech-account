// Package erasurepub publishes account-deletion saga messages to the
// {env}-account-user-erasure SNS topic (saga protocol spec §3).
package erasurepub

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"

	"gopkg.aoctech.app/api-commons/erasure"
)

type SNS struct {
	client   *sns.Client
	topicARN string
}

func New(client *sns.Client, topicARN string) *SNS { return &SNS{client: client, topicARN: topicARN} }

// publishInput builds the SNS publish with a services attribute, so each
// participant's subscription filters with {"services": ["<name>"]}.
func publishInput(topicARN string, m erasure.Message) (*sns.PublishInput, error) {
	body, err := erasure.Encode(m)
	if err != nil {
		return nil, err
	}
	services, err := json.Marshal(m.Services)
	if err != nil {
		return nil, err
	}
	return &sns.PublishInput{
		TopicArn: aws.String(topicARN),
		Message:  aws.String(string(body)),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"services": {DataType: aws.String("String.Array"), StringValue: aws.String(string(services))},
		},
	}, nil
}

func (p *SNS) Publish(ctx context.Context, m erasure.Message) error {
	in, err := publishInput(p.topicARN, m)
	if err != nil {
		return err
	}
	_, err = p.client.Publish(ctx, in)
	return err
}
