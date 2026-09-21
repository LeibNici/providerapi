.PHONY: build test run

build:
	go build -o bin/providerapi ./cmd/providerapi
	go build -o plugins/mock/mock ./plugins/mock
	go build -o plugins/openai-compat/openai-compat ./plugins/openai-compat

test:
	go test ./...

run: build
	./bin/providerapi serve -c config.yaml
