package bootstrap

import (
	"encoding/json"
	"errors"
	"strings"
)

const machineIdentityPath = "/etc/clo/machine-identity.json"

// MachineIdentity is public provisioning metadata. It deliberately contains
// no credential and no guessed provider server ID (unknown before create).
type MachineIdentity struct {
	SchemaVersion int    `json:"schemaVersion"`
	ProjectID     string `json:"projectID"`
	ProviderName  string `json:"providerName"`
	Attempt       string `json:"attempt"`
}

func (m MachineIdentity) Validate() error {
	if m.SchemaVersion != 1 {
		return errors.New("unsupported CLO machine identity version")
	}
	for _, v := range []string{m.ProjectID, m.ProviderName, m.Attempt} {
		if v == "" || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("incomplete or invalid CLO machine identity")
		}
	}
	return nil
}

func ComposeWithIdentity(input []byte, publicKey, privateCIDR string, identity MachineIdentity) ([]byte, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	return compose(input, publicKey, privateCIDR, append(data, '\n'))
}
