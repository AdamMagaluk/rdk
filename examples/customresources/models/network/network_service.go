// Package network implements an acme:service:summation, a demo service which sums (or subtracts) a given list of numbers.
package network

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/theojulienne/go-wireless"
	"go.uber.org/zap"

	"go.viam.com/rdk/config"
	"go.viam.com/rdk/examples/customresources/apis/networkapi"
	v1 "go.viam.com/rdk/examples/customresources/apis/proto/api/service/network/v1"
	"go.viam.com/rdk/registry"
	"go.viam.com/rdk/resource"
)

var Model = resource.NewModel(
	resource.Namespace("acme"),
	resource.ModelFamilyName("demo"),
	resource.ModelName("mynetwork"),
)

func init() {
	registry.RegisterService(networkapi.Subtype, Model, registry.Service{
		Constructor: newNetworkService,
	})
}

type networkService struct {
	mu           sync.Mutex
	allowUpdates bool
}

func newNetworkService(ctx context.Context, deps registry.Dependencies, cfg config.Service, logger *zap.SugaredLogger) (interface{}, error) {
	golog.Global().Warn("newNetworkService")
	return &networkService{allowUpdates: cfg.Attributes.Bool("allow_updates", false)}, nil
}

func (m *networkService) GetInterface(ctx context.Context, name string) (*v1.Interface, error) {
	golog.Global().Debugf("Impl: GetInterface %s", name)
	if name == "" {
		return nil, errors.New("must provide interface name")
	}

	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}

	out, err := interfaceToProto(iface)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func (m *networkService) ListInterfaces(ctx context.Context) ([]*v1.Interface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	out := make([]*v1.Interface, 0, len(all))
	for _, iface := range all {
		ifaceProto, err := interfaceToProto(&iface)
		if err != nil {
			return nil, err
		}
		out = append(out, ifaceProto)
	}

	return out, nil
}

func (m *networkService) WifiScan(ctx context.Context, interfaceName string, duration time.Duration) ([]*v1.WifiNetwork, error) {
	wc, err := wireless.NewClient(interfaceName)
	if err != nil {
		return nil, err
	}
	defer wc.Close()

	wc.ScanTimeout = duration
	aps, err := wc.Scan()
	if err != nil {
		return nil, err
	}

	out := make([]*v1.WifiNetwork, 0, len(aps))
	for _, ap := range aps {
		out = append(out, apToProto(ap))
	}

	return out, nil
}

func (m *networkService) WifiConnect(ctx context.Context, opts networkapi.WifiConnectOptions) (*v1.WifiConnectResponse, error) {
	return nil, errors.New("Unimplemented")
}

func (m *networkService) WifiConnectConfirm(ctx context.Context, token string) error {
	return errors.New("Unimplemented")
}

func apToProto(ap wireless.AP) *v1.WifiNetwork {
	return &v1.WifiNetwork{
		Id:        int64(ap.ID),
		Ssid:      ap.SSID,
		Bssid:     ap.BSSID,
		Essid:     ap.ESSID,
		Known:     false, // todo
		Rssi:      int64(ap.RSSI),
		Frequency: int64(ap.Frequency),
		Signal:    int64(ap.Signal),
		Flags:     ap.Flags,
	}
}

func interfaceToProto(iface *net.Interface) (*v1.Interface, error) {
	out := &v1.Interface{
		Name:            iface.Name,
		Mtu:             int64(iface.MTU),
		HardwareAddress: iface.HardwareAddr.String(),
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	out.Addresses = make([]*v1.Interface_Address, 0, len(addrs))
	for _, addr := range addrs {
		out.Addresses = append(out.Addresses, &v1.Interface_Address{
			Network: addr.Network(),
			Address: addr.String(),
		})
	}

	return out, nil
}

func (m *networkService) Reconfigure(ctx context.Context, cfg config.Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allowUpdates = cfg.Attributes.Bool("allow_updates", false)
	return nil
}
