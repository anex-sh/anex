package utils

import (
	"context"
	"testing"
)

func TestGetAWSECRLoginBuildsLoginString(t *testing.T) {
	orig := getECRCredentialsFunc
	defer func() { getECRCredentialsFunc = orig }()
	getECRCredentialsFunc = func(ctx context.Context, accountId, awsRegion string) (*ECRCredentials, error) {
		if accountId != "123456789012" || awsRegion != "eu-central-1" {
			t.Fatalf("unexpected inputs: %s %s", accountId, awsRegion)
		}
		return &ECRCredentials{
			Username: "AWS",
			Password: "secret",
			Registry: "https://123456789012.dkr.ecr.eu-central-1.amazonaws.com",
		}, nil
	}

	img := "123456789012.dkr.ecr.eu-central-1.amazonaws.com/repo:tag"
	out := GetAWSECRLogin(context.Background(), img)
	want := "-u AWS -p secret https://123456789012.dkr.ecr.eu-central-1.amazonaws.com"
	if out != want {
		t.Fatalf("unexpected output: %q want %q", out, want)
	}
}

func TestGetAWSECRLoginOnErrorReturnsEmpty(t *testing.T) {
	orig := getECRCredentialsFunc
	defer func() { getECRCredentialsFunc = orig }()
	getECRCredentialsFunc = func(ctx context.Context, accountId, awsRegion string) (*ECRCredentials, error) {
		return nil, assertErr
	}
	img := "123456789012.dkr.ecr.eu-central-1.amazonaws.com/repo:tag"
	if out := GetAWSECRLogin(context.Background(), img); out != "" {
		t.Fatalf("expected empty string on error, got %q", out)
	}
}

func TestGetAWSECRCredentialsRejectsMalformedImage(t *testing.T) {
	if _, err := GetAWSECRCredentials(context.Background(), "ubuntu:22"); err == nil {
		t.Fatalf("expected error for malformed image reference")
	}
}

func TestIsAWSECRImage(t *testing.T) {
	if !IsAWSECRImage("123456789012.dkr.ecr.eu-central-1.amazonaws.com/repo:tag") {
		t.Fatalf("expected ECR image to be detected")
	}
	if IsAWSECRImage("docker.io/library/ubuntu:22.04") {
		t.Fatalf("expected non-ECR image not to be detected")
	}
}

// assertErr is a sentinel error for tests
var assertErr = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "boom" }
