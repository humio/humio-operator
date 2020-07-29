.PHONY: crds

all: cover

fmt:
	gofmt -l -w -s .

vet:
	go vet ./...

crds:
	hack/gen-crds.sh

cover: test
	go tool cover -func=coverage.out

cover-html: test
	go tool cover -html=coverage.out

test: fmt vet
	go test -v `go list ./... | grep -v test/e2e` -covermode=count -coverprofile coverage.out

install-e2e-dependencies:
	hack/install-e2e-dependencies.sh

run-e2e-tests: install-e2e-dependencies
	hack/install-helm-chart-dependencies-kind.sh
	hack/run-e2e-tests-kind.sh

run-e2e-tests-local-kind:
	hack/start-kind-cluster.sh
	hack/install-helm-chart-dependencies-kind.sh
	hack/run-e2e-tests-kind.sh

run-e2e-tests-local-crc:
	hack/start-crc-cluster.sh
	hack/install-helm-chart-dependencies-crc.sh
	hack/run-e2e-tests-crc.sh
