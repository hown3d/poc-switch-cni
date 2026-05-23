# PoC Switch CNIs

This PoC tries to demonstrate how switching a CNI works in a live cluster.
Cilium is configured to run only on nodes labeled `cni=cilium` while calico is running on nodes with `cni=calico`.
To ensure connectivity, we run a simple route controller, that adds the route to the docker containers routing table.
Cilium and Calico are both configured to use [Kubernetes Host Scope IPAM](https://docs.cilium.io/en/stable/network/concepts/ipam/kubernetes/), so that routing works.

## Get started

1. `kind create cluster --config=kind.yaml`
2. Setup cluster `skaffold run`
This will:

- Install cilium on nodes with labels `cni=cilium`
- Install calico on nodes with labels `cni=calico`
- Start route controller

3. Create a pod on cilium node and on calico node

```
kubectl run --image nicolaka/netshoot -ti debug-calico --labels=cni=calico --port 80 --overrides '{"spec":{"nodeSelector": {"cni": "calico"}}}'
kubectl run --image nicolaka/netshoot -ti debug-cilium --labels=cni=cilium --port 80 --overrides '{"spec":{"nodeSelector": {"cni": "cilium"}}}'
```

6. Testing connections via service

```bash
kubectl apply -f tests/service/service.yaml
kubectl exec debug-cilium -- nc -lvk 80
kubectl exec debug-calico -ti -- nc -v cilium.default.svc.cluster.local 80
```

6.1. Testing connections via service with ciliums kube-proxy replacement

```
# run cilium in kube-proxy replacement mode
$ helm upgrade --install cilium cilium/cilium \
  --namespace kube-system \
  --values cilium/values.yaml \
  --values cilium/kubeproxy-replacement-values.yaml

# run kube-proxy only on calico nodes
$ kubectl patch daemonsets.apps -n kube-system kube-proxy -p '{"spec":{"template":{"spec":{"nodeSelector":{"cni":"calico"}}}}}'
```

## BGP

Both calico and cilium provide the ability to distribute routes via BPG.
While calico is running a full fledged bgp daemon with bird, cilium is using it's own BGP control plane built ontop of goBGP.

To setup both CNIs with BGP, run `skaffold run -m cnis -p bgp`.
This will enable the BGP control plane in cilium as well as applying the neccessary CRDs both from cilium and calico.
Make sure that the peer IP addresseses are correct, as they are not deterministic.

### Outcome

Currently it is not possible to perform this sort of migration using BGP, since Cilium does unfortunately not import routes from other bgp daemons.

- <https://github.com/cilium/cilium/pull/33035>
- <https://github.com/cilium/cilium/blob/cd4777472adc07d6cb854fd6363cfbf91977db88/pkg/bgp/gobgp/server.go#L129-L148>
- <https://github.com/cilium/cilium/issues/34296#issuecomment-2288046054>

So while bird is able to accept the advertised addresses by Cilium for PodCIDRs, Cilium is not installing the routes from bird into it's datapath, not allowing pods on Cilium nodes to access pods on Calico nodes.
