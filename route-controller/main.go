package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"syscall"

	"github.com/hown3d/poc-switch-cni/route-controller/ccm"

	"github.com/spf13/pflag"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/cloud-provider/app"
	cloudcontrollerconfig "k8s.io/cloud-provider/app/config"
	"k8s.io/cloud-provider/names"
	"k8s.io/cloud-provider/options"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"
)

func main() {
	ccmOptions, err := options.NewCloudControllerManagerOptions()
	if err != nil {
		klog.Fatalf("Unable to initialize command options: %v", err)
	}

	fmt.Println("starting Controller")
	controllerInitializers := app.DefaultInitFuncConstructors
	controllerAliases := names.CCMControllerAliases()

	additionalFlags := cliflag.NamedFlagSets{}

	// setup context
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	command := app.NewCloudControllerManagerCommand(ccmOptions, cloudInitializer(ctx), controllerInitializers, controllerAliases, additionalFlags, ctx.Done())
	pflag.CommandLine.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	logs.InitLogs()
	defer logs.FlushLogs()

	if err := command.ExecuteContext(ctx); err != nil {
		logs.FlushLogs()
		os.Exit(1) //nolint:gocritic // os.Exit(1) is executed before defer
	}
}

func cloudInitializer(ctx context.Context) func(config *cloudcontrollerconfig.CompletedConfig) cloudprovider.Interface {
	return func(config *cloudcontrollerconfig.CompletedConfig) cloudprovider.Interface {
		cloudConfig := config.ComponentConfig.KubeCloudShared.CloudProvider
		ccm.ClusterCIDR = netip.MustParsePrefix(config.ComponentConfig.KubeCloudShared.ClusterCIDR)
		// initialize cloud provider with the cloud provider name and config file provided
		cloud, err := cloudprovider.InitCloudProvider(cloudConfig.Name, cloudConfig.CloudConfigFile)
		if err != nil {
			panic(err)
		}

		return cloud
	}
}
