package notify

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// SNS publishes verification mail through a pre-subscribed SNS topic. IAM
// credentials come from the AWS default credential chain (IRSA in Kubernetes).
type SNS struct {
	client   *sns.Client
	topicARN string
}

type DirectSMS struct{ client *sns.Client }

func NewDirectSMS(ctx context.Context, region string) (*DirectSMS, error) {
	if region == "" {
		return nil, fmt.Errorf("AWS_REGION is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &DirectSMS{client: sns.NewFromConfig(cfg)}, nil
}
func (s *DirectSMS) SendSMS(ctx context.Context, msg *SMS) error {
	_, err := s.client.Publish(ctx, &sns.PublishInput{PhoneNumber: &msg.To, Message: &msg.Text})
	return err
}

func NewSNS(ctx context.Context, region, topicARN string) (*SNS, error) {
	if region == "" || topicARN == "" {
		return nil, fmt.Errorf("AWS_REGION and AWS_SNS_TOPIC_ARN are required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &SNS{client: sns.NewFromConfig(cfg), topicARN: topicARN}, nil
}
func (s *SNS) Send(ctx context.Context, mail *Email) error {
	_, err := s.client.Publish(ctx, &sns.PublishInput{TopicArn: &s.topicARN, Subject: &mail.Subject, Message: &mail.Text})
	if err != nil {
		return fmt.Errorf("publish verification email: %w", err)
	}
	return nil
}
