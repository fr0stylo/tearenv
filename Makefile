.PHONY: build check e2e e2e-kind fmt lint manifests test test-race vet

build:
	mkdir -p bin
	go build -o bin/tearenv ./cmd/tearenv
	go build -o bin/tearenvd ./cmd/tearenvd

test:
	go test ./...

test-race:
	go test -race ./...

e2e:
	go test -count=1 ./e2e

e2e-kind:
	TEARENV_KIND_E2E=1 go test -count=1 -run TestKubernetesScalingWithKind -timeout=10m ./e2e

vet:
	go vet ./...

lint:
	go tool golangci-lint run

manifests:
	helm lint deploy/helm/tearenv
	helm template tearenv deploy/helm/tearenv --namespace tearenv-system >/dev/null
	helm template tearenv deploy/helm/tearenv --namespace tearenv-system \
		--set blueprint.enabled=true \
		--set-file blueprint.document=deploy/kustomize/overlays/blueprint/environment-blueprint.yaml >/dev/null
	kubectl kustomize deploy/kustomize/overlays/default >/dev/null
	kubectl kustomize deploy/kustomize/overlays/blueprint >/dev/null
	kubectl kustomize deploy/kustomize/overlays/load-balancer >/dev/null
	kubectl kustomize deploy/kustomize/overlays/public-keys >/dev/null
	kubectl kustomize deploy/kustomize/overlays/cluster-wide >/dev/null

fmt:
	gofmt -w $$(find cmd internal e2e -name '*.go')

check: lint vet test-race build
