package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComposeMachineIdentity(t *testing.T) {
	identity := MachineIdentity{SchemaVersion: 1, ProjectID: "project-id", ProviderName: "clo-node-attempt", Attempt: "attempt"}
	out, err := ComposeWithIdentity([]byte("#cloud-config\nwrite_files:\n - path: /etc/existing\n   content: |\n     keep\n     newlines\nruncmd:\n - echo register\n"), key(t), "10.174.80.0/21", identity)
	if err != nil {
		t.Fatal(err)
	}
	m := config(t, out)
	found := 0
	for _, item := range m["write_files"].([]any) {
		f := item.(map[string]any)
		if f["path"] == "/etc/existing" && f["content"] != "keep\nnewlines\n" {
			t.Fatal("existing file changed")
		}
		if f["path"] != "/etc/clo/machine-identity.json" {
			continue
		}
		found++
		if f["permissions"] != "0600" || f["owner"] != "root:root" {
			t.Fatalf("identity permissions %#v", f)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(f["content"].(string)), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 || got["schemaVersion"] != float64(1) || got["projectID"] != "project-id" || got["providerName"] != "clo-node-attempt" || got["attempt"] != "attempt" {
			t.Fatalf("identity=%#v", got)
		}
	}
	if found != 1 {
		t.Fatalf("identity files=%d", found)
	}
	run := m["runcmd"].([]any)
	if len(run) != 2 || run[1] != "echo register" {
		t.Fatalf("registration changed: %#v", run)
	}
	legacy, err := Compose([]byte("#cloud-config"), key(t), "10.174.80.0/21")
	if err != nil || strings.Contains(string(legacy), "machine-identity.json") {
		t.Fatalf("legacy compose changed: %v", err)
	}
}

func TestComposeRejectsIdentityCollision(t *testing.T) {
	identity := MachineIdentity{SchemaVersion: 1, ProjectID: "p", ProviderName: "n", Attempt: "a"}
	input := []byte("#cloud-config\nwrite_files:\n - path: /etc/clo/machine-identity.json\n   content: impostor\n")
	if _, err := ComposeWithIdentity(input, key(t), "10.174.80.0/21", identity); err == nil {
		t.Fatal("identity file overwritten")
	}
	if _, err := Compose(input, key(t), "10.174.80.0/21"); err == nil {
		t.Fatal("preflight accepted reserved identity path")
	}
	for _, invalid := range []MachineIdentity{
		{}, {SchemaVersion: 2, ProjectID: "p", ProviderName: "n", Attempt: "a"},
		{SchemaVersion: 1, ProjectID: "", ProviderName: "n", Attempt: "a"},
		{SchemaVersion: 1, ProjectID: "p", ProviderName: " ", Attempt: "a"},
		{SchemaVersion: 1, ProjectID: "p", ProviderName: "n", Attempt: ""},
	} {
		if _, err := ComposeWithIdentity([]byte("#cloud-config"), key(t), "10.174.80.0/21", invalid); err == nil {
			t.Fatalf("invalid identity accepted: %+v", invalid)
		}
	}
}
