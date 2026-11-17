// Package bootstrap composes Rancher cloud-init without replacing its commands.
package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

const networkScriptPath = "/usr/local/sbin/clo-rke2-network"
const networkConfigPath = "/etc/rancher/rke2/config.yaml.d/90-clo-private-ip.yaml"

func Compose(input []byte, publicKey, privateCIDR string) ([]byte, error) {
	return compose(input, publicKey, privateCIDR, nil)
}

func compose(input []byte, publicKey, privateCIDR string, identity []byte) ([]byte, error) {
	text := strings.TrimSpace(string(input))
	if text == "" {
		text = "#cloud-config"
	}
	if strings.TrimSpace(strings.SplitN(text, "\n", 2)[0]) != "#cloud-config" {
		return nil, errors.New("only #cloud-config YAML bootstrap is supported")
	}
	_, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid SSH public key")
	}
	prefix, err := netip.ParsePrefix(privateCIDR)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || !privatePrefix(prefix) {
		return nil, errors.New("expected a canonical private IPv4 project CIDR")
	}
	body := ""
	if parts := strings.SplitN(text, "\n", 2); len(parts) == 2 {
		body = parts[1]
	}
	dec := yaml.NewDecoder(strings.NewReader(body))
	var doc yaml.Node
	if err = dec.Decode(&doc); err != nil && err != io.EOF {
		return nil, fmt.Errorf("invalid cloud-init YAML: %w", err)
	}
	var trailing yaml.Node
	if err = dec.Decode(&trailing); err != io.EOF {
		return nil, errors.New("cloud-init must contain a single YAML document")
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(doc.Content) > 0 {
		if doc.Content[0].Kind != yaml.MappingNode {
			return nil, errors.New("cloud-init root must be a mapping")
		}
		if err = validate(doc.Content[0]); err != nil {
			return nil, err
		}
		m = doc.Content[0]
	}
	if v := member(m, "disable_root"); v != nil && !(v.Kind == yaml.ScalarNode && ((v.Tag == "!!bool" && strings.EqualFold(v.Value, "false")) || (v.Style == 0 && v.Tag == "!!str" && (strings.EqualFold(v.Value, "no") || strings.EqualFold(v.Value, "off"))))) {
		return nil, errors.New("bootstrap disables the required root SSH user")
	}
	files, err := sequence(m, "write_files")
	if err != nil {
		return nil, err
	}
	for _, f := range files.Content {
		if f.Kind != yaml.MappingNode {
			return nil, errors.New("write_files entry must be a mapping")
		}
		if path := member(f, "path"); path != nil && (path.Value == networkScriptPath || path.Value == networkConfigPath || path.Value == machineIdentityPath) {
			return nil, errors.New("bootstrap collides with CLO-owned configuration")
		}
	}
	run, err := sequence(m, "runcmd")
	if err != nil {
		return nil, err
	}
	users, err := sequence(m, "users")
	if err != nil {
		return nil, err
	}
	if len(users.Content) == 0 {
		users.Content = []*yaml.Node{scalar("default")}
	}
	var root *yaml.Node
	for i, u := range users.Content {
		if u.Kind == yaml.ScalarNode && u.Tag == "!!str" && u.Value == "root" {
			u = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{scalar("name"), scalar("root")}, HeadComment: u.HeadComment, LineComment: u.LineComment, FootComment: u.FootComment}
			users.Content[i] = u
		}
		if u.Kind == yaml.MappingNode && member(u, "name") != nil && member(u, "name").Value == "root" {
			if root != nil {
				return nil, errors.New("duplicate root user in bootstrap")
			}
			root = u
		}
	}
	if root == nil {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{scalar("name"), scalar("root"), scalar("lock_passwd"), {Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}}}
		users.Content = append(users.Content, root)
	}
	keys, err := sequence(root, "ssh_authorized_keys")
	if err != nil {
		return nil, err
	}
	found := false
	for _, k := range keys.Content {
		if k.Kind == yaml.ScalarNode && k.Value == publicKey {
			found = true
		}
	}
	if !found {
		keys.Content = append(keys.Content, scalar(publicKey))
	}
	put(root, "ssh_authorized_keys", keys)
	put(m, "users", users)
	put(m, "disable_root", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	files.Content = append(files.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{scalar("path"), scalar(networkScriptPath), scalar("permissions"), scalar("0755"), scalar("owner"), scalar("root:root"), scalar("content"), {Kind: yaml.ScalarNode, Tag: "!!str", Value: networkScript(prefix.String()), Style: yaml.LiteralStyle}}})
	if identity != nil {
		files.Content = append(files.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			scalar("path"), scalar(machineIdentityPath), scalar("permissions"), scalar("0600"),
			scalar("owner"), scalar("root:root"), scalar("content"),
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(identity), Style: yaml.LiteralStyle},
		}})
	}
	put(m, "write_files", files)
	run.Content = append([]*yaml.Node{scalar(networkScriptPath + " || exit $?")}, run.Content...)
	put(m, "runcmd", run)
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append([]byte("#cloud-config\n"), out...), nil
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
func member(m *yaml.Node, name string) *yaml.Node {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == name {
			return m.Content[i+1]
		}
	}
	return nil
}
func put(m *yaml.Node, name string, value *yaml.Node) {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == name {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, scalar(name), value)
}
func sequence(m *yaml.Node, name string) (*yaml.Node, error) {
	v := member(m, name)
	if v == nil || v.Tag == "!!null" {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}, nil
	}
	if v.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("cloud-init %s must be a sequence", name)
	}
	return v, nil
}
func privatePrefix(p netip.Prefix) bool {
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		r := netip.MustParsePrefix(s)
		if r.Contains(p.Addr()) && p.Bits() >= r.Bits() {
			return true
		}
	}
	return false
}
func validate(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		return errors.New("YAML aliases are not supported in machine bootstrap")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return errors.New("cloud-init mapping keys must be strings")
			}
			if seen[k.Value] {
				return fmt.Errorf("duplicate cloud-init key %q", k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := validate(c); err != nil {
			return err
		}
	}
	return nil
}

func networkScript(cidr string) string {
	return fmt.Sprintf(`#!/usr/bin/env python3
import ipaddress, json, os, pathlib, subprocess, tempfile
network = ipaddress.ip_network(%q)
interfaces = json.loads(subprocess.check_output(['ip','-j','-4','address','show','scope','global'], text=True))
addresses = {item['local'] for interface in interfaces for item in interface.get('addr_info', []) if item.get('family') == 'inet' and ipaddress.ip_address(item['local']) in network}
if len(addresses) != 1:
    raise SystemExit('Expected exactly one IPv4 in the CLO project network')
address = next(iter(addresses))
directory = pathlib.Path('/etc/rancher/rke2/config.yaml.d')
directory.mkdir(parents=True, exist_ok=True)
with tempfile.NamedTemporaryFile(mode='w', dir=str(directory), delete=False) as output:
    output.write('node-ip: ' + address + '\n')
    output.flush()
    os.fsync(output.fileno())
os.chmod(output.name, 0o600)
os.replace(output.name, str(directory / '90-clo-private-ip.yaml'))
`, cidr)
}
