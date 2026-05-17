export DOCKER_HOST=$(shell docker context inspect $(docker context show) | jq -r '.[] | .Endpoints.docker.Host')

.PHONY: route-controller
route-controller:
	go run ./route-controller/main.go --cloud-provider=kind --cluster-name=switch-cni --kubeconfig=${KUBECONFIG} --controllers=node-route-controller --cluster-cidr=10.244.0.0/16 --leader-elect=false

kind:
	kind create cluster --config=kind.yaml
