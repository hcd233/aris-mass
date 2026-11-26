# Aris MaaS

**English** | [简体中文](README.zh-CN.md)

A **MaaS model aggregation gateway + usage-data curation platform** for personal large-model usage.

Aris unifies access to multiple MaaS platforms, models, and API protocols. Through model aliases, upstream endpoint management, protocol translation, authentication, rate limiting, auditing, and a visual admin console, clients only need to talk to a single gateway address and a model alias — they never need to know the vendor, the real model name, or the upstream API differences.

## Core Capabilities

- **Model aggregation & single entry point**: two-level Endpoint/Model configuration (user-level multi-tenancy — ordinary users manage their own endpoints and models, admins can filter by user and create on their behalf); model aliases hide upstream differences, and one alias can be bound to multiple endpoints. Provides OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages compatible entry points, plus cross-protocol translation between OpenAI and Anthropic (including SSE streaming events).
- **Data curation & governance**: model interactions are aggregated into Sessions; Messages/Tools are stored in a protocol-neutral structure and deduplicated by checksum content addressing. Supports rating, share links, session filtering, and ShareGPT NDJSON streaming export with image inlining — curating data assets for private-model SFT.
- **Audit & runtime monitoring**: every call records four categories of token usage, time to first token, streaming duration, upstream status code, and Trace ID. Provides trend / success-rate / throughput / usage statistics and cron auditing, plus cross-pod runtime monitoring of QPS, CPU, memory, goroutines, and network I/O.
- **Subscription account OAuth (Claude Code / Codex)**: subscription accounts join as a dedicated endpoint type (`oauth_anthropic` / `oauth_openai`) via official OAuth flows. Access tokens refresh automatically (request-path with a 3-minute skew, plus a maintenance cron) and credentials are stored AES-256-GCM encrypted; the breaker key uses a stable per-endpoint identity, so token rotation never resets circuit state. Model aliases bind them like any endpoint — scheduling, fallback, auditing, pricing, and data curation apply unchanged — and subscription quota windows (Claude 5h/7d, Codex primary/secondary) are probed on a cron and shown in the console.
- **Client config export**: `aris model export` generates OpenCode, Claude Code, Codex, Pi, and DSH connection configs from the server's enabled model list — idempotent patching, `.bak` backups, atomic replacement, credential files at `0600`.
- **Security & governance**: GitHub/Google OAuth2 login + JWT admin auth, Proxy API Key call auth, four-tier `pending`/`demo`/`user`/`admin` permissions with user approval (demo account: module whitelist + session-whitelist / full-masking dual views + one-click login + access auditing), Aho-Corasick trigger-word blocking, and Redis token-bucket request rate limiting plus token-usage limiting.

## Comparison with Similar Projects

Four mainstream open-source MaaS gateways are compared below (snapshot **2026-10-19**): [new-api](https://github.com/QuantumNous/new-api) (Go/Gin + React; AGPLv3 + Section 7 terms), [LiteLLM](https://github.com/BerriAI/litellm) (Python + React; MIT + commercial enterprise licence), [OmniRoute](https://github.com/diegosouzapw/OmniRoute) (Node/TypeScript + SQLite; MIT) and [sub2api](https://github.com/Wei-Shaw/sub2api) (Go/Gin + Vue; LGPL-3.0). Only engineering capability is compared — product and go-to-market features are out of scope.

Three structural differences stand out:

- **Conversation bodies are a data-model choice, not a roadmap item.** Aris persists them, so checksum deduplication, scoring and ShareGPT/SFT export are structural capabilities. The other four have no conversation aggregate — LiteLLM can opt in, but its payload stays spend-log-shaped and its dataset export targets evaluation.
- **Quota enforcement and cost estimation are different designs.** new-api and sub2api deduct from quotas and balances (pre-deduct → settle, window soft caps); LiteLLM's budget family requires a database and **fails open without one**; Aris only estimates cost, with no budget gate.

Legend: ✅ yes · ◐ partial · — no. Ratings follow each project's own README/official docs at the snapshot date; numbered cells are explained under [Notes](#notes).

| Dimension | Aris | new-api | LiteLLM | OmniRoute | sub2api |
| --- | --- | --- | --- | --- | --- |
| **Ingress protocols** | ◐[1] | ✅[1] | ✅[1] | ✅ | ◐[1] |
| **Cross-protocol translation** | ✅ | ✅[2] | ✅ | ✅ | ◐[2] |
| **Upstream channel types (API key / subscription OAuth)** | ✅[3] | ◐[3] | ✅[3] | ✅[3] | ✅[3] |
| **Model alias & multi-endpoint binding** | ✅ | ✅ | ✅ | ✅ | ◐[4] |
| **Routing & scheduling** | ✅[5] | ✅[5] | ✅[5] | ✅[5] | ✅[5] |
| **Circuit breaking / isolation / degradation** | ✅[6] | ◐[6] | ◐[6] | ✅[6] | ✅[6] |
| **Multi-tenancy & permissions** | ✅ | ✅ | ✅ | —[7] | ✅ |
| **Authentication & login** | ✅[8] | ✅ | ✅[8] | ◐[8] | ✅ |
| **Rate limiting & concurrency** | ✅[9] | ✅ | ✅[9] | ✅ | ✅ |
| **Usage metering & quota enforcement** | ◐[10] | ✅[10] | ✅[10] | ◐[10] | ✅[10] |
| **Call audit & usage stats** | ✅[11] | ✅[11] | ✅ | ◐ | ◐[11] |
| **Conversation body persisted** | ✅[12] | —[12] | ◐[12] | — | — |
| **Conversation dataset export (ShareGPT/SFT)** | ✅[13] | — | ◐[13] | — | — |
| **Content safety & masking** | ✅[14] | ◐[14] | ✅[14] | ✅ | ◐ |
| **Observability & runtime monitoring** | ✅ | ◐[15] | ✅[15] | ◐[15] | ◐[15] |
| **Token compression** | ✅[16] | — | ◐[16] | ✅[16] | — |
| **MCP / A2A** | — | ◐[17] | ✅[17] | ✅[17] | ◐[17] |
| **Deployment & horizontal scaling** | ✅[18] | ✅[18] | ✅[18] | ◐[18] | ✅ |
| **Extensibility** | ◐[19] | ✅[19] | ✅[19] | ✅[19] | ✅[19] |

### Notes

**1 · Ingress protocols** — Aris ◐: text only (Chat/Responses/Messages + native Decision passthrough). new-api: four text protocols + Realtime/Responses WebSocket + media + task-plugin routes, ~60 channel types. LiteLLM: widest — batches, files, fine-tuning, evals, realtime, videos, ocr, search, memory, native Bedrock/Vertex, MCP + A2A. sub2api ◐: subscription-client shaped (Responses incl. `/backend-api/codex/responses`, Messages, Chat, `/v1/systemone`, Antigravity).

**2 · Cross-protocol translation** — Aris: 7 OpenAI ↔ Anthropic paths incl. SSE events. new-api: RelayKit converts the four text protocols, warning that protocol-specific tools/fields may not map exactly. LiteLLM: `/v1/messages` for all supported providers, but its Rust core covers only a few routes. sub2api ◐: provider-targeted conversions (Anthropic → xAI Responses, Codex Responses ← Chat, Antigravity).

**3 · Upstream channel types** — Aris ✅: `api_key` plus subscription OAuth endpoints (`oauth_anthropic` / `oauth_openai`, AES-256-GCM, refreshed with a 3-minute skew) sharing one forwarding path. new-api ◐: ~60 API-key types plus `ChannelTypeCodex = 57 "ChatGPT Subscription (Codex)"` (OAuth key JSON, `/v1/responses` only); the Claude Code OAuth channel (former type 53) was **reverted**. LiteLLM ✅: API keys plus ChatGPT-subscription device flow and per-user Copilot OAuth — the former has no cost-map price (spend stays $0); Anthropic Pro/Max undocumented. OmniRoute ✅: four-tier fallback (Subscription → API Key → Cheap → Free) with PKCE logins. sub2api ✅: pooled OAuth subscription accounts (Claude/Codex/Grok/Antigravity) with sticky sessions.

**4 · Model alias & multi-endpoint binding** — sub2api ◐: Composite Groups resolve a model to concrete providers; the others have first-class alias → N-endpoint binding with priority and weight.

**5 · Routing & scheduling** — Aris: priority tiers + in-tier A-Res weighted shuffle + session affinity (TTL 5 min, fail-open). new-api: weighted random, draggable priority, channel affinity, `auto` cross-group retry. LiteLLM: 5 strategies (default `simple-shuffle`) + four specialised routers + Auto Router. OmniRoute: 19 combo strategies + 16-factor scoring. sub2api: sticky sessions with per-group account selection.

**6 · Circuit breaking / isolation / degradation** — Aris ✅: `Guard` outside the retry loop — a three-state breaker keyed by `BaseURL\|hash(APIKey)` or stable OAuth endpoint identity (60s window, opens at ≥10 req and ≥50% errors, 30s → half-open with 1 probe) + per-key semaphore (≤32, 1s) → 503 + `Retry-After`; 429 never trips it, while SSE body leases retain slots and report mid-stream failures. new-api ◐: scheduled health probes with auto-disable/recover, no threshold breaker. LiteLLM ◐: router cooldowns only. OmniRoute ✅: provider breaker (408/5xx only, 8×/12×/2× → half-open 60/30/15s) + connection cooldown (×2 backoff, honours `Retry-After`) + per-model lockout + admission queueing. sub2api ✅: account error policy, temporary-unschedulable rules and failover.

**7 · Multi-tenancy & permissions** — OmniRoute —: single-operator, only API-key scoping, IP filtering and 33 MCP scopes. Aris: user-level multi-tenancy. new-api: users/groups/tokens with fine-grained admin permissions. LiteLLM: Organizations → Teams → Users → Virtual Keys (enterprise org/team admins). sub2api: users/groups with per-user and per-account concurrency.

**8 · Authentication & login** — Aris ✅: OAuth2 + JWT + API key, **no 2FA or passkeys**. LiteLLM ✅: SSO (OIDC/SAML, free ≤5 users) + JWT-OIDC lazy key registration + SCIM (enterprise) + failed-login rate limiting. OmniRoute ◐: password login plus an optional OIDC gate.

**9 · Rate limiting & concurrency** — Aris ✅: 100 req/min per IP or API key; the token-dimension limiter exists but **TPM is deliberately effectively unlimited** (`LimitCallProxyLLMToken = 1e8`, documented as a design decision). LiteLLM ✅: four-level RPM/TPM made hard by `enforce_model_rate_limits` (429 + `retry-after: 60`); multi-instance must share Redis or limits are counted per process.

**10 · Usage metering & quota enforcement** — Aris ◐: per-model pricing (micro-units, time windows + context tiers + 5m/1h cache-creation split, snapshotted at call time) but **estimation only, no interception**; quota is a per-key request cap. new-api ✅: quota points (1 USD = 500,000) with pre-deduct → settle → adjust, plus group/token quotas and subscription quota with reset periods. LiteLLM ✅: budget family (key/user/team/team-member/tag/global) that **requires a database — without one `max_budget` fails open**. OmniRoute ◐: per-key USD spend quotas and cost analytics (subscription providers read $0). sub2api ✅: token-level cost with balance/subscription debit and 5h/1d/7d windows (a **post-request soft cap**, so in-flight requests can overshoot).

**11 · Call audit & usage stats** — Aris ✅: per-call `ModelCallAudit` (four token dimensions, first-token vs stream latency, upstream status, user agent, Trace ID, estimated cost) written asynchronously. new-api ✅: usage/drawing/task logs with tokens, cost, duration and first-byte time, request and upstream request IDs; admins also see the retry chain, request conversion and billing path. sub2api ◐: dashboard-level request/token/cost and account state only.

**12 · Conversation body persisted** — Aris ✅: every call's history lands in the Session aggregate, Messages/Tools deduplicated by checksum and stored protocol-neutrally, same-prefix snapshots merged in real time (exceptions: trigger `omit` and the Playground). new-api —: log fields are tokens/cost/latency/IDs only; the issues requesting prompt logging (#924, #2721, #2982, #4852) are all closed. LiteLLM ◐: off by default, opt in with `store_prompts_in_spend_logs`, with `turn_off_message_logging` / `no-log` for redaction.

**13 · Conversation dataset export** — Aris ✅: ShareGPT NDJSON streaming export (one session per line, `X-Total-Sessions` completeness check) preserving multi-turn context, `<think>` reasoning, tool calls and inlined images, with score/model/time filters and pre-export statistics. LiteLLM ◐: Lens datasets (Export JSONL) are **evaluation**-oriented, need the separate Lens service + ClickHouse and have no ShareGPT/SFT format.

**14 · Content safety & masking** — Aris ✅: Aho-Corasick trigger words with `deny` / `omit` / `capture` actions, hot-reloaded via Redis pub/sub and version polling. LiteLLM ✅: ~50 guardrail integrations in four modes, but masking does **not** apply to streamed output and `llmguard` / `llamaguard` / `hide_secrets` / `lakera` need the enterprise licence. new-api ◐: per-channel extra settings and upstream header overrides only.

**15 · Observability & runtime monitoring** — LiteLLM ✅: the fullest metric families (TTFT, overhead, budget remaining, admission/rejection, DB queues), two official Grafana dashboards and a dedicated metrics port that bypasses virtual-key auth (do not expose publicly). new-api ◐: dashboards, health probes with webhook alerts, pprof/Pyroscope, and a custom `/api/perf-metrics` route — but **no Prometheus dependency**. sub2api ◐: dashboard and channel monitor, no Prometheus dependency in `go.mod`. OmniRoute ◐: dashboards, `X-OmniRoute-*` headers and a SQLite audit trail, but no `/metrics`.

**16 · Token compression** — Aris ✅: deterministic tool-output compression applied in place on typed DTOs before forwarding, with `smart_crusher` / `log_compressor` / `search_compressor` / `passthrough` strategies and before/after records. OmniRoute ✅: a 12-engine pipeline (RTK, Caveman, LLMLingua-2, GCF v3.2, OmniGlyph) with a default-on inflation guard. LiteLLM ◐: `compress()` (beta).

**17 · MCP / A2A** — LiteLLM ✅: MCP Gateway (per-key server/tool permissions, MCP cost tracking, `/mcp` usable from Cursor) + A2A Agent Gateway. OmniRoute ✅: built-in MCP server with 110 tools (stdio/HTTP/SSE, 33 scopes) + A2A v0.3. new-api ◐: JS task plugins and a marketplace only. sub2api ◐: a `hashicorp/go-plugin` system only.

**18 · Deployment & horizontal scaling** — Aris ✅: Compose/K8s with `preStop` + `terminationGracePeriodSeconds: 480` and nine Fx lifecycle hooks that drain requests, SSE and worker pools; cross-pod state in Redis. new-api ✅: multi-node needs a shared main DB and identical `SESSION_SECRET`/`CRYPTO_SECRET`, plus Redis for shared rate limits. LiteLLM ✅: Helm (HPA/KEDA), official AWS/GCP Terraform (no Azure), cosign-signed images, Prisma migrations, Redis required for multi-instance. OmniRoute ◐: single-node SQLite, with a remote mode driven by scoped tokens.

**19 · Extensibility** — Aris ◐: no plugin system — extension points are models.dev import and Fx wiring. new-api ✅: JS task plugins with a documented API and marketplace, plus custom OAuth providers. LiteLLM ✅: eight `CustomLogger` hooks, guardrail APIs in any language, custom auth/SSO/secret managers, provider registration by editing one JSON, callbacks from `s3://`/`gcs://`. OmniRoute ✅: plugin framework, marketplace and skills frameworks. sub2api ✅: `hashicorp/go-plugin` and module skills.

## System Architecture

The diagram set follows MECE: each diagram answers one question, while static structure, synchronous request paths, asynchronous data flows, and delivery lifecycles stay separate.

| Perspective | Diagrams | Question |
| --- | --- | --- |
| System & access | 01–02 | What exists, and how are callers authenticated? |
| Proxy & upstream | 03–05 | How are calls forwarded, endpoints scheduled, and subscription credentials maintained? |
| Data assets | 06–08 | How are sessions and images modeled, persisted, and exported? |
| Governance & observability | 09–12 | How do authorization, content safety, telemetry, and maintenance work? |
| Delivery & lifecycle | 13–16 | How is the frontend delivered, and how does the service deploy and stop? |

### System & access

**01 · System overview**: the backend uses DDD layering, with 17 application modules depending on domain abstractions through `command` / `query` / `port`; infrastructure implements repository interfaces in reverse. The global middleware chain is Trace → Compress → Log → Recover → Metrics → Inflight → Guard → CORS → Locale, Fx owns 9 lifecycle hooks, and 6 Cron jobs perform maintenance. [Interactive HTML](docs/diagrams/archify/aris-arch-01.html)

![System overview · DDD layering](docs/diagrams/archify/aris-arch-01.gif)

**02 · Access partitions & authentication**: routes are partitioned into Web / CLI / Proxy / Ops. Web `publicGroup` and `jwtGroup` are parallel, keeping OAuth2, refresh, Demo login, and public shares outside JWT middleware; CLI and LLM Proxy routes are registered separately but share `X-API-Key` authentication; Ops exposes only probes, metrics, and the install script. Authorization is covered separately in 09. [Interactive HTML](docs/diagrams/archify/aris-arch-02.html)

![Access partitions and authentication boundaries](docs/diagrams/archify/aris-arch-02.gif)

### Proxy & upstream

**03 · LLM proxy path**: API-key auth → request/token rate limits → trigger words → candidate resolution → tool-output compression and protocol conversion → Guard / retry / fallback → upstream. Seven OpenAI ↔ Anthropic conversion paths plus native Decision forwarding are supported. JSON / SSE responses return synchronously; session and audit work is only handed off at completion. [Interactive HTML](docs/diagrams/archify/aris-arch-03.html)

![LLM proxy path](docs/diagrams/archify/aris-arch-03.gif)

**04 · Upstream scheduling & resilience**: aliases resolve through the private tenant → public pool → optional legacy shared pool, then use priority tiers, A-Res weighting, and 5-minute affinity. Guard keys are stable and redacted: `BaseURL|first8(sha256(APIKey))` for API-key endpoints and `BaseURL|oauth:{endpointID}` for subscriptions. Same-endpoint backoff precedes cross-endpoint fallback; the SSE lease covers the full body, retaining its slot and reporting mid-stream failures. [Interactive HTML](docs/diagrams/archify/aris-arch-04.html)

![Upstream scheduling and resilience](docs/diagrams/archify/aris-arch-04.gif)

**05 · Subscription OAuth lifecycle**: Claude Code / Codex accounts bind through PKCE and one-time state; the verifier stays server-side, while the endpoint and AES-256-GCM credential are created in one transaction. Request-path refresh uses a 3-minute skew, and the maintenance Cron refreshes proactively and probes quota with 30-minute throttling. Authorization, refresh, probing, and model calls can share `OAUTH_SUBSCRIPTION_PROXY`; token rotation does not reset breaker identity, and quota snapshots never affect scheduling. [Interactive HTML](docs/diagrams/archify/aris-arch-05.html)

![Subscription OAuth credential lifecycle](docs/diagrams/archify/aris-arch-05.gif)

### Data assets

**06 · Session & content model**: Sessions authorize by `api_key_id`; Messages/Tools deduplicate globally by checksum, and Blobs by raw-byte sha256. A separate `FOR UPDATE` transaction merges continuations while preserving session ID, score, and shares, with targeted cache invalidation. ModelCallAudit remains an independent call fact. [Interactive HTML](docs/diagrams/archify/aris-arch-06.html)

![Session and content data model](docs/diagrams/archify/aris-arch-06.gif)

**07 · Asynchronous persistence**: `MessageStoreTask`, `ModelCallAuditTask`, and `DemoAccessAuditTask` share the bounded `Pond storePool` but keep distinct persistence paths. The session transaction neutralizes forged references, externalizes images, locks references, and deduplicates by checksum before post-commit prefix merging. Audits record the final outcome with four token dimensions, two latency phases, upstream status, and estimated cost. [Interactive HTML](docs/diagrams/archify/aris-arch-07.html)

![Asynchronous tasks and persistence data flow](docs/diagrams/archify/aris-arch-07.gif)

**08 · Images & dataset export**: client-supplied `aris-blob:` references are downgraded to text; valid base64 images receive server-generated ImageRefs and move through pending / uploaded states across PostgreSQL and S3-compatible storage. Session details and export share ImageReader. Export filters by API-key ownership, writes one ShareGPT object per NDJSON line, restores images as base64, and uses `X-Total-Sessions` for completeness checks. [Interactive HTML](docs/diagrams/archify/aris-arch-08.html)

![Image externalization and dataset export data flow](docs/diagrams/archify/aris-arch-08.gif)

### Governance & observability

**09 · Permissions, ownership & Demo**: `pending < demo < user < admin`; writes require user or above, ordinary resources are scoped by UserID / APIKeyID, and an empty scope yields an empty result. Only admins maintain public Endpoints. Demo uses fail-closed module allowlists, a session allowlist plus full-masking views, and login/module access auditing. [Interactive HTML](docs/diagrams/archify/aris-arch-09.html)

![Permissions, ownership, and Demo governance](docs/diagrams/archify/aris-arch-09.gif)

**10 · Trigger-word hot reload**: database changes drive full Aho-Corasick rebuilds through `Publish(trigger:changed)`, `INCR(trigger:version)`, 2-second polling, and a 5-minute fallback. `deny` returns a protocol-native refusal, `omit` skips session storage only, and `capture` saves prior context without calling upstream; mixed hits resolve as deny > capture > omit. [Interactive HTML](docs/diagrams/archify/aris-arch-10.html)

![Trigger-word hot reload](docs/diagrams/archify/aris-arch-10.gif)

**11 · Logs, audits & runtime metrics**: three fact chains stay independent—structured logs flow to CLS with `X-Trace-Id`, model calls reach PostgreSQL audits through Pond, and each Prometheus Registry snapshots to Redis every 5 seconds. Monitor currently shows QPS / CPU / memory / goroutines / network, while `/metrics` reads the local Registry through `promhttp.Handler`. [Interactive HTML](docs/diagrams/archify/aris-arch-11.html)

![Logs, audits, and runtime metrics](docs/diagrams/archify/aris-arch-11.gif)

**12 · Background jobs**: `CronManager` supports hot reload and manual triggers, `cron:reload` synchronizes pods, and Redis locks enforce single-instance execution with `CronCallAudit`. The 6 jobs are terminal cleanup, soft-delete purge, Think extraction, trigger-hit sync, blob upload retry, and OAuth maintenance. Session prefix merging runs after each store commit instead of occupying a Cron job. [Interactive HTML](docs/diagrams/archify/aris-arch-12.html)

![Scheduled task trigger and execution](docs/diagrams/archify/aris-arch-12.gif)

### Delivery & lifecycle

**13 · Web frontend runtime**: Next.js statically exports the console; the root layout contains I18n / Theme / Auth, InteractionLayer, and first-frame theme/motion scripts. It currently has 15 dashboard pages, 4 public pages, and 12 shared hooks. `src/lib` is the sole backend gateway; Playground uses SSE and Dataset uses NDJSON. [Interactive HTML](docs/diagrams/archify/aris-arch-13.html)

![Web frontend architecture](docs/diagrams/archify/aris-arch-13.gif)

**14 · Frontend build & embedded delivery**: `next build` emits `/web` assets, `make web-build` copies and pre-compresses each file with `gzip -9`, and `embed.FS` ships them in the server binary. `/web/*` negotiates gzip explicitly, returns 404 for missing assets, and falls back to `index.html` for pages. Maple Mono CN is hosted on Cloudflare R2 rather than embedded. [Interactive HTML](docs/diagrams/archify/aris-arch-14.html)

![Frontend build and embedded delivery](docs/diagrams/archify/aris-arch-14.gif)

**15 · Deployment & release**: master / tag builds produce frontend assets and a Go binary, then publish a linux/amd64 image to GHCR through buildx. Master or manual runs deploy by digest and verify both `/health` and `/web/`. Kubernetes runs 2 replicas with `maxUnavailable: 0` and 512Mi / 1000m limits; tags also publish the four-platform aris client. [Interactive HTML](docs/diagrams/archify/aris-arch-15.html)

![Deployment and release topology](docs/diagrams/archify/aris-arch-15.gif)

**16 · Graceful shutdown**: Fx runs 9 hooks in reverse: Cron → Inflight → Pond → Metrics Flusher → HTTP → Logger → TriggerService → DB → Redis. Inflight drains before Pond stops so truncated streams can still record usage and persistence; Pond stops store → image upload → agent pools in order. Kubernetes coordinates rollout draining with `preStop sleep 10` and `terminationGracePeriodSeconds: 480`. [Interactive HTML](docs/diagrams/archify/aris-arch-16.html)

![Graceful shutdown sequence](docs/diagrams/archify/aris-arch-16.gif)

## Tech Stack

| Layer | Technology |
| --- | --- |
| Backend | Go 1.25 · Fiber v3 · Huma v2 · Cobra · Viper · Uber Fx |
| Data | PostgreSQL + GORM · Redis (cache / token bucket / Cron locks) · SQLite (unit tests) · Pond goroutine pool |
| Observability | Zap + Lumberjack · Tencent Cloud CLS · Prometheus · fgprof · `X-Trace-Id` request tracing |
| Frontend | Next.js 16 (App Router) · React 19 · TypeScript · Tailwind CSS 4 · Base UI/shadcn · Recharts · Mermaid |
| Delivery | Frontend static assets gzip pre-compressed + `embed.FS` embedding · Docker multi-stage build (Distroless nonroot) · Docker Compose · Kubernetes · GitHub Actions |

## Quick Start

### Requirements

- Go 1.25+, Node.js 22+ (only needed for frontend development/building)
- PostgreSQL, Redis (can be started via Docker Compose)

### Start dependencies & configure

```bash
git clone https://github.com/hcd233/aris-mass.git
cd aris-mass
go mod download

# Prepare config (adjust DB, Redis, JWT Secret, OAuth2, etc. as needed)
cp env/api.env.template env/api.env
cp env/postgresql.env.template env/postgresql.env
cp env/redis.env.template env/redis.env

# Start PostgreSQL and Redis
docker volume create postgresql-data && docker volume create redis-data
docker compose -f docker/docker-compose-full.yml up -d
```

### Migrate & start the service

```bash
# Database migration
go run ./cmd/server database migrate

# Build and embed the frontend (required for production; skippable for backend-only dev)
make web-build

# Start the service
go run ./cmd/server server start --host localhost --port 8080
```

After startup:

- Admin console `http://localhost:8080/`
- Health `/health` · Readiness `/ready` · SSE health `/ssehealth`
- Dev-only API docs `/docs`, OpenAPI `/openapi.json` (auto-disabled in production)

First use: log in to the admin console via OAuth2 → admin approves the user → create a Proxy API Key → configure an Endpoint and a Model → then call the OpenAI/Anthropic compatible APIs.

### Frontend standalone development

```bash
cd web && npm ci && npm run dev   # http://localhost:3000
```

## Common Commands

| Command | Description |
| --- | --- |
| `make build` | Production build of server + four-platform `aris` client |
| `make build-server` / `make build-client[-all]` | Build the server / the `aris` client respectively |
| `make web-build` | Build the frontend into `internal/web/dist` and gzip pre-compress |
| `make test` / `make test-cover` | Full test suite / coverage report |
| `make lint` | Custom convention lint (`lint conv`) + static checks (vet/staticcheck/golangci-lint) |
| `make web-lint` / `make web-format` | Frontend ESLint / Prettier |
| `make fgprof` | Pull a remote fgprof profile and open the flame graph |

Server CLI: `server start`, `database migrate`, `object migrate` (`cmd/server`); convention/static analysis: `lint conv/static` (`cmd/lint`); `aris` client CLI: `aris init`, `aris model export`, `aris status`, `aris update` (`cmd/client`).

## API Overview

All APIs are registered with Huma and generate OpenAPI; in development they can be explored interactively at `/docs`.

| Prefix | Auth | Description |
| --- | --- | --- |
| `/api/openai/v1` · `/api/anthropic/v1` | `X-API-Key` | LLM-compatible entry points: chat/completions, responses, messages, count_tokens, models |
| `/api/cli/v1/model/list` · `/aris/client/check` | `X-API-Key` | `aris` client model distribution and client key check |
| `/api/web/v1/oauth2` · `/token` | Public (rate-limited) | OAuth2 login/callback, token refresh |
| `/api/web/v1/user` | JWT | Profile; admins can list, approve/demote, and delete users |
| `/api/web/v1/apikey` · `/session` · `/dataset` | JWT + owner isolation | API keys, sessions/sharing, dataset export |
| `/api/web/v1/endpoint` · `/model` · `/upstream` | JWT (user-level multi-tenancy) | Endpoints, models (user-isolated; admins can filter by user and create on their behalf), aggregated upstream view |
| `/api/web/v1/trigger` · `/cron` · `/audit/cron` · `/metrics` | JWT + admin | Trigger words, Cron management/triggering, Cron audit, runtime metrics |
| `/api/web/v1/audit` | JWT | Model-call audit & statistics (admins see global) |
| `/health` `/ready` `/ssehealth` `/metrics` `/install.sh` | Public | Probes, Prometheus metrics, `aris` client install script |

## Deployment

- **Single-host Compose**: `docker compose -f docker/docker-compose-single.yml up -d` (requires `env/api.env`, `IMAGE_TAG`, and the external network `1panel-network`; the API starts only after the migration container succeeds; host `7070` → container `8080`). Use `docker-compose-dev-single.yml` for development (port `7060`).
- **Kubernetes**: Deployment + Service + ConfigMap/Secret, rolling updates; `preStop` + draining + `terminationGracePeriodSeconds` support lossless shutdown of long-lived SSE connections.
- **CI**: GitHub Actions builds GHCR images and publishes the `aris` client Release (tar.gz + sha256); the server's `/install.sh` provides a self-contained install script.

## Project Structure

```text
cmd/server        Server entry (start / database migrate / object migrate)
cmd/lint          Convention and static-analysis entry
cmd/client        aris client entry
internal/
  router/         Route registration (Huma Operation + middleware binding)
  handler/        HTTP adaptation layer
  application/    Use-case orchestration (identity/session/llmproxy/...)
  domain/         Domain models and rules
  infrastructure/ GORM Repository, Redis, JWT, LLM Transport, OAuth, object storage, goroutine pool
  cron/           6 maintenance jobs (session, Think, trigger, blob, OAuth)
  bootstrap/      Fx dependency injection & lifecycle
  web/dist        Frontend build output (embed.FS)
web/              Next.js admin console source
  src/app/        App Router route groups ((dashboard) 15 pages · portal / login / callback / share)
  src/components/ Base & business components (ui / charts / chat / dataset / playground / motion)
  src/lib/        api-client · auth-context · i18n · theme · types (backend DTO mirrors)
docker/           Dockerfile and Compose configs
env/              Environment variable templates
docs/             Design docs and architecture diagrams
test/             Unit tests and E2E tests
```

## Documentation

- Architecture diagram set: [docs/diagrams/](docs/diagrams/)
- Design & troubleshooting docs: [docs/](docs/)
- Development standards (for contributors and AI agents): [AGENTS.md](AGENTS.md) and [docs/agents/](docs/agents/)

## License

[Apache License 2.0](LICENSE)
