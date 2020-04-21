all: cover

fmt:
	go fmt ./...

vet:
	go vet ./...

cover: test
	go tool cover -func=coverage.out

cover-html: test
	go tool cover -html=coverage.out

test: fmt vet
	go test -v ./... -covermode=count -coverprofile coverage.out
