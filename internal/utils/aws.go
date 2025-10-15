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

var getECRCredentialsFunc = getECRCredentials

func getECRCredentials(ctx context.Context, accountId string, awsRegion string) (map[string]string, error) {
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

	username := parts[0]
	password := parts[1]

	return map[string]string{
		"username": username,
		"password": password,
		"registry": fmt.Sprintf("https://%s.dkr.ecr.%s.amazonaws.com", accountId, awsRegion),
	}, nil
}

func GetAWSECRLogin(ctx context.Context, image string) string {
	logger := log.G(ctx)
	parts := strings.Split(image, ".")
	accountID := parts[0]
	region := parts[3]

	ecrCredentials, err := getECRCredentialsFunc(ctx, accountID, region)
	if err != nil {
		logger.Warnf("Error getting ECR credentials: %v", err)
		return ""
	} else {
		imageLogin := fmt.Sprintf("-u %s -p %s %s", ecrCredentials["username"], ecrCredentials["password"], ecrCredentials["registry"])
		return imageLogin
	}
}
