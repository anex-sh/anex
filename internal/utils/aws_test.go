package utils

import (
	"context"
	"testing"
)

func TestGetAWSECRLoginBuildsLoginString(t *testing.T) {
	orig := getECRCredentialsFunc
	defer func() { getECRCredentialsFunc = orig }()
	getECRCredentialsFunc = func(ctx context.Context, accountId, awsRegion string) (map[string]string, error) {
		if accountId != "123456789012" || awsRegion != "eu-central-1" {
			t.Fatalf("unexpected inputs: %s %s", accountId, awsRegion)
		}
		return map[string]string{
			"username": "AWS",
			"password": "secret",
			"registry": "https://123456789012.dkr.ecr.eu-central-1.amazonaws.com",
		}, nil
	}

	img := "123456789012.dkr.ecr.eu-central-1.amazonaws.com/repo:tag"
	out := GetAWSECRLogin(context.Background(), img)
	if out == "" {
		t.Fatalf("expected non-empty login string")
	}
	want := "-u AWS -p secret https://123456789012.dkr.ecr.eu-central-1.amazonaws.com"
	if out != want {
		t.Fatalf("unexpected output: %q want %q", out, want)
	}
}

func TestGetAWSECRLoginOnErrorReturnsEmpty(t *testing.T) {
	orig := getECRCredentialsFunc
	defer func() { getECRCredentialsFunc = orig }()
	getECRCredentialsFunc = func(ctx context.Context, accountId, awsRegion string) (map[string]string, error) {
		return nil, assertErr
	}
	img := "123456789012.dkr.ecr.eu-central-1.amazonaws.com/repo:tag"
	if out := GetAWSECRLogin(context.Background(), img); out != "" {
		t.Fatalf("expected empty string on error, got %q", out)
	}
}

// assertErr is a sentinel error for tests
var assertErr = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "boom" }
