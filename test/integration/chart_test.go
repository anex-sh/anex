//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	clusterName = "gpu-provider-it"
	namespace   = "gpu-provider-it"
	releaseName = "gpu-provider-it"

	vkImage = "virtual-kubelet:latest"
	gwImage = "gateway:latest"
)

// helmValues is the values.yaml passed to `helm install -f`. Uses the mock
// cloud provider so no real Vast.AI / RunPod credentials are needed.
const helmValues = `
deployment:
  gateway:
    class: "node-port"
    domain: "127.0.0.1"
    nodePortUDP: 31000

  containers:
    virtualKubelet:
      image:
        tag: "latest"

    gateway:
      image:
        tag: "latest"

appConfig:
  cluster:
    clusterUUID: "minikube"

  cloudProvider:
    active: "mock"

  virtualNode:
    nodeName: "gpu-node"

    labels:
      node-provider: vastai
`

func TestChartInstallsAndStaysHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	for _, tool := range []string{"kind", "helm", "kubectl", "docker"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required tool %q not found on PATH: %v", tool, err)
		}
	}

	for _, img := range []string{vkImage, gwImage} {
		if err := exec.CommandContext(ctx, "docker", "image", "inspect", img).Run(); err != nil {
			t.Fatalf("image %q not found locally; build it first (e.g. `docker build -f deploy/Dockerfile -t %s .`)", img, img)
		}
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	chartPath := filepath.Join(repoRoot, "deploy", "chart")

	t.Cleanup(func() {
		if os.Getenv("KEEP_CLUSTER") == "1" && t.Failed() {
			t.Logf("KEEP_CLUSTER=1 and test failed: leaving cluster %q for inspection", clusterName)
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = runNoFail(cleanupCtx, t, "kind", "delete", "cluster", "--name", clusterName)
	})

	valuesFile := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesFile, []byte(helmValues), 0o600); err != nil {
		t.Fatalf("write values file: %v", err)
	}

	run(ctx, t, "kind", "create", "cluster", "--name", clusterName)
	run(ctx, t, "kind", "load", "docker-image", vkImage, "--name", clusterName)
	run(ctx, t, "kind", "load", "docker-image", gwImage, "--name", clusterName)

	// --set overrides fields not covered by helmValues: image repository (to
	// use the locally-loaded images) and pullPolicy (so kind doesn't try to
	// pull from the remote registry).
	run(ctx, t,
		"helm", "upgrade", "--install", releaseName, chartPath,
		"--namespace", namespace, "--create-namespace",
		"--wait", "--timeout", "5m",
		"-f", valuesFile,
		"--set", "deployment.containers.virtualKubelet.image.repository=virtual-kubelet",
		"--set", "deployment.containers.virtualKubelet.image.pullPolicy=IfNotPresent",
		"--set", "deployment.containers.gateway.image.repository=gateway",
		"--set", "deployment.containers.gateway.image.pullPolicy=IfNotPresent",
	)

	run(ctx, t, "kubectl", "-n", namespace, "wait",
		"--for=condition=Ready", "pod", "--all", "--timeout=300s")

	time.Sleep(180 * time.Second)

	assertNoBadPods(ctx, t)
	t.Log("initial health check passed; waiting 10s for late crashes")
	time.Sleep(10 * time.Second)
	assertNoBadPods(ctx, t)
}

func run(ctx context.Context, t *testing.T, name string, args ...string) {
	t.Helper()
	if err := runCmd(ctx, t, name, args...); err != nil {
		dumpDiagnostics(t)
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
}

func runNoFail(ctx context.Context, t *testing.T, name string, args ...string) error {
	t.Helper()
	return runCmd(ctx, t, name, args...)
}

func runCmd(ctx context.Context, t *testing.T, name string, args ...string) error {
	t.Helper()
	t.Logf("$ %s %s", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = testLogWriter{t}
	cmd.Stderr = testLogWriter{t}
	return cmd.Run()
}

func mustOutput(ctx context.Context, t *testing.T, name string, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = testLogWriter{t}
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return buf.String()
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		w.t.Log(line)
	}
	return len(p), nil
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				RestartCount int    `json:"restartCount"`
				Ready        bool   `json:"ready"`
				State        struct {
					Waiting *struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"waiting,omitempty"`
					Terminated *struct {
						Reason   string `json:"reason"`
						ExitCode int    `json:"exitCode"`
					} `json:"terminated,omitempty"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func assertNoBadPods(ctx context.Context, t *testing.T) {
	t.Helper()
	out := mustOutput(ctx, t, "kubectl", "-n", namespace, "get", "pods", "-o", "json")
	var list podList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("parse pod list: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("no pods found in namespace %q", namespace)
	}
	failed := false
	for _, p := range list.Items {
		for _, c := range p.Status.ContainerStatuses {
			if c.RestartCount > 0 {
				t.Errorf("container %s/%s restarted %d times", p.Metadata.Name, c.Name, c.RestartCount)
				failed = true
			}
			if w := c.State.Waiting; w != nil {
				switch w.Reason {
				case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull",
					"CreateContainerError", "RunContainerError", "CreateContainerConfigError":
					t.Errorf("container %s/%s waiting: %s — %s", p.Metadata.Name, c.Name, w.Reason, w.Message)
					failed = true
				}
			}
			if !c.Ready {
				t.Errorf("container %s/%s is not Ready", p.Metadata.Name, c.Name)
				failed = true
			}
		}
	}
	if failed {
		dumpDiagnostics(t)
		t.FailNow()
	}
}

func dumpDiagnostics(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	for _, args := range [][]string{
		{"kubectl", "-n", namespace, "get", "pods", "-o", "wide"},
		{"kubectl", "-n", namespace, "describe", "pods"},
		{"kubectl", "-n", namespace, "logs", "--all-containers=true", "--tail=200", "-l", fmt.Sprintf("app=%s", releaseName)},
		{"kubectl", "-n", namespace, "logs", "--all-containers=true", "--previous", "--tail=200", "-l", fmt.Sprintf("app=%s", releaseName)},
	} {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		_ = cmd.Run()
		t.Logf("--- %s ---\n%s", strings.Join(args, " "), buf.String())
	}
}
