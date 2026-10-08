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
