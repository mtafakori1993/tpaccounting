# Changes to the core application

The AI service uses only the application's public `/api/v1` API, called as the signed-in user.
No migrations or tables were changed. One **product feature** was added to the application itself (see the end of
this file): the parity requirement says a missing business operation is added to the software, not refused by the AI.

Everything below is removed by `tafakor-ai/remove-touchpoints.sh` (add `--tags` to remove the tagging edits too);
`tafakor-ai/verify-removal.sh` proves the tree is then identical to `main` and that the app builds and its tests pass.

## The three touchpoints (behind the `AI_SERVICE_URL` flag)

| # | File | Change | Disabled behaviour (`AI_SERVICE_URL` empty) |
|---|---|---|---|
| a | `frontend/index.html` | one `<script src="/ai-api/loader.js" defer>` | the loader is an empty 200 script: nothing loads, no console error |
| b | `backend/internal/server/aiproxy.go` (new), `server.go` (+1 route line) | same-origin reverse proxy `/ai-api/*` → AI service, SSE flush, longer write deadline for streams | `/ai-api/*` returns 404 (loader: empty script) |
| b | `backend/internal/platform/config/config.go` | `AIServiceURL` read from `AI_SERVICE_URL` | empty = off |
| b | `docker-compose.yml` | passes `AI_SERVICE_URL` to the `app` service | default empty |
| c | `backend/internal/server/middleware.go` | `Permissions-Policy: microphone=(self)` only when the add-on is enabled (push-to-talk); CSP unchanged (same origin) | `microphone=()` as before |

Supporting edits:
- `backend/internal/server/middleware.go`: `statusRecorder.Unwrap()` (3 lines). Without it the request-logging wrapper hid
  `http.Flusher` from `http.ResponseController`, so streamed `/ai-api` answers were buffered until the end (found by the
  end-to-end SSE check, see REPORT). It is a generic, behaviour-neutral fix for any streamed response.
- `backend/internal/server/aiproxy_test.go` (new): disabled = harmless, streaming through the real middleware chain, microphone header.

## Tagging edits (harmless `data-ai-*` attributes; recorded in `tafakor-ai/tagging.patch`)

Tags are derived automatically from component props and are language independent: a label's tag is the i18n *key*
that produced its text (e.g. `common.amount`), never the visible fa/en text. The coverage crawl verifies this.

| File | Tag(s) |
|---|---|
| `frontend/src/i18n/I18nProvider.tsx` | remembers which i18n key produced each text; exports `aiKeyOf(text)` (no change to translations) |
| `frontend/src/components/ui/aiTags.ts` (new) | `aiKey(label)`, `aiPage(path)`, and `startAiTagger()` which tags plain `<button>/<a>/<input>` elements in feature pages from their translated label, table column header, `<label for>`, or stable code |
| `frontend/src/components/ui/Field.tsx` | `Field` → `data-ai-field` (+ optional `aiKey` prop); `Checkbox`/`Switch` → `data-ai-field` (+ `aiKey` prop) |
| `frontend/src/components/ui/Button.tsx` | `Button`/`IconButton` → `data-ai-action`; `LinkButton` → `data-ai-nav` (its route) |
| `frontend/src/components/ui/Dialog.tsx` | `data-ai-dialog` (from the title key), close button `data-ai-action="common.close"` |
| `frontend/src/components/ui/ComboBox.tsx` | options → `data-ai-option-id` |
| `frontend/src/components/ui/DataTable.tsx` | rows → `data-ai-row-id` |
| `frontend/src/components/ui/Misc.tsx` | `Tabs` → `data-ai-tab`; `ActionMenu` → `data-ai-action` (+ `aiKey` prop) |
| `frontend/src/layout/AppShell.tsx` | `<main data-ai-page>`, nav links `data-ai-nav`, language buttons, user menu tag, starts the tagger |
| `frontend/src/features/payroll/PayrollSettingsPage.tsx` | `aiKey` on data-labelled insurance checkboxes (their label is data, not a translation) |

Frontend tests (`npm test`, 57) and type checks pass with the tags; the backend test suite including the PostgreSQL
integration tests passes.

## Product features (part of the application; kept when the add-on is removed)

Listed in `tafakor-ai/product-changes.txt`; `verify-removal.sh` excludes exactly these paths from its "identical to
`main`" check and still builds and tests the app.

| Feature | Files | Notes |
|---|---|---|
| Settle invoices with an **existing** receipt/payment, and release the allocations | `backend/internal/treasury/service.go` (`AllocateTx`, `ReleaseTxAllocations`), `handler.go` (`POST`/`DELETE /treasury/transactions/{id}/allocations`, permission `treasury.edit`, audited with before/after), `frontend/src/features/treasury/AllocateDialog.tsx`, `TransactionsPage.tsx`, `i18n/fa.ts`, `i18n/en.ts`, `backend/internal/integration/workflow_test.go` | Reuses the existing allocation service (`invoices.AllocatePayment` / `ReleasePayment`): no new accounting logic, the ledger is not touched. Previously allocations could only be given when the receipt was created; a deposit registered first could never be matched to its invoice later, and a paid invoice could only be cancelled by cancelling its receipt. |
