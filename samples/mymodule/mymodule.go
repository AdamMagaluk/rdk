package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/edaniels/golog"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	"go.viam.com/utils"

	pb "go.viam.com/api/proto/viam/module/v1"
	"go.viam.com/rdk/component/motor"
	"go.viam.com/rdk/config"
	rdkclient "go.viam.com/rdk/grpc/client"
	pbgeneric "go.viam.com/rdk/proto/api/component/generic/v1"
	"go.viam.com/rdk/resource"
	//"go.viam.com/rdk/config"
)

type myComponent struct {
	pbgeneric.UnimplementedGenericServiceServer
}

func (c *myComponent) Do(ctx context.Context, req *pbgeneric.DoRequest) (*pbgeneric.DoResponse, error) {
	res, err := structpb.NewStruct(map[string]interface{}{"Zort!": "FJORD!"})
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
	log.Printf("Config: %+v", cfg)
	log.Printf("Deps: %+v", req.Dependencies)

	if len(req.Dependencies) > 0 {
		rc, err := rdkclient.New(context.Background(), "localhost:8080", logger)
		if err != nil {
			logger.Error(err)
		}
		rName, _ := resource.NewFromString("rdk:component:motor/beepbeep")
		if err != nil {
			logger.Error(err)
		}
		m, err := rc.ResourceByName(rName)
		if err != nil {
			logger.Error(err)
		}
		logger.Debugf("Component1: %+v", m)
		mreal, ok := m.(motor.Motor)
		logger.Debugf("Component2: %+v, %+v", mreal, ok)
	}

	return &pb.AddComponentResponse{}, nil
}

func (s *server) Ready(ctx context.Context, req *pb.ReadyRequest) (*pb.ReadyResponse, error) {
	return &pb.ReadyResponse{Ready: true}, nil
}

// Arguments for the command.
type Arguments struct {
	Socket         string `flag:"0,required,usage=socket path"`
}

var logger = golog.NewDevelopmentLogger("MyModule")

func main() {
	// f, err := os.OpenFile("/tmp/mod.log", os.O_RDWR | os.O_CREATE | os.O_APPEND, 0666)
	// if err != nil {
	// 	log.Fatalf("error opening file: %v", err)
	// }
	// defer f.Close()
	//log.SetOutput(f)


	var err error

	var argsParsed Arguments
	if err := utils.ParseFlags(os.Args, &argsParsed); err != nil {
		log.Fatal(err)
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt)
	signal.Notify(shutdown, syscall.SIGTERM)

	oldMask := syscall.Umask(0o077)
	lis, err := net.Listen("unix", argsParsed.Socket)
	syscall.Umask(oldMask)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterModuleServiceServer(s, &server{})
	pbgeneric.RegisterGenericServiceServer(s, &myComponent{})


	log.Printf("server listening at %v", lis.Addr())
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()
	<-shutdown
	log.Println("Sutting down gracefully.")
	s.GracefulStop()
}
