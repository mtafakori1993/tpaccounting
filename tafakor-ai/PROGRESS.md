# Tafakor AI — progress log

Branch: `feature/tafakor-ai` (never pushed, never merged by the agent).

## Repository findings (before Phase 1)
- Single Go binary (`backend/`, chi router) serves `/api/v1` **and** the built SPA; there is no nginx. The
  "reverse-proxy route" touchpoint therefore lives in the Go server (`backend/internal/server`).
- Auth: opaque `tp_session` cookie (HttpOnly) + `tp_csrf` double-submit cookie / `X-CSRF-Token` header.
  `GET /api/v1/auth/me` returns user, permissions, company, fiscal year → used by the AI service to validate every request.
- Security headers: CSP `script-src 'self'; connect-src 'self'` (same-origin AI route needs no CSP change);
  `Permissions-Policy: microphone=()` blocks push-to-talk → the only header change needed (touchpoint c).
- Server `WriteTimeout` is 120 s → the proxy must extend the write deadline for SSE streams.
- Receipts/payments: `POST /treasury/transactions` (kind receipt|payment|transfer, party **or** counter account,
  optional cost center = project). Expenses: `POST /expenses` (category, payment_method cash|bank|credit, `post:true`).
  Cancel-by-reversal: `POST /treasury/transactions/{id}/cancel`, `POST /expenses/{id}/cancel`.
- Budget control: 422 `budget_confirmation_required` / `budget_exceeded` → retried with `X-Budget-Confirm` / `X-Budget-Override`.
- No multi-currency on documents (amounts are base-currency NUMERIC); FX amounts must be converted by the user-confirmed rate.
- Frontend: React 19 + Vite, shared components in `frontend/src/components/ui`, router in `src/App.tsx` (BrowserRouter).
- Host Go is 1.22 but `GOTOOLCHAIN=auto` fetches 1.26; Docker builds use `golang:1.26-alpine`.
- A **production-like stack is running** (`tpaccounting-app-1` on 127.0.0.1:8085). It is never touched; tests use a
  separate compose project `tpai-test` on other ports with its own volumes.

## Plan
1. Skeleton & isolation — folder layout, Go service (health, session validation, SQLite repo, proxy-aware HTTP clients),
   compose file, core touchpoints behind `AI_SERVICE_URL` flag, widget loader, removal procedure.
2. Widget + OpenRouter — Shadow-DOM widget, streaming chat over SSE through the proxy, uploads (picker/drop/paste), status.
3. Intake/classification/tools/safety — extraction + normalisation, voice, classification, playbook + knowledge,
   read/write tools, approval switch + policy engine + tokens, duplicates, ambiguity, audit, learning.
4. Tagging + UI agent — component auto-tagging, app map generator, coverage test, observer/cursor/executor/verifier.
5. Vision fallback.
6. Hardening — undo, recovery, rate limits, cost log, retention, remaining tools, README.
7. Final verification on a throwaway demo company, REPORT.md.

## Status
(updated after each phase below)

### Phase 1 — Skeleton and isolation: DONE
- `tafakor-ai/service`: Go module (own go.mod), health (`/ai-api/healthz`), provider health, session validation via
  `GET /api/v1/auth/me` on every request, CSRF double-submit check, per-user rate limit, SQLite (WAL) behind
  `store.Repository` with all required tables, proxy-aware provider client, direct (never proxied) accounting client.
- Core touchpoints behind `AI_SERVICE_URL`: (a) loader `<script>` in `frontend/index.html`, (b) `/ai-api/*` reverse proxy
  in the Go server (SSE flush, extended write deadline), (c) `Permissions-Policy: microphone=(self)` only when enabled.
- `docker-compose.ai.yml`, `Dockerfile`, `.env.example`; throwaway stack `test/docker-compose.test.yml` (project `tpai-test`,
  ports 18085/18090, own volumes and image tags) + `test/seed_demo.py` demo company.
- Tests green: store (defaults, scoping, approval single-use/expiry/tamper, retention, task isolation), netx (bypass,
  env-proxy ignored), provider (SSE through a forwarding proxy without buffering, proxy-unreachable safe stop with no
  direct fallback, no credential leaks, bounded retries), httpapi (session per request, CSRF, rate limit), core
  `aiproxy_test.go` (disabled = harmless, streaming, microphone header).
- Removal verified by `tafakor-ai/verify-removal.sh` (worktree: remove touchpoints + folder → tree identical to `main`,
  backend builds and tests pass, frontend builds).
- Live check: test core → `/ai-api` → AI container works. The host proxy rejects Docker subnets (D11).

### Phase 2 — Widget + OpenRouter: DONE
- Shadow-DOM widget (one 64 KB bundle, no framework, no imports from the app): draggable orb and panel, fa/en,
  RTL/LTR following `<html lang>`, light/dark via the app's tokens, mobile layout, text chat with streamed answers,
  file picker / drag & drop / paste, human-readable status steps, error cards.
- `AIProvider` + `OpenRouter` (SSE streaming, usage/cost, bounded retries, proxy-only, plain-language errors), `Router` extension point.

### Phase 3 — Intake, classification, tools, safety core: DONE
- Extraction (vision, per-field confidence, normalisation, masking, injection flag, second amount reading),
  voice (`SpeechProvider`, OpenAI-compatible default, number words → digits, ASR alias corrections, editable transcript),
  classification (LLM event + deterministic resolver against the company's own data), knowledge guide, playbook,
  read tools + create_receipt/payment/expense, approval switch + policy engine + tokens, duplicates, ambiguity
  questions (one at a time, recommended first), audit, learning from corrections.
- Live (real model, demo company): 32/32 scenario checks incl. the demo script, loan from an individual, VPN expense
  with FX, learning, prompt injection, auto mode + undo, permission inheritance, queries.

### Phase 4 — Tagging + UI agent: DONE
- Component auto-tagging (i18n-key based), generic tagger, coverage crawl: 60 routes + their "new" dialogs, 0 untagged,
  identical tags in fa and en. `ai-map.json` generated from the router config + crawl.
- Observer, virtual cursor, drivers (React inputs, ComboBox, Jalali DateField, select, tabs, checkbox), executor with
  per-step verification, resumable tasks (refresh → resume/cancel), Cancel always visible.
- Browser e2e: receipt → approval → cursor fills the real receipt form → server-verified result: 13/13.

### Phase 5 — Vision fallback: DONE
- SVG-foreignObject screenshot with numbered candidate marks, `/ai-api/ui/locate` (model returns a candidate index only),
  screenshots stored as files and referenced in the audit. Unit-tested; not needed by the e2e run (tags were enough).

### Phase 6 — Hardening: DONE
- Undo via reversal (always human-approved), error recovery (proxy/provider/session/permission/validation/budget/period),
  rate limits, cost log (per task and 30-day summary in AI Activity), retention, remaining tools (invoice, purchase,
  journal entry as drafts; attach; PDF via the app's letterhead renderer), README.
- Found and fixed during live tests: request body unreadable after SSE headers (buffered now + HTTP test), core
  middleware hid `Flush` (Unwrap + test through the middleware chain), OCR misread (second reading), click on a link
  causing a full reload (page-scoped targeting), form unable to express party+account (UI capability check).

### Phase 7 — Final verification: DONE (fresh throwaway stack, containerized AI service, real model through the proxy)
- AI service unit/integration: 13 packages green (71 test functions). Widget: typecheck + 6 unit tests.
- Core app: full backend suite incl. PostgreSQL integration tests green; frontend 57 tests + typecheck green.
- Live scenarios on a fresh demo company: 32/32. SSE streaming end to end: 16 deltas over 8.9 s (not buffered).
- Browser e2e (virtual cursor fills the real receipt form): 13/13. Tag coverage: 60 routes, 0 problems (fa + en).
- Removal procedure verified (tree identical to `main`, builds and tests pass).

### Follow-up — conversation memory bug: FIXED
- Persisted, versioned conversations; follow-ups merged into a structured frame; only missing fields asked; intent kept;
  explicit "none" answers complete; refresh restores transcript + open question/approval; tool calls/results stored;
  concurrent requests serialized and stale writes rejected. New operation: inventory receipt from production.
- Tests: frame merge/null detection (unit), Galatea multi-turn, intent stability, persistence across HTTP requests and
  refresh, tool results in state, concurrency (`-race`), details-card answers. Live: 37/37 incl. the Galatea conversation;
  verified in Chrome (refresh mid-conversation, follow-up typed in the composer, registered and checked in the app).

### Follow-up — "AI accounting employee" (capability-driven): DONE except the last live re-run (credit exhausted)
- Discovery: `cmd/capmap` → 223 operations (READ_ONLY 98, REQUIRES_USER_CONFIRMATION 39, REQUIRES_APPROVAL 55,
  NOT_SAFE_TO_AUTOMATE 24, NOT_SUPPORTED_BY_AI 7); feature matrix in `CAPABILITIES.md`.
- Registry: generic operation runner over the catalog, filtered by the user's permissions (no hidden superuser).
- Planner: read/ask/answer/plan, refs between steps, approval bound to plan hash, before/after audit, read-back,
  partial-failure resume, self-repair on validation errors, amount guard (D31).
- Unit tests: create-party-then-pay, invented tools/fields refused, restricted ops refused, permission-filtered catalog,
  failure + resume, approval binding, read-only answers, refs, repair, follow-up after reload, amount guard, 402 retry.
- Live (real model, demo company): inventory, create+pay, sales invoice 300,000,000 posted, purchase from file,
  receivables, payables, profit comparison, stock alert, Iranicard balance, restricted perms refused, complex workflow.
  The complex workflow exposed the 150B amount slip → amount guard; wrong demo documents were cancelled. The re-run after
  the fix could not complete: the OpenRouter account has ~$0.17 left (HTTP 402).

### Follow-up — switch to google/gemini-2.5-flash-lite: DONE (live re-run limited by credit)
- Default model changed (config, compose, .env.example, README); OpenRouter fallback models; lookups-in-plans run as reads.
- First live run on flash-lite: 7/14 — 6 failures were OpenRouter "rate-limited upstream" for flash-lite (now: fallback model),
  2 were lookups placed in plans as approval steps (now: run as reads). Stock question re-run after the fix: PASS (60, matches the app).
- Full re-run not possible: OpenRouter account credit ≈ $0.03.
