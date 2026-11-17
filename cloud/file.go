package cloud

import (
	"context"
	"errors"
	"golang.org/x/time/rate"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// FileStorage constructs a redacting SDK client for each request, reading the
// mounted Secret afresh. Credentials never become node or registry state.
type FileStorage struct {
	path    string
	factory func(string) (StorageAPI, error)
}

func credentialFile(path string) (string, error) {
	file, e := os.Open(path)
	if e != nil {
		return "", errors.New("CLO credential file unavailable")
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, 16385))
	if e != nil || len(raw) > 16384 {
		return "", errors.New("CLO credential file invalid")
	}
	token := strings.Trim(strings.TrimSpace(string(raw)), "\"'")
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" || strings.IndexFunc(token, unicode.IsSpace) >= 0 || strings.ContainsRune(token, 0) {
		return "", errors.New("CLO credential file invalid")
	}
	return token, nil
}
func NewFileStorage(path string) (*FileStorage, error) {
	transport := newPacedTransport(http.DefaultTransport, rate.NewLimiter(rate.Every(time.Second), 1))
	return newFileStorage(path, func(token string) (StorageAPI, error) {
		return newClient(token, endpoint, &http.Client{Transport: transport, Timeout: 30 * time.Second})
	})
}
func newFileStorage(path string, factory func(string) (StorageAPI, error)) (*FileStorage, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || factory == nil {
		return nil, errors.New("absolute CLO credential file required")
	}
	if _, e := credentialFile(path); e != nil {
		return nil, e
	}
	return &FileStorage{path, factory}, nil
}
func fileCall[T any](f *FileStorage, call func(StorageAPI) (T, error)) (T, error) {
	var zero T
	token, e := credentialFile(f.path)
	if e != nil {
		return zero, e
	}
	api, e := f.factory(token)
	if e != nil {
		return zero, errors.New("CLO client initialization failed")
	}
	return call(api)
}
func fileWrite(f *FileStorage, call func(StorageAPI) error) error {
	_, e := fileCall(f, func(api StorageAPI) (struct{}, error) { return struct{}{}, call(api) })
	return e
}
func (f *FileStorage) GetProject(ctx context.Context, id string) (Project, error) {
	return fileCall(f, func(a StorageAPI) (Project, error) { return a.GetProject(ctx, id) })
}
func (f *FileStorage) GetServer(ctx context.Context, id string) (Server, error) {
	return fileCall(f, func(a StorageAPI) (Server, error) { return a.GetServer(ctx, id) })
}
func (f *FileStorage) ListServers(ctx context.Context, id string) ([]Server, error) {
	return fileCall(f, func(a StorageAPI) ([]Server, error) { return a.ListServers(ctx, id) })
}
func (f *FileStorage) CreateServer(ctx context.Context, id string, r CreateRequest) (string, error) {
	return fileCall(f, func(a StorageAPI) (string, error) { return a.CreateServer(ctx, id, r) })
}
func (f *FileStorage) DeleteServer(ctx context.Context, id string, boot []string) error {
	return fileWrite(f, func(a StorageAPI) error { return a.DeleteServer(ctx, id, boot) })
}
func (f *FileStorage) GetVolume(ctx context.Context, id string) (Volume, error) {
	return fileCall(f, func(a StorageAPI) (Volume, error) { return a.GetVolume(ctx, id) })
}
func (f *FileStorage) GetAddress(ctx context.Context, id string) (Address, error) {
	return fileCall(f, func(a StorageAPI) (Address, error) { return a.GetAddress(ctx, id) })
}
func (f *FileStorage) GetNetworks(ctx context.Context, id string) ([]Network, error) {
	return fileCall(f, func(a StorageAPI) ([]Network, error) { return a.GetNetworks(ctx, id) })
}
func (f *FileStorage) Power(ctx context.Context, id, action string) error {
	return fileWrite(f, func(a StorageAPI) error { return a.Power(ctx, id, action) })
}
func (f *FileStorage) ListVolumes(ctx context.Context, id string) ([]Volume, error) {
	return fileCall(f, func(a StorageAPI) ([]Volume, error) { return a.ListVolumes(ctx, id) })
}
func (f *FileStorage) CreateVolume(ctx context.Context, id string, r CreateVolumeRequest) (string, error) {
	return fileCall(f, func(a StorageAPI) (string, error) { return a.CreateVolume(ctx, id, r) })
}
func (f *FileStorage) AttachVolume(ctx context.Context, id, node string) error {
	return fileWrite(f, func(a StorageAPI) error { return a.AttachVolume(ctx, id, node) })
}
func (f *FileStorage) DetachVolume(ctx context.Context, id string, r DetachOptions) error {
	return fileWrite(f, func(a StorageAPI) error { return a.DetachVolume(ctx, id, r) })
}
func (f *FileStorage) ExtendVolume(ctx context.Context, id string, size int) error {
	return fileWrite(f, func(a StorageAPI) error { return a.ExtendVolume(ctx, id, size) })
}
func (f *FileStorage) DeleteVolume(ctx context.Context, id string) error {
	return fileWrite(f, func(a StorageAPI) error { return a.DeleteVolume(ctx, id) })
}

var _ StorageAPI = (*FileStorage)(nil)
