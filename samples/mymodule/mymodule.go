package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	pb "go.viam.com/api/proto/viam/module/v1"
	"go.viam.com/rdk/component/motor"
	"go.viam.com/rdk/config"
	rdkclient "go.viam.com/rdk/grpc/client"
	pbgeneric "go.viam.com/rdk/proto/api/component/generic/v1"
	"go.viam.com/rdk/resource"
)

type myComponent struct {
	pbgeneric.UnimplementedGenericServiceServer
}

var	myMotor motor.Motor

func (c *myComponent) Do(ctx context.Context, req *pbgeneric.DoRequest) (*pbgeneric.DoResponse, error) {

	cmd := req.Command.AsMap()
	myMotor.SetPower(ctx, cmd["speed"].(float64), nil)

	logger.Debugf("SMURF INPUT: %+v %+v", cmd, myMotor)

	res, err := structpb.NewStruct(map[string]interface{}{"Speed": cmd["speed"]})
	if err != nil {
		return nil, err
	}

	resp := &pbgeneric.DoResponse{
		Result: res,
	}
	return resp, nil
}

type server struct {
	pb.UnimplementedModuleServiceServer
}

func (s *server) AddComponent(ctx context.Context, req *pb.AddComponentRequest) (*pb.AddComponentResponse, error) {
	cfg, err := config.ComponentConfigFromProto(req.Config)
	if err != nil {
		return &pb.AddComponentResponse{}, err
	}
	logger.Debugf("Config: %+v", cfg)
	logger.Debugf("Deps: %+v", req.Dependencies)

	for _, dep := range req.Dependencies {
		rc, err := rdkclient.New(context.Background(), "localhost:8080", logger)
		if err != nil {
			logger.Error(err)
		}
		rName, _ := resource.NewFromString(dep)
		if err != nil {
			logger.Error(err)
		}
		m, err := rc.ResourceByName(rName)
		if err != nil {
			logger.Error(err)
		}
		logger.Debugf("Component1: %+v\n", m)
		mreal, ok := m.(motor.Motor)
		logger.Debugf("Component2: %+v, %+v\n", mreal, ok)
		myMotor = mreal
	}

	return &pb.AddComponentResponse{}, nil
}

func (s *server) Ready(ctx context.Context, req *pb.ReadyRequest) (*pb.ReadyResponse, error) {
	return &pb.ReadyResponse{Ready: true}, nil
}

var logger = NewLogger()

func NewLogger() (*zap.SugaredLogger) {
	cfg := zap.NewDevelopmentConfig()
	cfg.OutputPaths = []string{"/tmp/mod.log"}
	l, err := cfg.Build()
	if err != nil {
		return nil
	}
	return l.Sugar()
}

func main() {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt)
	signal.Notify(shutdown, syscall.SIGTERM)


	oldMask := syscall.Umask(0o077)
	lis, err := net.Listen("unix", os.Args[1])
	syscall.Umask(oldMask)
	defer os.Remove(os.Args[1])
	if err != nil {
		logger.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterModuleServiceServer(s, &server{})
	pbgeneric.RegisterGenericServiceServer(s, &myComponent{})


	logger.Debugf("server listening at %v", lis.Addr())
	go func() {
		if err := s.Serve(lis); err != nil {
			logger.Fatalf("failed to serve: %v", err)
		}
	}()
	<-shutdown
	logger.Debug("Sutting down gracefully.")
	s.GracefulStop()
}
