# ProviderApi v1 验收

技术验收：**PASS**（2026-09-21，LeibNici）。

**Technical acceptance baseline:** [`ad7d3a2f8698fbc193d0db0c63a707d96025c5b2`](https://github.com/LeibNici/providerapi/commit/ad7d3a2f8698fbc193d0db0c63a707d96025c5b2) — v1 behavior already accepted. This SHA does not move.

**Release hygiene:** module path, README, minimal CI, this doc only. Zero behavior change. The hygiene commit SHA is not the technical acceptance SHA.

**Release baseline:** — fill only after this PR merges and the small RC passes. That later `main` SHA is what `v1.0.0` will point at. Leave it blank now.

`v1.0.0` **尚未作为正式 release tag**。

## 证据

| 项 | 结果 |
|----|------|
| `go test ./...` | 绿（含 fake upstream 全链路、500→502、plugin crash、reasoning body matrix） |
| OpenRouter 非流式 | `req_e3f389e10867a64a` HTTP 200 `SMOKE_OK` |
| OpenRouter 流式 | `req_5e5d360fc5edd0ea` HTTP 200 SSE + `[DONE]` |
| 流式 404 | `req_6f0e0a1be1a7581f` `sonnet` guardrail，HTTP 404 JSON，不是 200+SSE |
| 上游 500 → 网关 502 | `MapProviderStatus`；`fake-500` / `mock-error` 断言 502 |
| Cursor Chat | `req_02da56e2da388402` HTTP 200 `SMOKE_OK` |
| Cursor Agent tool | `req_ab199d85c37323fe` HTTP 200 `tool_calls` |
| Cursor multi-tool | `req_19ed7c6c78401375` → `req_58b8b7358e6258e8` HTTP 200 `MULTI_TOOL_OK` |
| reasoning alias → 真实 body | `deepseek-low` `req_091bf0e6e944e04a` `effort=low`；`deepseek-high` `req_c287a9a07fe56529` `effort=high`；`deepseek-v4.1-flash` `req_7641a4e8f5e93590` 无 `reasoning` 字段 |
| request > alias | `reasoning_matrix_test.go` |
| metadata canary | `smoke-private-7b2c9e-openrouter-test` 不在 metadata |
| plugin crash recovery | P0：`plugin_crash`、约 3s EOF、随后 restart |

OpenRouter 瞬时 502（`req_fd2f35fd8ba1563d`，随后恢复，未挂死）不构成验收失败。

代码：[v1 P0 PR](https://github.com/LeibNici/providerapi/pull/1)、[v1 P1 PR](https://github.com/LeibNici/providerapi/pull/2)。

## 冻结，不算 v1 缺陷

进 v1.1 或更后，不塞回 v1：

- native Cursor reasoning selector
- per-model `supported_reasoning_levels`
- raw upstream SSE full capture
- provider retry / fallback
- plugin marketplace / sandbox
- 更多 provider plugin
- 超出最小 `go test` / `go vet` / 三二进制 build 的 CI 与 release 工程

最小 GitHub Actions 属于 tag 前的 Release Hygiene，不是新功能。
