package ccm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

var ClusterCIDR netip.Prefix

type routes struct {
	dockerclient *client.Client
}

// CreateRoute implements [cloudprovider.Routes].
func (r *routes) CreateRoute(ctx context.Context, clusterName string, nameHint string, route *cloudprovider.Route) error {
	if err := r.modifyRoute(ctx, clusterName, opCreate, route); err != nil {
		processErr, ok := errors.AsType[processError](err)
		if !ok {
			return err
		}

		stdout, err := io.ReadAll(processErr.stderr)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(stdout)) == "RTNETLINK answers: File exists" {
			klog.Info("route already exists")
			return nil
		}
	}
	return nil
}

// DeleteRoute implements [cloudprovider.Routes].
func (r *routes) DeleteRoute(ctx context.Context, clusterName string, route *cloudprovider.Route) error {
	if err := r.modifyRoute(ctx, clusterName, opDelete, route); err != nil {
		processErr, ok := errors.AsType[processError](err)
		if !ok {
			return err
		}

		stdout, err := io.ReadAll(processErr.stderr)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(stdout)) == "RTNETLINK answers: No such process" {
			klog.Info("route already gone")
			return nil
		}
	}
	return nil
}

type op string

const (
	opDelete = "delete"
	opCreate = "add"
)

func (r *routes) modifyRoute(ctx context.Context, clusterName string, op op, route *cloudprovider.Route) error {
	var nodeInternalIP netip.Addr
	for _, nodeAddr := range route.TargetNodeAddresses {
		if nodeAddr.Type != v1.NodeInternalIP {
			continue
		}
		nodeInternalIP = netip.MustParseAddr(nodeAddr.Address)
	}
	if !nodeInternalIP.IsValid() {
		panic("node internal ip not found")
	}

	containers, err := r.allNodeContainers(ctx, clusterName)
	if err != nil {
		return err
	}

	var routeErr error
	for _, container := range containers {
		log := klog.FromContext(ctx).WithValues("targetNode", route.TargetNode, "operation", op, "container", container.Names)

		// skip route if target node is the container itself
		if container.NetworkSettings.Networks["kind"].IPAddress.Compare(nodeInternalIP) == 0 {
			log.Info("container is targetNode, skipping route")
			continue
		}
		cmd := []string{"ip", "route", string(op), route.DestinationCIDR, "via", nodeInternalIP.String(), "dev", "eth0"}
		log.Info("modifyRoute", "cmd", strings.Join(cmd, " "))
		_, _, err := r.executeCommandInContainer(ctx, container.ID, cmd)
		if err != nil {
			routeErr = errors.Join(routeErr, fmt.Errorf("adding route in container %s: %w", container.ID, err))
		}
	}
	return routeErr
}

// ListRoutes implements [cloudprovider.Routes].
func (r *routes) ListRoutes(ctx context.Context, clusterName string) ([]*cloudprovider.Route, error) {
	nodeMap, err := r.nodeMap(ctx, clusterName)
	if err != nil {
		return nil, err
	}

	type route struct {
		Node string
		Cidr string
		IP   string
	}

	routeSet := sets.New[route]()

	for nodeIP, node := range nodeMap {
		id, err := r.containerIDByName(ctx, node)
		if err != nil {
			return nil, err
		}

		ipRoutes, err := r.listIPRoutes(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("list ip routes of container %s: %w", id, err)
		}

		nodeRouteSet := sets.New[route]()
		for _, iproute := range ipRoutes {
			if iproute.Dest == "" || iproute.Gateway == "" {
				continue
			}
			destPrefix, err := netip.ParsePrefix(iproute.Dest)
			if err != nil {
				continue
			}
			if !ClusterCIDR.Overlaps(destPrefix) {
				klog.Infof("route dest %s does not match cluster pod cidr, skipping", destPrefix)
				continue
			}

			gateway, err := netip.ParseAddr(iproute.Gateway)
			if err != nil {
				return nil, err
			}

			targetNode, ok := nodeMap[gateway]
			if !ok {
				if iproute.Device == "cilium_host" {
					iproute.Gateway = nodeIP.String()
					targetNode = node
				} else {
					klog.Infof("gateway %s is not a node", gateway)
					continue
				}
			}
			nodeRouteSet = nodeRouteSet.Insert(route{
				Node: targetNode,
				IP:   iproute.Gateway,
				Cidr: iproute.Dest,
			})
		}

		// intersection ensures that we only report routes that are present on all nodes.
		// Otherwise reconcile must recreate the route, so that every node has the route in it's table.
		if routeSet.Len() == 0 {
			routeSet = nodeRouteSet
		} else {
			routeSet = nodeRouteSet.Intersection(routeSet)
		}

		klog.V(1).InfoS("new route set after intersection",
			"routeSet", routeSet.UnsortedList(),
			"nodeRouteSet", nodeRouteSet.UnsortedList(),
			"node", node)
	}

	routeList := routeSet.UnsortedList()
	cloudproviderRoutes := make([]*cloudprovider.Route, 0, len(routeList))
	for _, r := range routeList {
		cloudproviderRoutes = append(cloudproviderRoutes, &cloudprovider.Route{
			TargetNode: types.NodeName(r.Node),
			TargetNodeAddresses: []v1.NodeAddress{
				{
					Type:    v1.NodeInternalIP,
					Address: r.IP,
				},
				{
					Type:    v1.NodeHostName,
					Address: r.Node,
				},
			},
			EnableNodeAddresses: true,
			DestinationCIDR:     r.Cidr,
		})
	}

	return cloudproviderRoutes, nil
}

func (r *routes) nodeMap(ctx context.Context, clusterName string) (map[netip.Addr]string, error) {
	containers, err := r.allNodeContainers(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	m := make(map[netip.Addr]string, len(containers))

	for _, container := range containers {
		containerInspect, err := r.dockerclient.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
		if err != nil {
			return nil, err
		}

		network, ok := containerInspect.Container.NetworkSettings.Networks["kind"]
		if !ok {
			continue
		}
		nodeName := containerInspect.Container.Config.Hostname
		m[network.IPAddress] = nodeName
	}
	return m, nil
}

func (r *routes) containerIDByName(ctx context.Context, name string) (string, error) {
	filters := client.Filters{}
	filters = filters.Add("name", name)
	containers, err := r.dockerclient.ContainerList(ctx, client.ContainerListOptions{
		Filters: filters,
	})
	if err != nil {
		return "", err
	}
	if len(containers.Items) == 0 {
		return "", fmt.Errorf("container list returned 0 items")
	}
	if len(containers.Items) == 1 {
		return containers.Items[0].ID, nil
	}
	for _, c := range containers.Items {
		for _, n := range c.Names {
			n, _ := strings.CutPrefix(n, "/")
			if n == name {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("container with name %s not found", name)
}

func (r *routes) allNodeContainers(ctx context.Context, clusterName string) ([]container.Summary, error) {
	filters := client.Filters{}
	filters = filters.Add("label", fmt.Sprintf("io.x-k8s.kind.cluster=%s", clusterName))
	containers, err := r.dockerclient.ContainerList(ctx, client.ContainerListOptions{
		Filters: filters,
	})
	if err != nil {
		return nil, err
	}
	return containers.Items, nil
}

type ipRoute struct {
	Dest    string `json:"dst"`
	Gateway string `json:"gateway"`
	Device  string `json:"dev"`
}

func (r *routes) listIPRoutes(ctx context.Context, containerID string) ([]ipRoute, error) {
	cmd := []string{"ip", "--json", "route", "list"}
	stdout, _, err := r.executeCommandInContainer(ctx, containerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("get route list of container: %w", err)
	}
	buf := new(bytes.Buffer)
	stdout = io.TeeReader(stdout, buf)
	routes := []ipRoute{}
	if err := json.NewDecoder(stdout).Decode(&routes); err != nil {
		return nil, fmt.Errorf("decoding ip route list json output: %w", err)
	}
	klog.V(1).Infof("ip route output: %s", buf.String())
	return routes, nil
}

func (r *routes) executeCommandInContainer(ctx context.Context, containerID string, cmd []string) (io.Reader, io.Reader, error) {
	res, err := r.dockerclient.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("exec create: %w", err)
	}

	attachRes, err := r.dockerclient.ExecAttach(ctx, res.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("exec attach: %w", err)
	}

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachRes.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("demultiplex output: %w", err)
	}

	inspectRes, err := r.dockerclient.ExecInspect(ctx, res.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("exec inspect: %w", err)
	}
	if inspectRes.ExitCode != 0 {
		return nil, nil, processError{code: inspectRes.ExitCode, stderr: &stderr, stdout: &stdout}
	}
	return &stdout, &stderr, nil
}

type processError struct {
	code   int
	stderr *bytes.Buffer
	stdout *bytes.Buffer
}

func (e processError) Error() string {
	return fmt.Sprintf("process exit with code %d\nstderr:\n%s\nstdout:\n%s\n", e.code, e.stderr.Bytes(), e.stdout.Bytes())
}

var _ cloudprovider.Routes = (*routes)(nil)
