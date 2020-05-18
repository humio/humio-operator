all: cover

fmt:
	gofmt -l -w -s .

vet:
	go vet ./...

cover: test
	go tool cover -func=coverage.out

cover-html: test
	go tool cover -html=coverage.out

test: fmt vet
	go test -v `go list ./... | grep -v test/e2e` -covermode=count -coverprofile coverage.out

install-e2e-dependencies:
	hack/install-e2e-dependencies.sh

run-e2e-tests: install-e2e-dependencies
	hack/install-zookeeper-kafka.sh
	hack/run-e2e-tests.sh

run-e2e-tests-local:
	hack/start-kind-cluster.sh
	hack/install-zookeeper-kafka.sh
	hack/run-e2e-tests.sh
