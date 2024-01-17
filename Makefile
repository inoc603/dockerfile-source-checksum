build:
	go build -o docker-source-checksum .

test:
	go test -v . -coverpkg ./... -count 1 -cover -coverprofile coverage.out
