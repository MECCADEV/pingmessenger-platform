package notify

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SES sends directly to the recipient inbox. The sender identity must be
// verified in SES and the workload needs ses:SendEmail permission.
type SES struct {
	client *sesv2.Client
	from   string
}

func NewSES(ctx context.Context, region, from string) (*SES, error) {
	if region == "" || from == "" {
		return nil, fmt.Errorf("AWS_REGION and AWS_SES_FROM_EMAIL are required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &SES{client: sesv2.NewFromConfig(cfg), from: from}, nil
}

func (s *SES) Send(ctx context.Context, mail *Email) error {
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination:      &types.Destination{ToAddresses: []string{mail.To}},
		Content: &types.EmailContent{Simple: &types.Message{
			Subject: &types.Content{Data: aws.String(mail.Subject), Charset: aws.String("UTF-8")},
			Body:    &types.Body{Text: &types.Content{Data: aws.String(mail.Text), Charset: aws.String("UTF-8")}},
		}},
	})
	if err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	return nil
}
