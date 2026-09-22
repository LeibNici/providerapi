.PHONY: build test run web

web:
	cd web && npm ci && npm run build

build: web
	go build -o bin/providerapi ./cmd/providerapi
	go build -o plugins/mock/mock ./plugins/mock
	go build -o plugins/openai-compat/openai-compat ./plugins/openai-compat

test:
	go test ./...

run: build
	./bin/providerapi serve -c config.yaml
