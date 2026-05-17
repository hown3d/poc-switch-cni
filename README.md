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
