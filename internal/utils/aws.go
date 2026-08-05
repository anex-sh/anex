package utils

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// ECRCredentials holds docker registry credentials minted from an AWS ECR
// authorization token. The token (and thus Password) is valid for 12 hours.
type ECRCredentials struct {
	Username string
	Password string
	Registry string
}

var getECRCredentialsFunc = getECRCredentials

// IsAWSECRImage reports whether the image reference points at an AWS ECR registry.
func IsAWSECRImage(image string) bool {
	return strings.Contains(image, ".dkr.ecr.") && strings.Contains(image, ".amazonaws.com")
}

func getECRCredentials(ctx context.Context, accountId string, awsRegion string) (*ECRCredentials, error) {
	logger := log.G(ctx)
	logger.Info("Getting ECR credentials for region: " + awsRegion)

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsRegion))
	if err != nil {
		logger.Errorf("Error loading AWS config: %v", err)
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ecrClient := ecr.NewFromConfig(cfg)

	output, err := ecrClient.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		logger.Errorf("Error getting ECR authorization token: %v", err)
		return nil, fmt.Errorf("failed to get ECR authorization token: %w", err)
	}

	if len(output.AuthorizationData) == 0 {
		return nil, fmt.Errorf("no authorization data returned from ECR")
	}

	authData := output.AuthorizationData[0]
	authToken := *authData.AuthorizationToken

	decodedToken, err := base64.StdEncoding.DecodeString(authToken)
	if err != nil {
		logger.Errorf("Error decoding authorization token: %v", err)
		return nil, fmt.Errorf("failed to decode authorization token: %w", err)
	}

	// The token is in the format "username:password"
	parts := strings.SplitN(string(decodedToken), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid authorization token format")
	}

	return &ECRCredentials{
		Username: parts[0],
		Password: parts[1],
		Registry: fmt.Sprintf("https://%s.dkr.ecr.%s.amazonaws.com", accountId, awsRegion),
	}, nil
}

// GetAWSECRCredentials mints fresh credentials for the ECR registry the image
// belongs to, using the default AWS credential chain.
func GetAWSECRCredentials(ctx context.Context, image string) (*ECRCredentials, error) {
	parts := strings.Split(image, ".")
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected ECR image reference: %s", image)
	}
	accountID := parts[0]
	region := parts[3]

	return getECRCredentialsFunc(ctx, accountID, region)
}

// GetAWSECRLogin returns ECR credentials in Vast.AI's docker login format
// ("-u <user> -p <password> <registry>"), or "" if they cannot be obtained.
func GetAWSECRLogin(ctx context.Context, image string) string {
	logger := log.G(ctx)
	creds, err := GetAWSECRCredentials(ctx, image)
	if err != nil {
		logger.Warnf("Error getting ECR credentials: %v", err)
		return ""
	}
	return fmt.Sprintf("-u %s -p %s %s", creds.Username, creds.Password, creds.Registry)
}
