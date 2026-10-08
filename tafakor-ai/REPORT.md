# Tafakor AI — overnight build report

Branch `feature/tafakor-ai` (not pushed, `main` untouched). Everything lives in `tafakor-ai/` except the documented
core touchpoints and tagging edits ([CHANGES_TO_CORE.md](CHANGES_TO_CORE.md)). Decisions: [DECISIONS.md](DECISIONS.md)
(D1–D25). Progress log: [PROGRESS.md](PROGRESS.md). How it works and how to run it: [README.md](README.md).

## What was built
- **AI service (Go, own module and container).** It validates the session on every request through the app's
  `/auth/me`, checks CSRF, and inherits the user's permissions by calling `/api/v1` as the user (cookie + CSRF token
  forwarded, never proxied). Its own SQLite store (WAL) sits behind a repository interface with the tables learned_rules,
  aliases, user_settings, company_settings, audit_log, tasks, approval_tokens, idempotency and cost_log. Uploads, PDFs
  and screenshots are files on its own volume.
- **Model access.** `AIProvider` / OpenRouter with streaming, usage and cost logging, and bounded retries for 429/5xx
  only. Calls go only through the configured proxy (8118), with no silent fallback and plain-language errors. A
  per-purpose `Router` is the extension point for a cheap classifier model later.
- **Intake.**
  - **Documents:** vision extraction of images and PDFs with per-field confidence. Persian/Arabic digits, Jalali dates,
    currencies and card/account masking are normalised in Go. Instruction-like text is flagged. The amount is read a
    second time, independently.
  - **Voice:** push-to-talk → `SpeechProvider` (any OpenAI-compatible transcription API) → an editable transcript, with
    spoken numbers turned into digits and learned name corrections applied.
- **Understanding.** An LLM step produces a structured event. A deterministic resolver then maps it onto the company's
  real parties, projects, cash/bank accounts, categories and accounts; it never invents IDs. It asks one targeted
  question at a time with the recommended option first, and stops with instructions when no suitable account exists.
  It handles conflicts (document vs instruction), ambiguity, FX and toman→rial.
- **Safety core.**
  - A deterministic policy engine decides the mode, from the approval switch (default "ask me"), company lock,
    per-task override, limits, every required guardrail and the never-auto list.
  - Approvals use single-use, expiring tokens bound to the canonical payload hash plus the attachment hashes.
  - Duplicate detection runs before every write.
  - Write tools are an allowlist and only run with a matching authorisation.
  - Every result is read back and verified.
  - Undo uses the app's cancel-by-reversal and always needs a human.
  - Full audit trail; cost per task.
- **Learning.** Offers to remember are generated server-side from the user's own corrections. Rules start in a
  "suggest" stage and apply by default after 3 confirmations. Conflicts trigger a question. Scope is personal, or
  company-wide with `settings.edit`. Rules can be viewed, edited, disabled and deleted, and every change is audited.
- **Widget** (one 65 KB script, Shadow DOM). Draggable orb and panel; fa/en with RTL/LTR; light/dark from the app's
  tokens; mobile layout. Streams chat and shows status steps; accepts files by picker, drag & drop or paste; supports
  voice; shows question, preview (debit/credit), approval, result, offers and table cards. Tabs: AI Activity (with
  Undo and cost), What I've learned, Settings (limits, simulation, company lock). The approval switch is always visible.
- **UI agent.**
  - Tagging: automatic, language-independent tags in the shared components, plus a tagger for plain elements.
  - App map: `ai-map.json` is generated from the router config and a crawl.
  - The agent itself: observer, visible virtual cursor, drivers (React inputs, ComboBox, Jalali DateField, select,
    tabs, checkbox), and an executor with per-step verification.
  - Vision fallback: a screenshot with numbered marks; the model may only answer with a candidate number.
  - Resumable tasks and an always-available Cancel.
- **Remaining tools.** Sales/purchase invoices and journal entries (as drafts, never automatic); attach; PDF through the
  app's own letterhead renderer; queries (unpaid invoices, this month's expenses, transactions, reports, open screen).

## What was tested (all green at the end)
| Suite | Result |
|---|---|
| AI service `go test ./...` (fake accounting API + scripted model, test-only) | 13 packages, 71 tests: allowlist, permission inheritance, duplicates, ambiguity, amount conflict, Persian digits/Jalali, spoken numbers, both example events, learning (create/scope/conflict/delete/trust), switch default/persistence, each guardrail individually, limits, company lock, model cannot change mode/switch, token single-use/expiry/tamper, injection, proxy bypass, proxy-unreachable safe stop, SSE through a proxy, verifier failure paths, vision locate, audit completeness, HTTP-level SSE |
| Widget | typecheck, 6 unit tests |
| Core app | backend `go test ./...` incl. PostgreSQL integration tests; frontend 57 tests + typecheck; new `aiproxy_test.go` |
| Live scenarios (`test/live_scenarios.py`, real model, fresh demo company, containerized AI behind the app) | **32/32** |
| Streaming end to end (`test/verify_sse_proxy.py`) | 16 deltas over 8.9 s → not buffered |
| Browser e2e (`widget npm run e2e`) | **13/13**: the cursor fills and saves the real receipt form; the result is verified and the receipt attached |
| Tag coverage (`widget npm run coverage`) | 60 routes + "new" dialogs, 0 untagged, identical in fa and en |
| Removal (`verify-removal.sh`) | tree identical to `main`; builds and tests pass |

The demo script (section 13) was run: the receipt advance from Company X for the website project was extracted. The
second amount reading caught an OCR misread (120,000,000 read as 125,000,000) and forced confirmation. Party and project
were found and the destination account was asked. The duplicate check ran, a preview with debit/credit lines was shown,
and the user approved. It was registered, read back and verified, the original receipt attached, and a PDF generated.
The UI-cursor version of that flow runs in the e2e test with a plain customer payment (D16 explains why advances with
"advances account + party" go through the API). The loan from an individual, the VPN expense (FX) and a correction
that creates a learned rule were also run live.

## Problems found and fixed during testing
1. The request body was unreadable after the SSE headers were sent (HTTP/1.1). It is now buffered first, with an HTTP-level test.
2. The core's logging middleware hid `Flush`, so the `/ai-api` proxy buffered whole answers. Fixed with
   `statusRecorder.Unwrap()` (3 lines in the core) and a test through the real middleware chain.
3. An OCR misread at confidence 0.99. Fixed by reading the amount twice and forcing human confirmation on disagreement (D21).
4. The agent clicked a link with the same tag, which reloaded the page. Targets are now page-scoped, visible only, and buttons win over links.
5. A form couldn't express "ledger account + party". Fixed with a UI capability check: the agent registers directly and says why (D16).
6. A tag differed between languages ("Employees"). `LinkButton` is now tagged by route, and the coverage test compares fa/en.

## Not tested / needs human verification
- **Voice with a real speech provider.** No ASR credentials were available. It is tested against a local fake
  OpenAI-compatible server and the transcript UI is built. Set `SPEECH_BASE_URL/KEY/MODEL` and try push-to-talk (the
  browser asks for microphone permission; this needs HTTPS or localhost).
- **Text-to-speech replies** (`AI_TTS_ENABLED=true`): browser speech synthesis; not checked by ear.
- **The proxy's Docker access control.** During the night the host proxy first answered 403 to Docker subnets and later
  accepted them (D11). If the container reports "network proxy is not responding", allow the Docker subnet in the proxy.
- **The real `tpaccounting` stack showed "Up 5 minutes" at 06:56.** All agent compose commands used the separate project
  `tpai-test` (`-p tpai-test`) and never targeted that stack, so the restart looks like a host/Docker restart. Please
  confirm nothing unexpected happened there.
- **Vision fallback in the browser.** The server side and the protocol are tested; in the e2e run every target was found
  by tags, so the screenshot path never ran in a real browser. The canvas render is browser-dependent.
- **Phones and tablets.** The responsive panel CSS was not checked on a device.
- **Latency.** A request takes ~10–15 s before the first answer: classification plus the double amount reading on
  Sonnet through the proxy. The `Router` extension point is ready for a cheaper classifier model.
- **Manual journal entries, invoices and purchases** created by the assistant are tested only as tool calls (unit level),
  not through a live conversation.

## Follow-up: the assistant as an accounting employee (capability-driven)

**Discovery (Phase 1–3).** `service/cmd/capmap` reads the app's source and produces `CAPABILITIES.md` (feature matrix:
module, capability, UI, backend, AI tool, AI can execute, confirmation, permission, inputs, status) and the embedded
catalog the assistant uses. 223 operations: READ_ONLY 98 · REQUIRES_USER_CONFIRMATION 39 · REQUIRES_APPROVAL 55 ·
NOT_SAFE_TO_AUTOMATE 24 · NOT_SUPPORTED_BY_AI 7. Every tool maps to a real route; nothing is invented (D28–D29).

**Workflows.** A planner proposes multi-step plans (e.g. create the missing counterparty → purchase invoice → payment
from Bank Mellat), validated against the catalog and the user's permissions, approved as one card bound to the exact steps,
executed in order with read-back checks and before/after audit, resumable after a failure (D30).

**Phase 12 findings.**
- A. *Allocating an existing receipt to an invoice is not possible.* Allocations can only be given when a receipt/payment is
  created (`allocations` / `auto_allocate`); there is no endpoint to match an already-registered deposit to an invoice later.
  The assistant says so honestly instead of faking it (e.g. by cancelling and re-entering the receipt).
- B. *Permission checks inside handlers.* Cheque state transitions and some document actions check permissions inside the
  handler, not on the route; capmap extracts them (`inner_permissions`) so the assistant applies the same rule.
- C. *UI calls without a backend route:* none found.
- D. *Backend routes without UI:* `budgets.control.events.get` and `payroll.list` (real gaps); the
  `/payroll/periods/{id}/<action>` routes are aliases of UI-used routes. `sales/purchases.kind.create` were flagged by static
  analysis but do have a UI (the URL is held in a variable) — false negatives, verified by hand.
- E. *Validation duplicated in the UI vs backend:* required markers come from validators but the backend is the authority;
  the assistant relies on the app's 422 messages and repairs the plan from them.
- F. *A model can mis-scale amounts* (150,000,000,000 for 15M toman in a live run). Fixed with a deterministic guard (D31).
  The wrong demo documents were cancelled (payment 21, purchase invoice 8 in the throwaway company).

**Tests.** All unit tests green (incl. planner and amount-guard tests). Live scenarios with the real model passed for all
listed requests before the guard was added. **The re-run of the complex workflow after the fix failed with HTTP 402: the
OpenRouter account has only ~$0.17 credit left** (the key's own $5 limit still shows ~$1.56, but the account is the binding
limit). Add credit, then run `python3 test/employee_scenarios.py http://127.0.0.1:18085`.

## How to run it
See README → Setup. For the throwaway test stack used tonight (still running, demo data only):
```sh
tafakor-ai/test/stack.sh up            # project tpai-test: app http://127.0.0.1:18085, db, tafakor-ai container
python3 tafakor-ai/test/seed_demo.py   # demo company: admin@demo.test / Demo-Passw0rd!, viewer@demo.test / Viewer-Passw0rd!
tafakor-ai/test/stack.sh down          # stop;   stack.sh reset  → also delete the tpai-test volumes (only those)
```
The OpenRouter key is in the gitignored `tafakor-ai/.env` (used by the test stack); it is not in any commit.

## How to remove it
`docker compose -f tafakor-ai/docker-compose.ai.yml down -v` → `sh tafakor-ai/remove-touchpoints.sh --tags` →
`rm -rf tafakor-ai` → remove `AI_SERVICE_URL` from `.env`. Verified by `tafakor-ai/verify-removal.sh`.

## Morning checklist (≈15 minutes, on http://127.0.0.1:18085 as admin@demo.test)
1. The orb appears at the bottom corner. Open it: the header shows "Approval: ask me" and the switch is off.
2. Drop `tafakor-ai/test/fixtures/receipt-customer.png` on the panel and type "Company X paid this for the website
   project, register it as a customer receipt". Answer the account question → check the preview (debit bank /
   credit receivable, project) → **Confirm & Register** with "Show me on screen": watch the cursor fill the receipt
   form; the green result card has a number, PDF and Undo.
3. Open Treasury → Receipts and confirm the document, its project and the attached receipt. Open Settings → Audit
   log: the creator is Demo Admin.
4. Ask "این ماه چقدر هزینه داشتیم؟" → you get an answer from data.
5. Upload `fixtures/receipt-vpn.png` and type "Purchased a VPN for the company": it asks for the FX rate and who paid.
   In Review change the category → it offers "Remember this for next time?" → Yes → check the "What I've learned" tab.
6. Turn on "Register automatically" (a risk confirmation appears), then ask for a small receipt from Company X: it is
   registered automatically, a notice appears, and AI Activity shows it with **Undo**. Undo asks for approval.
7. Drop `fixtures/receipt-injection.png` with no text while auto mode is on: nothing is registered and the instruction
   in the image is ignored. Turn auto mode off.
8. Sign in as viewer@demo.test and ask to register a receipt: it refuses (no permission).
9. Switch the app to English and dark theme: the widget follows (LTR, dark).
10. Optional: set speech credentials and try push-to-talk; the transcript appears for editing before sending.
