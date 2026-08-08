package vastai

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateOnStartScriptRendersParams(t *testing.T) {
	params := OnStartTemplateParams{
		Workdir:      "/work",
		Command:      "python app.py",
		AgentURL:     "https://agent/bin",
		WireproxyURL: "https://wireproxy/bin",
		PromtailURL:  "https://promtail/bin",
	}
	out := GenerateOnStartScript(params)
	checks := []string{
		"cd /work",
		"curl https://wireproxy/bin -o /usr/bin/wireproxy",
		"curl https://promtail/bin -o /usr/bin/promtail",
		"curl https://agent/bin -o /usr/bin/container_agent",
		"python app.py",
	}
	for _, sub := range checks {
		if !strings.Contains(out, sub) {
			t.Fatalf("script missing fragment %q. Output:\n%s", sub, out)
		}
	}
}

func TestGenerateOnStartScriptIsValidShell(t *testing.T) {
	script := GenerateOnStartScript(OnStartTemplateParams{Command: "true"})
	path := filepath.Join(t.TempDir(), "onstart.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("rendered script is not valid bash: %v\n%s\nScript:\n%s", err, out, script)
	}
}

// The bootstrap script runs under `set -e`, so anything that fails in the SSH
// preparation section silently kills the rest of the boot: no agent, no
// tunnels, no provider environment. Vast.ai images vary in what SSH material
// they ship, so every shape has to survive.
func TestOnStartScriptToleratesMissingSSHKeyMaterial(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name:  "no .ssh directory at all",
			setup: func(t *testing.T, root string) {},
		},
		{
			name: "empty .ssh directory",
			setup: func(t *testing.T, root string) {
				mkdirAll(t, filepath.Join(root, ".ssh"))
			},
		},
		{
			name: "authorized_keys present",
			setup: func(t *testing.T, root string) {
				dir := filepath.Join(root, ".ssh")
				mkdirAll(t, dir)
				if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte("ssh-ed25519 AAAA test\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	block := sshPreparationBlock(t, GenerateOnStartScript(OnStartTemplateParams{}))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")
			mkdirAll(t, root)
			tc.setup(t, root)

			// The block is written for a container running as root. Retarget it
			// at a throwaway directory owned by whoever runs the test so the
			// chown/chmod calls are permitted; the control flow is unchanged.
			runnable := strings.ReplaceAll(block, "/root", root)
			runnable = strings.ReplaceAll(runnable, "root:root", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()))
			runnable = "set -euo pipefail\n" + runnable + "\necho BOOTSTRAP_CONTINUED\n"

			path := filepath.Join(t.TempDir(), "ssh-prep.sh")
			if err := os.WriteFile(path, []byte(runnable), 0o600); err != nil {
				t.Fatal(err)
			}

			out, err := exec.Command("bash", path).CombinedOutput()
			if err != nil {
				t.Fatalf("SSH preparation aborted the bootstrap: %v\n%s\nScript:\n%s", err, out, runnable)
			}
			if !strings.Contains(string(out), "BOOTSTRAP_CONTINUED") {
				t.Fatalf("bootstrap did not reach the next stage. Output:\n%s\nScript:\n%s", out, runnable)
			}
		})
	}
}

// sshPreparationBlock slices the SSH ownership/permission section out of a
// rendered bootstrap script: everything between the tmux marker and the curl
// helper that follows it.
func sshPreparationBlock(t *testing.T, script string) string {
	t.Helper()
	const (
		start = "touch ~/.no_auto_tmux"
		end   = "ensure_curl()"
	)
	from := strings.Index(script, start)
	to := strings.Index(script, end)
	if from < 0 || to < from {
		t.Fatalf("could not locate the SSH preparation block in:\n%s", script)
	}
	return script[from+len(start) : to]
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
