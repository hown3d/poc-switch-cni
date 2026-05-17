package ccm

import (
	"io"

	"github.com/moby/moby/client"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(config io.Reader) (cloudprovider.Interface, error) {
		dockerClient, err := client.New(client.FromEnv)
		if err != nil {
			return nil, err
		}
		return &CloudControllerManager{
			routes: &routes{
				dockerclient: dockerClient,
			},
		}, nil
	})
}

const ProviderName = "kind"

type CloudControllerManager struct {
	routes *routes
}

// Clusters implements [cloudprovider.Interface].
func (c *CloudControllerManager) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// HasClusterID implements [cloudprovider.Interface].
func (c *CloudControllerManager) HasClusterID() bool {
	return false
}

// Initialize implements [cloudprovider.Interface].
func (c *CloudControllerManager) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientBuilder.ClientOrDie("cloud-controller-manager").CoreV1().Events("")})
}

// Instances implements [cloudprovider.Interface].
func (c *CloudControllerManager) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

// InstancesV2 implements [cloudprovider.Interface].
func (c *CloudControllerManager) InstancesV2() (cloudprovider.InstancesV2, bool) {
	return nil, false
}

// LoadBalancer implements [cloudprovider.Interface].
func (c *CloudControllerManager) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// ProviderName implements [cloudprovider.Interface].
func (c *CloudControllerManager) ProviderName() string {
	return ProviderName
}

// Routes implements [cloudprovider.Interface].
func (c *CloudControllerManager) Routes() (cloudprovider.Routes, bool) {
	return c.routes, true
}

// Zones implements [cloudprovider.Interface].
func (c *CloudControllerManager) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}
