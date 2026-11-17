package bootstrap

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func key(t *testing.T) string {
	t.Helper()
	k, e := ssh.NewPublicKey(ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public())
	if e != nil {
		t.Fatal(e)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}
func config(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if e := yaml.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestComposePreservesRancherBootstrapAndAddsKey(t *testing.T) {
	input := []byte("#cloud-config\npackage_update: false\nwrite_files:\n  - path: /etc/example\n    permissions: '0640'\n    content: |\n      first\n      second\nusers:\n  - default\n  - name: root\n    ssh_authorized_keys:\n      - old-key\nruncmd:\n  - [sh, -c, 'echo register']\n  - systemctl start rancher-system-agent\n")
	b, e := Compose(input, key(t), "10.174.80.0/21")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(b), "#cloud-config\n") {
		t.Fatal("cloud-init marker missing")
	}
	m := config(t, b)
	files := m["write_files"].([]any)
	first := files[0].(map[string]any)
	if first["content"] != "first\nsecond\n" || first["permissions"] != "0640" {
		t.Fatalf("multiline file corrupted %#v", first)
	}
	if m["package_update"] != false {
		t.Fatal("unrelated option changed")
	}
	users := m["users"].([]any)
	if users[0] != "default" {
		t.Fatal("default user lost")
	}
	root := users[1].(map[string]any)
	keys := root["ssh_authorized_keys"].([]any)
	if len(keys) != 2 || keys[0] != "old-key" || keys[1] != key(t) {
		t.Errorf("keys corrupted %#v", keys)
	}
	run := m["runcmd"].([]any)
	if len(run) != 3 || run[0] != "/usr/local/sbin/clo-rke2-network || exit $?" {
		t.Fatalf("networking must precede registration %#v", run)
	}
	register := run[1].([]any)
	if len(register) != 3 || register[2] != "echo register" || run[2] != "systemctl start rancher-system-agent" {
		t.Fatalf("registration changed %#v", run)
	}
}

func TestComposeRejectsUnsupportedOrAmbiguousDocuments(t *testing.T) {
	for _, input := range []string{
		"#!/bin/sh\necho hi\n", "#cloud-config\nfoo: [\n", "#cloud-config\nruncmd: []\nruncmd: []\n",
		"#cloud-config\nwrite_files:\n - path: /usr/local/sbin/clo-rke2-network\n   content: old\n",
		"#cloud-config\nwrite_files:\n - path: /etc/rancher/rke2/config.yaml.d/90-clo-private-ip.yaml\n   content: old\n",
		"#cloud-config\nruncmd: echo register\n", "#cloud-config\nusers: root\n",
		"#cloud-config\nusers: [root, {name: root}]\n", "#cloud-config\ndisable_root: true\n",
		"#cloud-config\nfoo: &a {bar: baz}\nother: *a\n", "#cloud-config\n---\nfoo: bar\n---\nother: doc\n",
	} {
		t.Run(input, func(t *testing.T) {
			if _, e := Compose([]byte(input), key(t), "10.174.80.0/21"); e == nil {
				t.Fatal("unsafe document accepted")
			}
		})
	}
}

func TestComposeRejectsInvalidKeyAndCIDR(t *testing.T) {
	for _, cidr := range []string{"garbage", "::/0", "0.0.0.0/0", "10.174.80.1/21"} {
		if _, e := Compose([]byte("#cloud-config\n"), key(t), cidr); e == nil {
			t.Errorf("invalid private CIDR accepted %q", cidr)
		}
	}
	if _, e := Compose([]byte("#cloud-config\n"), "not-a-public-key", "10.174.80.0/21"); e == nil {
		t.Error("invalid key accepted")
	}
}

func TestGuestNetworkScriptSelectsPrivateAddressAndRejectsAmbiguity(t *testing.T) {
	b, e := Compose([]byte("#cloud-config\nruncmd: [echo-register]\n"), key(t), "10.174.80.0/21")
	if e != nil {
		t.Fatal(e)
	}
	var script string
	for _, f := range config(t, b)["write_files"].([]any) {
		v := f.(map[string]any)
		if v["path"] == "/usr/local/sbin/clo-rke2-network" {
			script = v["content"].(string)
		}
	}
	if script == "" {
		t.Fatal("no networking script")
	}
	for _, tc := range []struct {
		name, addresses string
		ok              bool
	}{
		{"one-private", `[{"addr_info":[{"family":"inet","local":"188.120.250.144"}]},{"addr_info":[{"family":"inet","local":"10.174.80.3"}]}]`, true},
		{"no-private", `[{"addr_info":[{"family":"inet","local":"188.120.250.144"}]}]`, false},
		{"two-private", `[{"addr_info":[{"family":"inet","local":"10.174.80.3"},{"family":"inet","local":"10.174.80.4"}]}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, _ := json.Marshal(script)
			addresses, _ := json.Marshal(tc.addresses)
			harness := fmt.Sprintf(`import json,pathlib,subprocess,tempfile
from unittest.mock import patch
source=%s
addresses=%s
with tempfile.TemporaryDirectory() as root:
 def path(p): return pathlib.PosixPath(root + str(p))
 with patch('subprocess.check_output',return_value=addresses), patch('pathlib.Path',side_effect=path):
  exec(source,{'__name__':'__main__'})
 print(pathlib.PosixPath(root+'/etc/rancher/rke2/config.yaml.d/90-clo-private-ip.yaml').read_text())
`, src, addresses)
			out, e := exec.Command("python3", "-c", harness).CombinedOutput()
			if tc.ok {
				if e != nil || string(out) != "node-ip: 10.174.80.3\n\n" {
					t.Fatalf("script failed %v: %s", e, out)
				}
			} else if e == nil {
				t.Fatalf("ambiguous address accepted: %s", out)
			}
		})
	}
}

func TestNetworkFailureStopsRegistration(t *testing.T) {
	b, e := Compose([]byte("#cloud-config\nruncmd: [echo-register]\n"), key(t), "10.174.80.0/21")
	if e != nil {
		t.Fatal(e)
	}
	run := config(t, b)["runcmd"].([]any)
	marker := filepath.Join(t.TempDir(), "registered")
	first := strings.ReplaceAll(run[0].(string), "/usr/local/sbin/clo-rke2-network", "false")
	_ = exec.Command("sh", "-c", first+"\ntouch "+marker).Run()
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("registration ran after network setup failed")
	}
}
