// Package main tests out all four custom models in the complexmodule.
package main

import (
	"context"

	"github.com/edaniels/golog"

	"go.viam.com/rdk/examples/customresources/apis/networkapi"
	"go.viam.com/rdk/robot/client"
)

func main() {
	logger := golog.NewDebugLogger("client")
	robot, err := client.New(
		context.Background(),
		"localhost:8080",
		logger,
	)
	if err != nil {
		logger.Fatal(err)
	}
	defer robot.Close(context.Background())

	logger.Info(robot.ResourceNames())

	logger.Info("---- Testing network module -----")
	res, err := robot.ResourceByName(networkapi.Named("network-service"))
	if err != nil {
		logger.Fatal(err)
	}

	logger.Info("---- Testing calls -----")

	network := res.(networkapi.Network)
	ret1, err := network.GetInterface(context.Background(), "en0")
	if err != nil {
		logger.Fatal(err)
	}
	logger.Info(ret1)

	ret2, err := network.ListInterfaces(context.Background())
	if err != nil {
		logger.Fatal(err)
	}

	for _, iface := range ret2 {
		logger.Info(iface)
	}

	ret3, err := network.WifiConnect(context.Background(), networkapi.WifiConnectOptions{})
	if err != nil {
		logger.Fatal(err)
	}
	logger.Info(ret3)
}
