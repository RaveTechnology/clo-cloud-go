package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/clo-ru/cloapi-go-client/v3"
)

const endpoint = "https://api.clo.ru"

type Client struct {
	sdk   *sdk.ClientWithResponses
	token string
}
type APIError struct {
	Status  int
	Message string
	cause   error
}

func (e *APIError) Error() string { return fmt.Sprintf("CLO API (%d): %s", e.Status, e.Message) }
func (e *APIError) Unwrap() error { return e.cause }
func IsNotFound(err error) bool   { var e *APIError; return errors.As(err, &e) && e.Status == 404 }

func New(token string) (*Client, error) {
	return newClient(token, endpoint, &http.Client{Transport: sharedAPITransport, Timeout: 30 * time.Second})
}
func newClient(token, base string, h *http.Client) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("CLO API token is required")
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("CLO API redirects are refused") }
	s, err := sdk.New(token, sdk.WithBaseURL(base), sdk.WithHTTPClient(h), sdk.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		return nil, errors.New("cannot initialize CLO API client")
	}
	return &Client{sdk: s, token: token}, nil
}
func (c *Client) failure(op string, err error) error {
	code := 0
	if a, ok := sdk.AsApiError(err); ok {
		code = a.Code
	}
	var cause error
	if errors.Is(err, context.Canceled) {
		cause = context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		cause = context.DeadlineExceeded
	}
	return &APIError{Status: code, Message: op + ": " + strings.ReplaceAll(err.Error(), c.token, "[redacted]"), cause: cause}
}
func malformed(op string) error {
	return &APIError{Message: op + ": missing result in successful response"}
}
func serverFrom(s sdk.ServerSchema) Server {
	r := Server{ID: s.Id, Name: s.Name, ProjectID: s.Project, Status: s.Status, SwitchStatus: s.SwitchStatus}
	if s.Addresses != nil {
		r.AddressIDs = append([]string(nil), (*s.Addresses)...)
	}
	if s.DiskData != nil {
		for _, d := range *s.DiskData {
			r.DiskIDs = append(r.DiskIDs, d.Id)
		}
	}
	return r
}

func (c *Client) GetProject(ctx context.Context, id string) (Project, error) {
	r, e := c.sdk.ProjectDetailWithResponse(ctx, id)
	if e != nil {
		return Project{}, c.failure("get project", e)
	}
	if r.OK == nil || r.OK.Result == nil {
		return Project{}, malformed("get project")
	}
	return Project{ID: r.OK.Result.Id, Status: r.OK.Result.Status}, nil
}
func (c *Client) GetServer(ctx context.Context, id string) (Server, error) {
	r, e := c.sdk.ServerDetailWithResponse(ctx, id)
	if e != nil {
		return Server{}, c.failure("get server", e)
	}
	if r.OK == nil || r.OK.Result == nil {
		return Server{}, malformed("get server")
	}
	return serverFrom(*r.OK.Result), nil
}
func (c *Client) ListServers(ctx context.Context, project string) ([]Server, error) {
	if strings.TrimSpace(project) == "" {
		return nil, errors.New("server project is required")
	}
	var out []Server
	seen := map[string]bool{}
	progress := inventoryProgress{}
	for offset := 0; offset < inventoryPageSize*maxInventoryPages; offset += inventoryPageSize {
		r, e := c.sdk.ProjectServerListWithResponse(ctx, project, sdk.WithPage(100, offset))
		if e != nil {
			return nil, c.failure("list servers", e)
		}
		if r.OK == nil {
			return nil, malformed("list servers")
		}
		length := 0
		if r.OK.Result != nil {
			length = len(*r.OK.Result)
		}
		done, e := progress.page(r.Body, offset, length)
		if e != nil {
			return nil, e
		}
		if r.OK.Result != nil {
			for _, s := range *r.OK.Result {
				if s.Id == "" {
					return nil, malformed("list servers")
				}
				if seen[s.Id] {
					return nil, errors.New("CLO server pagination repeated an ID")
				}
				seen[s.Id] = true
				out = append(out, serverFrom(s))
			}
		}
		if done {
			return out, nil
		}
	}
	return nil, malformed("inventory page bound exceeded")
}
func (c *Client) CreateServer(ctx context.Context, project string, p CreateRequest) (string, error) {
	// Marshal into generated anonymous structs so only our explicit fields are sent.
	addresses := []map[string]any{}
	if p.PublicIPID != "" {
		addresses = append(addresses, map[string]any{"address_id": p.PublicIPID, "external": true, "version": 4})
	}
	b := map[string]any{"name": p.Name, "image": p.ImageID, "flavor": map[string]any{"vcpus": p.VCPUs, "ram": p.RAMGB, "cpu_type": "SHARED"},
		"storages": []map[string]any{{"size": p.BootGB, "bootable": true, "storage_type": "volume"}}, "addresses": addresses, "user_data": p.UserDataBase64}
	raw, e := json.Marshal(b)
	if e != nil {
		return "", e
	}
	var body sdk.ServerCreateJSONRequestBody
	if e = json.Unmarshal(raw, &body); e != nil {
		return "", e
	}
	r, e := c.sdk.ServerCreateWithResponse(ctx, project, body)
	if e != nil {
		return "", c.failure("create server", e)
	}
	if r.OK == nil || r.OK.Result == nil || r.OK.Result.Id == "" {
		return "", malformed("create server")
	}
	return r.OK.Result.Id, nil
}
func (c *Client) DeleteServer(ctx context.Context, id string, boots []string) error {
	if len(boots) == 0 {
		return errors.New("refusing server deletion without explicit boot volume ownership")
	}
	for _, id := range boots {
		if id == "" || id == "all" {
			return errors.New("invalid boot volume ownership")
		}
	}
	raw, e := json.Marshal(map[string]any{"delete_volumes": boots, "delete_addresses": []string{}, "clear_fstab": false})
	if e != nil {
		return e
	}
	var body sdk.ServerDeleteJSONRequestBody
	if e = json.Unmarshal(raw, &body); e != nil {
		return e
	}
	_, e = c.sdk.ServerDeleteWithResponse(ctx, id, body)
	if e != nil {
		return c.failure("delete server", e)
	}
	return nil
}
func (c *Client) GetVolume(ctx context.Context, id string) (Volume, error) {
	r, e := c.sdk.VolumeDetailWithResponse(ctx, id)
	if e != nil {
		return Volume{}, c.failure("get volume", e)
	}
	if r.OK == nil || r.OK.Result == nil {
		return Volume{}, malformed("get volume")
	}
	return volumeFrom(*r.OK.Result), nil
}
func (c *Client) GetAddress(ctx context.Context, id string) (Address, error) {
	r, e := c.sdk.AddressDetailWithResponse(ctx, id)
	if e != nil {
		return Address{}, c.failure("get address", e)
	}
	if r.OK == nil || r.OK.Result == nil {
		return Address{}, malformed("get address")
	}
	a := r.OK.Result
	out := Address{ID: a.Id, Status: a.Status, Type: a.Type, External: a.External}
	if a.Address != nil {
		out.IP = *a.Address
	}
	if a.AttachedTo != nil {
		out.AttachedServerID = a.AttachedTo.Id
	}
	return out, nil
}
func (c *Client) GetNetworks(ctx context.Context, project string) ([]Network, error) {
	r, e := c.sdk.NetworksListWithResponse(ctx, project)
	if e != nil {
		return nil, c.failure("get networks", e)
	}
	if r.OK == nil {
		return nil, malformed("get networks")
	}
	var out []Network
	if r.OK.Result != nil {
		for _, n := range *r.OK.Result {
			out = append(out, Network{ID: n.Id, CIDR: n.SubnetAddress, Gateway: n.Gateway, Status: n.Status, Private: n.IsPrivate})
		}
	}
	return out, nil
}
func (c *Client) Power(ctx context.Context, id, action string) error {
	var e error
	switch action {
	case "start":
		_, e = c.sdk.ServerStartWithResponse(ctx, id)
	case "stop":
		_, e = c.sdk.ServerStopWithResponse(ctx, id)
	case "reboot":
		_, e = c.sdk.ServerRebootWithResponse(ctx, id)
	default:
		return errors.New("unsupported CLO power action")
	}
	if e != nil {
		return c.failure(action+" server", e)
	}
	return nil
}
