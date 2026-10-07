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
