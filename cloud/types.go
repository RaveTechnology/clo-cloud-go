// Package cloud adapts the generated CLO SDK to the operations used by drivers.
package cloud

import "context"

type Project struct{ ID, Status string }
type Server struct {
	ID, Name, ProjectID, Status string
	SwitchStatus                string
	AddressIDs, DiskIDs         []string
}
type Volume struct {
	ID, Name, Status, AttachedServerID, Device string
	Bootable                                   bool
	SizeGB                                     int
}
type Address struct {
	ID, IP, Status, Type, AttachedServerID string
	External                               bool
}
type Network struct {
	ID, CIDR, Gateway, Status string
	Private                   bool
}
type CreateRequest struct {
	Name, ImageID, PublicIPID, UserDataBase64 string
	VCPUs, RAMGB, BootGB                      int
}

type API interface {
	GetProject(context.Context, string) (Project, error)
	GetServer(context.Context, string) (Server, error)
	ListServers(context.Context, string) ([]Server, error)
	CreateServer(context.Context, string, CreateRequest) (string, error)
	DeleteServer(context.Context, string, []string) error
	GetVolume(context.Context, string) (Volume, error)
	GetAddress(context.Context, string) (Address, error)
	GetNetworks(context.Context, string) ([]Network, error)
	Power(context.Context, string, string) error
}

// StorageAPI adds volume lifecycle operations without enlarging the machine
// driver's dependency on API. Ownership and mutation permissions belong to the
// CSI coordinator; this adapter never retries an ambiguous write.
type StorageAPI interface {
	API
	ListVolumes(context.Context, string) ([]Volume, error)
	CreateVolume(context.Context, string, CreateVolumeRequest) (string, error)
	AttachVolume(context.Context, string, string) error
	DetachVolume(context.Context, string, DetachOptions) error
	ExtendVolume(context.Context, string, int) error
	DeleteVolume(context.Context, string) error
}

type CreateVolumeRequest struct {
	Name   string
	SizeGB int
}

type DetachOptions struct{ Hot bool }
