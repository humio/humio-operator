all: cover

fmt:
	go fmt ./...

vet:
	go vet ./...

cover: test
	go tool cover -func=cover.out

test: fmt vet
	go test -v ./... -covermode=count -coverprofile cover.out
