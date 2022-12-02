// Package networkapi defines a simple number summing service API for demonstration purposes.
package networkapi

import (
	"context"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	pb "go.viam.com/rdk/examples/customresources/apis/proto/api/service/network/v1"
	"go.viam.com/utils/rpc"
	"google.golang.org/protobuf/types/known/durationpb"

	"go.viam.com/rdk/registry"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/subtype"
	"go.viam.com/rdk/utils"
	goutils "go.viam.com/utils"
)

var Subtype = resource.NewSubtype(
	resource.Namespace("acme"),
	resource.ResourceTypeService,
	resource.SubtypeName("network"),
)

// Named is a helper for getting the named Summation's typed resource name.
func Named(name string) resource.Name {
	return resource.NameFromSubtype(Subtype, name)
}

func init() {
	registry.RegisterResourceSubtype(Subtype, registry.ResourceSubtype{
		Reconfigurable: wrapWithReconfigurable,
		RegisterRPCService: func(ctx context.Context, rpcServer rpc.Server, subtypeSvc subtype.Service) error {
			return rpcServer.RegisterServiceServer(
				ctx,
				&pb.NetworkService_ServiceDesc,
				NewServer(subtypeSvc),
				pb.RegisterNetworkServiceHandlerFromEndpoint,
			)
		},
		RPCServiceDesc: &pb.NetworkService_ServiceDesc,
		RPCClient: func(ctx context.Context, conn rpc.ClientConn, name string, logger golog.Logger) interface{} {
			return newClientFromConn(conn, name, logger)
		},
	})
}

type WifiConnectOptions struct {
	Interface       string
	ConnectDuration time.Duration
	SSID            string
	PSK             *string
}

// Summation defines the Go interface for the service (should match the protobuf methods.)
type Network interface {
	GetInterface(ctx context.Context, interfaceName string) (*pb.Interface, error)
	ListInterfaces(ctx context.Context) ([]*pb.Interface, error)
	WifiScan(ctx context.Context, interfaceName string, duration time.Duration) ([]*pb.WifiNetwork, error)
	WifiConnect(ctx context.Context, opts WifiConnectOptions) (*pb.WifiConnectResponse, error)
	WifiConnectConfirm(ctx context.Context, token string) error
}

func wrapWithReconfigurable(r interface{}, name resource.Name) (resource.Reconfigurable, error) {
	mc, ok := r.(Network)
	if !ok {
		return nil, utils.NewUnimplementedInterfaceError((Network)(nil), r)
	}
	if reconfigurable, ok := mc.(*reconfigurableNetwork); ok {
		return reconfigurable, nil
	}
	return &reconfigurableNetwork{actual: mc, name: name}, nil
}

var (
	_ = Network(&reconfigurableNetwork{})
	_ = resource.Reconfigurable(&reconfigurableNetwork{})
)

type reconfigurableNetwork struct {
	mu     sync.RWMutex
	name   resource.Name
	actual Network
}

func (g *reconfigurableNetwork) Name() resource.Name {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.name
}

func (g *reconfigurableNetwork) ProxyFor() interface{} {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actual
}

func (g *reconfigurableNetwork) Reconfigure(ctx context.Context, newNetwork resource.Reconfigurable) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	actual, ok := newNetwork.(*reconfigurableNetwork)
	if !ok {
		return utils.NewUnexpectedTypeError(g, newNetwork)
	}
	if err := goutils.TryClose(ctx, g.actual); err != nil {
		golog.Global().Errorw("error closing old", "error", err)
	}
	g.actual = actual.actual
	return nil
}

func (g *reconfigurableNetwork) GetInterface(ctx context.Context, interfaceName string) (*pb.Interface, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actual.GetInterface(ctx, interfaceName)
}

func (g *reconfigurableNetwork) ListInterfaces(ctx context.Context) ([]*pb.Interface, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actual.ListInterfaces(ctx)
}

func (g *reconfigurableNetwork) WifiScan(ctx context.Context, interfaceName string, duration time.Duration) ([]*pb.WifiNetwork, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actual.WifiScan(ctx, interfaceName, duration)
}

func (g *reconfigurableNetwork) WifiConnect(ctx context.Context, opts WifiConnectOptions) (*pb.WifiConnectResponse, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actual.WifiConnect(ctx, opts)
}

func (g *reconfigurableNetwork) WifiConnectConfirm(ctx context.Context, token string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actual.WifiConnectConfirm(ctx, token)
}

// subtypeServer implements the Summation RPC service from summation.proto.
type subtypeServer struct {
	pb.UnimplementedNetworkServiceServer
	s subtype.Service
}

func NewServer(s subtype.Service) pb.NetworkServiceServer {
	return &subtypeServer{s: s}
}

func (s *subtypeServer) getMyService(name string) (Network, error) {
	resource := s.s.Resource(name)
	if resource == nil {
		return nil, errors.Errorf("no network service with name (%s)", name)
	}

	g, ok := resource.(Network)
	if !ok {
		return nil, errors.Errorf("resource with name (%s) is not a Network", name)
	}
	return g, nil
}

func (s *subtypeServer) GetInterface(ctx context.Context, req *pb.GetInterfaceRequest) (*pb.Interface, error) {
	g, err := s.getMyService(req.Name)
	if err != nil {
		return nil, err
	}

	resp, err := g.GetInterface(ctx, req.InterfaceName)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *subtypeServer) ListInterfaces(ctx context.Context, req *pb.ListInterfacesRequest) (*pb.ListInterfacesResponse, error) {
	g, err := s.getMyService(req.Name)
	if err != nil {
		return nil, err
	}

	resp, err := g.ListInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.ListInterfacesResponse{Interfaces: resp}, nil
}

func (s *subtypeServer) WifiConnect(ctx context.Context, req *pb.WifiConnectRequest) (*pb.WifiConnectResponse, error) {
	g, err := s.getMyService(req.Name)
	if err != nil {
		return nil, err
	}

	opts := WifiConnectOptions{
		Interface:       req.InterfaceName,
		SSID:            req.Ssid,
		PSK:             req.Psk,
		ConnectDuration: req.ConnectTimeout.AsDuration(),
	}
	return g.WifiConnect(ctx, opts)
}

func (s *subtypeServer) WifiConnectConfirm(ctx context.Context, req *pb.WifiConnectConfirmRequest) (*pb.WifiConnectConfirmResponse, error) {
	g, err := s.getMyService(req.Name)
	if err != nil {
		return nil, err
	}

	err = g.WifiConnectConfirm(ctx, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	return &pb.WifiConnectConfirmResponse{}, nil
}

func (s *subtypeServer) WifiScan(ctx context.Context, req *pb.WifiScanRequest) (*pb.WifiScanResponse, error) {
	g, err := s.getMyService(req.Name)
	if err != nil {
		return nil, err
	}

	networks, err := g.WifiScan(ctx, req.InterfaceName, req.Duration.AsDuration())
	if err != nil {
		return nil, err
	}
	return &pb.WifiScanResponse{Networks: networks}, nil
}

func newClientFromConn(conn rpc.ClientConn, name string, logger golog.Logger) Network {
	sc := newSvcClientFromConn(conn, logger)
	return clientFromSvcClient(sc, name)
}

func newSvcClientFromConn(conn rpc.ClientConn, logger golog.Logger) *serviceClient {
	client := pb.NewNetworkServiceClient(conn)
	sc := &serviceClient{
		conn:   conn,
		client: client,
		logger: logger,
	}
	return sc
}

type serviceClient struct {
	conn   rpc.ClientConn
	client pb.NetworkServiceClient
	logger golog.Logger
}

type client struct {
	*serviceClient
	name string
}

func clientFromSvcClient(sc *serviceClient, name string) Network {
	return &client{sc, name}
}

func (c *client) GetInterface(ctx context.Context, interfaceName string) (*pb.Interface, error) {
	resp, err := c.client.GetInterface(ctx, &pb.GetInterfaceRequest{Name: c.name, InterfaceName: interfaceName})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *client) ListInterfaces(ctx context.Context) ([]*pb.Interface, error) {
	resp, err := c.client.ListInterfaces(ctx, &pb.ListInterfacesRequest{Name: c.name})
	if err != nil {
		return nil, err
	}

	return resp.Interfaces, nil
}

func (c *client) WifiScan(ctx context.Context, interfaceName string, duration time.Duration) ([]*pb.WifiNetwork, error) {
	resp, err := c.client.WifiScan(ctx, &pb.WifiScanRequest{
		Name:          c.name,
		InterfaceName: interfaceName,
		Duration:      durationpb.New(duration),
	})
	if err != nil {
		return nil, err
	}

	return resp.Networks, nil
}

func (c *client) WifiConnect(ctx context.Context, opts WifiConnectOptions) (*pb.WifiConnectResponse, error) {
	resp, err := c.client.WifiConnect(ctx, &pb.WifiConnectRequest{
		Name:           c.name,
		InterfaceName:  opts.Interface,
		ConnectTimeout: durationpb.New(opts.ConnectDuration),
		Ssid:           opts.SSID,
		Psk:            opts.PSK,
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *client) WifiConnectConfirm(ctx context.Context, token string) error {
	_, err := c.client.WifiConnectConfirm(ctx, &pb.WifiConnectConfirmRequest{Name: c.name, ConfirmationToken: token})
	if err != nil {
		return err
	}

	return nil
}
