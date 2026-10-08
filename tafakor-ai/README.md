# Tafakor AI — in-app accounting assistant (add-on)

Tafakor AI works inside Tafakor Pardazan Accounting the way an employee would. It reads receipts, invoices,
typed requests and voice, works out which financial event happened and where it belongs in the company's books,
shows a preview with the debit/credit lines, and registers it after your approval (or automatically within limits
you set). It does this either by driving the real screens with a visible virtual cursor or through the app's public
API. Every success message is built from data read back from the accounting API after the write.

```
Browser ─ app (React) ─┬─ /api/v1  ─────────────► Go accounting server ─► PostgreSQL
   └ widget (Shadow DOM)└─ /ai-api/* (same origin) ─► tafakor-ai service ─┬─► /api/v1 as the user (cookie + CSRF, no proxy)
                                                                          ├─► OpenRouter via HTTP proxy :8118
                                                                          └─► SQLite (own volume)
```

## Contents
- [Layout](#layout) · [Setup](#setup) · [Environment](#environment-variables) · [Proxy](#model-access-and-proxy)
- [Touchpoints](#the-three-touchpoints) · [Tagging and app map](#tagging-and-app-map) · [Approval switch](#approval-switch-and-guardrails)
- [Learning](#learning) · [Security](#security) · [Backup](#backup-and-retention) · [Tests](#tests) · [Removal](#removal)

## Layout
```
tafakor-ai/
  service/      Go service (own go.mod): httpapi, agent (orchestrator), classify, extract, policy, approval,
                duplicates, learning, tools (allowlist), accounting (API client), provider (OpenRouter), speech,
                normalize (digits, Jalali, number words), store (SQLite repository), files, netx (proxy rules)
  widget/       TypeScript widget, one bundle (esbuild) in a Shadow DOM: chat, cards, voice, activity, learned
                rules, settings; agent/ = observer, virtual cursor, drivers, executor, vision fallback
  knowledge/    accounting-model.md, screens.md, ai-map.json (generated)
  test/         throwaway compose stack, demo seed, receipts, live scenarios, SSE check, host-run script
  docker-compose.ai.yml  Dockerfile  .env.example  remove-touchpoints.sh  verify-removal.sh  tagging.patch
```

## Setup
```sh
cp tafakor-ai/.env.example tafakor-ai/.env          # set OPENROUTER_API_KEY (never commit it)
docker compose -f tafakor-ai/docker-compose.ai.yml --env-file tafakor-ai/.env up -d --build
echo 'AI_SERVICE_URL=http://tafakor-ai:8090' >> .env  # main .env of the accounting app
docker compose up -d app                              # the app picks up the flag
```
The AI container joins the accounting network (`ACCOUNTING_NETWORK`, default `tpaccounting_internal`), so the app
reaches it as `tafakor-ai:8090` and it reaches the app as `app:8085`. With `AI_SERVICE_URL` empty the add-on is
off and the app behaves exactly as before. Build behind a restricted network with
`GOPROXY=https://goproxy.cn,direct BUILD_HTTP_PROXY=http://127.0.0.1:8118 docker compose … build`.

## Environment variables
See `.env.example` (all documented there). The most important:

| Variable | Default | Purpose |
|---|---|---|
| `OPENROUTER_API_KEY` | – | model access (server side only) |
| `OPENROUTER_MODEL` / `OPENROUTER_VISION_MODEL` | `anthropic/claude-sonnet-4.6` | chat/classification and vision models |
| `OPENROUTER_PROXY_URL` | `http://host.docker.internal:8118` | proxy for model calls (credentials allowed in the URL) |
| `SPEECH_BASE_URL`, `SPEECH_API_KEY`, `SPEECH_MODEL`, `SPEECH_PROXY_URL` | off | OpenAI-compatible transcription |
| `AI_ACCOUNTING_URL` | `http://app:8085` | accounting app, always called directly |
| `AI_NO_PROXY` | `localhost,127.0.0.1,app,db,tafakor-ai` | hosts that never use a provider proxy |
| `AI_COMPANY_LOCK_AUTO` | `false` | forbid automatic mode for everyone (also settable per company in the widget) |
| `AI_APPROVAL_TTL_SECONDS` | `600` | approval token lifetime |
| `AI_RATE_LIMIT_PER_MINUTE` | `30` | per-user limit for state-changing requests |
| `AI_AUDIT_RETENTION_DAYS` | `365` | audit rows, uploads and screenshots older than this are purged daily |
| `AI_TTS_ENABLED` | `false` | allow spoken replies (browser speech synthesis) |

## Model access and proxy
- All model calls are made by the service to OpenRouter (`AIProvider` interface, streaming). The key never reaches the browser.
- External AI/speech calls use **only** their configured proxy (`*_PROXY_URL`, port 8118). The accounting client
  uses a transport with no proxy at all (environment `HTTP_PROXY` is ignored), and `AI_NO_PROXY` hosts are never proxied.
- In Docker, the host's proxy is `host.docker.internal:8118` (mapped with `extra_hosts: host-gateway`), never `localhost`.
- **The proxy must accept connections from the Docker network.** The proxy on the development host refuses Docker
  subnets (HTTP 403); allow them, e.g. privoxy `permit-access 172.16.0.0/12` (see DECISIONS D11).
- Proxy or provider unreachable: the task stops with a plain message ("…nothing was registered"); mutations are never
  retried; there is no fallback to another route. Bounded retries (2) only for 429/5xx before any text was streamed.
- Health: `GET /ai-api/health/provider` (signed in) checks connectivity through the proxy without spending tokens.
- Streaming through the proxy and the app's `/ai-api` route is verified by `test/verify_sse_proxy.py`.
- Extension point: `provider.Router` maps purposes (chat, classify, extract, ui) to providers; a small cheap
  classifier can be added with `Router.Override(PurposeClassify, …)`.

## The three touchpoints
1. `frontend/index.html`: `<script src="/ai-api/loader.js" defer>`.
2. Go server: `/ai-api/*` → `AI_SERVICE_URL` reverse proxy (same origin, no CORS; SSE flushed).
3. Headers: `Permissions-Policy: microphone=(self)` when enabled (push-to-talk). CSP needs no change (same origin).
Details: [CHANGES_TO_CORE.md](CHANGES_TO_CORE.md).

## Tagging and app map
Shared components emit language-independent tags derived from their props: `data-ai-page` (route pattern),
`data-ai-action` / `data-ai-field` / `data-ai-dialog` (the i18n key of the label, e.g. `treasury.newReceipt`,
`common.amount`), `data-ai-tab`, `data-ai-option-id`, `data-ai-row-id`, `data-ai-nav`. A small tagger covers plain
elements in feature pages.

```sh
cd tafakor-ai/widget
npm run coverage   # visits every route (from App.tsx) in fa and en: fails on any untagged element or tag difference
npm run map        # regenerates knowledge/ai-map.json (screens, routes, fields, actions, dialogs)
npm run e2e        # browser test: receipt → approval → virtual cursor fills the real form → verified result
```
The UI agent's target order: tags → accessibility (role + label) → vision (numbered candidates on a screenshot) →
never blind coordinates. Each step is verified (value shown, option chosen, dialog closed, validation message read).
Drivers handle React controlled inputs, `ComboBox`, the Jalali `DateField`, selects, tabs and checkboxes. Uploads are
attached by the service, never through native file pickers.

## Approval switch and guardrails
- One visible switch per user: **Ask me before registering** (default) / **Register automatically**. Turning it on asks
  for an explicit risk confirmation; turning it off is instant. "This time ask me" per task. Company lock (env or widget).
- Limits: max amount per document per currency, max automatic registrations per day, which actions may be automatic
  (receipts and expenses by default; payments optional; nothing else can be enabled).
- The deterministic policy engine (`service/internal/policy`) decides the mode from facts the service computed. The
  model never sees or sets the mode, the switch or a limit. Always human: duplicates, instruction/document conflicts,
  ambiguous or unknown party/project/account, low-confidence values, closed or locked periods, budget control, validation
  issues, anything not explicitly requested, anything originating from document text, foreign currency, a learned rule still
  in its suggestion stage, amounts over the limit, daily cap. Never automatic: cancellations, journal entries, invoices, checks, payroll.
- Human approval uses a single-use, expiring token bound to a hash of the exact payload (tool, canonical arguments and
  the attached files' SHA-256). Any edit voids it. Simulation mode shows what auto mode would have done.
- Auto-registered documents appear in **AI Activity** with **Undo** (the app's cancel-by-reversal, itself always approved by a human).

## Learning
When you correct a suggestion (answer a question with "remember", or change a value in review), the assistant offers
to remember it. Rules: expense category by item, account per event type (and party), default cash/bank per payment
method, party aliases (also used to correct speech recognition). The first times a rule only suggests ("last time you
chose X"); after 3 confirmations it applies by default and is still shown in the preview. Conflicting rules → a question.
Rules are personal; users with `settings.edit` can share them company-wide (the company playbook). The
"What I've learned" tab lists, disables, promotes and deletes them; every change is audited. Rules never touch the
approval switch or guardrails and are never created from document or page text.

## Security
Session validated on every request through `GET /api/v1/auth/me`; CSRF double-submit; permissions inherited (the API
re-checks everything); company/user scoping of all AI data; per-user rate limits; upload validation by sniffed type and
size; allowlisted tools only (no SQL, shell, HTTP or JavaScript execution); all model output parsed and validated;
document/page text treated as data (injection is flagged and forces human approval); card/account numbers masked
before storage; API key and proxy credentials never logged.

## Backup and retention
AI data lives in the `ai-data` volume: `tafakor-ai.db` (SQLite, WAL) and `files/` (uploads, PDFs, screenshots).
```sh
docker compose -f tafakor-ai/docker-compose.ai.yml exec tafakor-ai sh -c 'cd /data && tar czf - tafakor-ai.db* files' > backups/tafakor-ai.tgz
```
(stop the service for a byte-exact copy, or copy the db with `sqlite3 .backup` if available). Retention: audit rows,
expired tokens, idempotency keys and files older than `AI_AUDIT_RETENTION_DAYS` are purged daily.

## Tests
```sh
cd tafakor-ai/service && go test ./...                      # unit + integration (fake accounting API, scripted model)
cd tafakor-ai/widget && npm run typecheck && npm run build
tafakor-ai/test/stack.sh up && python3 tafakor-ai/test/seed_demo.py   # throwaway stack + demo company
python3 tafakor-ai/test/live_scenarios.py                   # live model, demo company: 32 checks
python3 tafakor-ai/test/verify_sse_proxy.py                 # streaming through the whole chain
cd tafakor-ai/widget && npm run coverage && npm run e2e
sh tafakor-ai/verify-removal.sh
```

## Removal
```sh
docker compose -f tafakor-ai/docker-compose.ai.yml down -v   # stops the service, deletes ONLY the AI volume
sh tafakor-ai/remove-touchpoints.sh --tags                   # removes touchpoints (+ tagging edits; optional, harmless)
rm -rf tafakor-ai                                            # and remove AI_SERVICE_URL from the main .env
```
`verify-removal.sh` checks this procedure on a scratch worktree: the result is identical to `main`, the backend builds
and its server tests pass, and the frontend builds.
