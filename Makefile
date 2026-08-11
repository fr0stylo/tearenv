.PHONY: build check e2e e2e-kind fmt test test-race vet

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

fmt:
	gofmt -w $$(find cmd internal e2e -name '*.go')

check: vet test-race build
