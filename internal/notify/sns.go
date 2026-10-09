package notify

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
)

// SNS publishes verification mail through a pre-subscribed SNS topic. IAM
// credentials come from the AWS default credential chain (IRSA in Kubernetes).
type SNS struct {
	client   *sns.Client
	topicARN string
}

type DirectSMS struct {
	client         *sns.Client
	mirrorTopicARN string
}

func NewDirectSMS(ctx context.Context, region, mirrorTopicARN string) (*DirectSMS, error) {
	if region == "" {
		return nil, fmt.Errorf("AWS_REGION is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &DirectSMS{client: sns.NewFromConfig(cfg), mirrorTopicARN: mirrorTopicARN}, nil
}
func (s *DirectSMS) SendSMS(ctx context.Context, msg *SMS) error {
	_, err := s.client.Publish(ctx, &sns.PublishInput{
		PhoneNumber: &msg.To,
		Message:     &msg.Text,
		MessageAttributes: map[string]types.MessageAttributeValue{
			"AWS.SNS.SMS.SMSType": {DataType: aws.String("String"), StringValue: aws.String("Transactional")},
		},
	})
	if err != nil {
		return err
	}
	// Explicitly opt-in test sink; production leaves SMS_MIRROR_TOPIC_ARN empty.
	if s.mirrorTopicARN != "" {
		_, _ = s.client.Publish(ctx, &sns.PublishInput{TopicArn: &s.mirrorTopicARN, Message: &msg.Text})
	}
	return nil
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
