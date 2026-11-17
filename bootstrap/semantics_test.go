package bootstrap

import (
	"os/exec"
	"strings"
	"testing"
)

func TestComposePreservesOriginalRuncmdExitSemantics(t *testing.T) {
	output, err := Compose([]byte("#cloud-config\nruncmd:\n - grep -q marker /dev/null\n - echo registration-ran\n"), key(t), "10.174.80.0/21")
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, entry := range config(t, output)["runcmd"].([]any) {
		commands = append(commands, entry.(string))
	}
	script := strings.ReplaceAll(strings.Join(commands, "\n"), networkScriptPath, "true")
	result, _ := exec.Command("sh", "-c", script).CombinedOutput()
	if !strings.Contains(string(result), "registration-ran") {
		t.Fatal("successful network step changed the original shell failure behavior")
	}
}

func TestComposePreservesCloudInitScalarSemantics(t *testing.T) {
	input := []byte("#cloud-config\nwrite_files:\n - path: /etc/example\n   content: payload\n   append: no\ncustom:\n plain_yes: yes\n quoted_yes: 'yes'\n clock: 1:20\n octal: 0640\n quoted_octal: '0640'\n sequence: [off, 'off']\n")
	output, err := Compose(input, key(t), "10.174.80.0/21")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-c", `import sys,yaml
d=yaml.safe_load(sys.stdin.read())
assert d['write_files'][0]['append'] is False,repr(d['write_files'][0]['append'])
c=d['custom']
assert c['plain_yes'] is True
assert c['quoted_yes']=='yes'
assert c['clock']==80 and isinstance(c['clock'],int)
assert c['octal']==416 and isinstance(c['octal'],int)
assert c['quoted_octal']=='0640'
assert c['sequence']==[False,'off']
`)
	cmd.Stdin = strings.NewReader(string(output))
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cloud-init YAML semantics changed (test requires scripts/requirements-test.txt): %v %s", err, result)
	}
}
