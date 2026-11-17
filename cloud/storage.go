package cloud

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/clo-ru/cloapi-go-client/v3"
)

var _ StorageAPI = (*Client)(nil)

func volumeFrom(v sdk.VolumeSchema) Volume {
	out := Volume{ID: v.Id, Name: v.Name, Status: v.Status, Bootable: v.Bootable, SizeGB: v.Size}
	if v.AttachedToServer != nil {
		out.AttachedServerID = v.AttachedToServer.Id
		out.Device = v.AttachedToServer.Device
	}
	return out
}

func (c *Client) ListVolumes(ctx context.Context, project string) ([]Volume, error) {
	if strings.TrimSpace(project) == "" {
		return nil, errors.New("volume project is required")
	}
	var out []Volume
	seen := map[string]bool{}
	progress := inventoryProgress{}
	for offset := 0; offset < inventoryPageSize*maxInventoryPages; offset += inventoryPageSize {
		r, err := c.sdk.ProjectVolumesListWithResponse(ctx, project, sdk.WithPage(100, offset))
		if err != nil {
			return nil, c.failure("list volumes", err)
		}
		if r.OK == nil {
			return nil, malformed("list volumes")
		}
		length := 0
		if r.OK.Result != nil {
			length = len(*r.OK.Result)
		}
		done, err := progress.page(r.Body, offset, length)
		if err != nil {
			return nil, err
		}
		if r.OK.Result != nil {
			for _, v := range *r.OK.Result {
				if v.Id == "" {
					return nil, malformed("list volumes")
				}
				if seen[v.Id] {
					return nil, errors.New("CLO volume pagination repeated an ID")
				}
				seen[v.Id] = true
				out = append(out, volumeFrom(v))
			}
		}
		if done {
			return out, nil
		}
	}
	return nil, malformed("inventory page bound exceeded")
}

func (c *Client) CreateVolume(ctx context.Context, project string, p CreateVolumeRequest) (string, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(p.Name) == "" || p.SizeGB <= 0 {
		return "", errors.New("volume project, name and positive size are required")
	}
	no := false
	r, err := c.sdk.VolumeCreateWithResponse(ctx, project, sdk.VolumeCreateJSONRequestBody{
		Name: p.Name, Size: p.SizeGB, Autorename: &no,
	})
	if err != nil {
		return "", c.failure("create volume", err)
	}
	if r.OK == nil || r.OK.Result == nil || r.OK.Result.Id == "" {
		return "", malformed("create volume")
	}
	return r.OK.Result.Id, nil
}

func (c *Client) AttachVolume(ctx context.Context, volume, server string) error {
	if strings.TrimSpace(volume) == "" || strings.TrimSpace(server) == "" {
		return errors.New("volume and target server are required")
	}
	_, err := c.sdk.VolumeAttachWithResponse(ctx, volume, sdk.VolumeAttachJSONRequestBody{ServerId: server})
	if err != nil {
		return c.failure("attach volume", err)
	}
	return nil
}

func (c *Client) DetachVolume(ctx context.Context, volume string, p DetachOptions) error {
	if strings.TrimSpace(volume) == "" {
		return errors.New("volume is required")
	}
	no := false
	_, err := c.sdk.VolumeDetachWithResponse(ctx, volume, sdk.VolumeDetachJSONRequestBody{Force: &p.Hot, ClearFstab: &no})
	if err != nil {
		return c.failure("detach volume", err)
	}
	return nil
}

func (c *Client) ExtendVolume(ctx context.Context, volume string, newSizeGB int) error {
	if strings.TrimSpace(volume) == "" || newSizeGB <= 0 {
		return errors.New("volume and positive size are required")
	}
	no := false
	_, err := c.sdk.VolumeExtendWithResponse(ctx, volume, sdk.VolumeExtendJSONRequestBody{NewSize: newSizeGB, RebootServer: &no})
	if err != nil {
		return c.failure("extend volume", err)
	}
	return nil
}

func (c *Client) DeleteVolume(ctx context.Context, volume string) error {
	if strings.TrimSpace(volume) == "" {
		return errors.New("volume is required")
	}
	no := false
	_, err := c.sdk.VolumeDeleteWithResponse(ctx, volume, sdk.VolumeDeleteJSONRequestBody{Force: &no, ClearFstab: &no})
	if err != nil {
		return c.failure("delete volume", err)
	}
	return nil
}
