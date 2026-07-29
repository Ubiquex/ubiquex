package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
)

// NewTaggingAPI loads AWS credentials/config the standard way
// (environment, ~/.aws/credentials, SSO, instance role — whatever
// config.LoadDefaultConfig already resolves; ubx doesn't reimplement
// credential discovery), mirroring audit/cloudtrail.New's own identical
// convention exactly, and returns a real TaggingAPI scoped to region.
func NewTaggingAPI(ctx context.Context, region string) (TaggingAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("discovery: load AWS config: %w", err)
	}
	return resourcegroupstaggingapi.NewFromConfig(cfg), nil
}
