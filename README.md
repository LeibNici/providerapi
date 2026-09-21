# ProviderApi

OpenAI Chat Completions gateway for Cursor. Cursor talks to ProviderApi; ProviderApi talks to provider plugins over JSON-RPC on a Unix socket.

```text
Cursor  →  POST /v1/chat/completions  →  ProviderApi  →  plugin  →  upstream
```

v1 exposes only Chat Completions. There is no `/v1/responses`, dashboard, or multi-tenant stack.

## Build

```bash
go build -o bin/providerapi ./cmd/providerapi
go build -o plugins/mock/mock ./plugins/mock
go build -o plugins/openai-compat/openai-compat ./plugins/openai-compat
```

## Run (mock)

```bash
cp config.example.yaml config.yaml
./bin/providerapi serve -c config.yaml
```

Health check:

```bash
curl http://127.0.0.1:8317/health
```

Point an OpenAI SDK or Cursor at:

```text
http://localhost:8317/v1
```

Use model alias `mock` for the in-process mock plugin.

## OpenRouter (openai-compat instance)

`openrouter` is a configured instance of the generic `openai-compat` plugin, not a hardcoded OpenRouter provider.

1. Export a key (never put it in YAML, traces, or git):

```bash
export OPENROUTER_API_KEY=...
```

2. In Cursor, set Base URL to `http://localhost:8317/v1` and pick a DeepSeek V4.1 Flash alias (smoke-tested on OpenRouter):

   - `deepseek-v4.1-flash` — default; no `reasoning` field in upstream body
   - `deepseek-low` — `reasoning.effort: low`
   - `deepseek-high` — `reasoning.effort: high`

   Upstream slug: `deepseek/deepseek-v4.1-flash`.

3. Inspect a request:

```bash
./bin/providerapi requests
./bin/providerapi trace req_xxx
```

Admin API listens on `127.0.0.1:8318` only.

## Layout

- `cmd/providerapi` — `serve`, `models`, `plugins`, `requests`, `trace`
- `internal/protocol` — canonical request/event IR
- `internal/api` — OpenAI HTTP adapter + admin
- `internal/plugin` — spawn / handshake / stream / cancel / audit
- `sdk/plugin` — JSON-RPC plugin server
- `plugins/mock` — mock provider
- `plugins/openai-compat` — generic OpenAI-compatible upstream client

## Security

- Credentials exist only as environment variables injected at plugin spawn.
- Handshake carries `base_url`, `extra_headers`, and `reasoning_map` only.
- Traces redact Authorization / API keys / cookies / tokens / secrets.
- `~/.providerapi` is created as `0700`; db and traces are `0600`.
