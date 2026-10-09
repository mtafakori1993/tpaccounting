# Decisions

Each entry: decision — alternatives — reason.

D1. **Reverse-proxy route lives in the Go server** (`/ai-api/*` → `AI_SERVICE_URL`), enabled only when `AI_SERVICE_URL` is set.
Alternatives: add nginx/Caddy in front. Reason: the repo has no reverse proxy; adding one would change deployment for every user.

D2. **Disabled = harmless.** When `AI_SERVICE_URL` is empty the core answers `/ai-api/loader.js` with an empty script (200)
and every other `/ai-api/*` with 404. Alternative: no route at all (the `<script>` tag would 404 in the console).
Reason: the main app must behave exactly as before, without console errors.

D3. **Storage: SQLite via `modernc.org/sqlite`** (pure Go, CGO off, WAL) behind `store.Repository`. Alternative: mattn/go-sqlite3
(needs CGO). Reason: static binary, same build style as the core.

D4. **The AI service has its own Go module** (`tafakor-ai/service/go.mod`) and never imports core packages.
The Jalali algorithm is re-implemented (small, tested) rather than imported. Reason: removability / isolation.

D5. **Session validation on every request** via `GET /api/v1/auth/me` with the forwarded `tp_session` cookie; no cache.
Alternative: short TTL cache. Reason: spec says every request; the call is cheap and keeps revocation immediate.

D6. **CSRF is checked by the AI service too** (double submit: `tp_csrf` cookie must equal `X-CSRF-Token`) on every
state-changing `/ai-api` request, and the same token is forwarded to the accounting API.

D7. **Proxy scope.** One `http.Transport` per external provider with `Proxy` set to that provider's `*_PROXY_URL`;
the accounting client uses a transport with `Proxy: nil` (explicit bypass, ignores `HTTP(S)_PROXY` env). An extra
`AI_NO_PROXY` list is honoured for provider URLs that are internal. No silent fallback to a direct route.

D8. **Docker networking.** The AI container joins the core compose network (`tpaccounting_internal`, configurable) so the
core reaches it as `http://tafakor-ai:8090` and it reaches the core as `http://app:8085`; the host proxy is reached as
`host.docker.internal:8118` via `extra_hosts: host-gateway`.

D9. **Foreign currency.** Accounting documents have no currency field (base currency only). For a foreign-currency
receipt the assistant always asks for the FX rate (human approval forced), books the converted base amount and writes
the original amount/currency/rate into the description. Alternative: refuse FX. Reason: matches the app's model.

D10. **OpenRouter key handling.** Provided at runtime by the user; stored only in the gitignored `tafakor-ai/.env`,
never in code, docs, logs or commits.

D11. **Host proxy refuses Docker subnets (found during testing).** The proxy on host port 8118 answers
`403 Forbidden` to CONNECT requests coming from Docker bridge networks (172.x) and works from 127.0.0.1, i.e. it
has a source-address ACL. The compose file still targets `host.docker.internal:8118` as required; the operator must
allow the Docker subnet in the proxy (e.g. privoxy `permit-access 172.16.0.0/12`) — listed under "Needs human
verification". The agent did not change the host proxy. Live model tests were run with the service binary on the host
(`test/run-host-ai.sh`, bound to the docker0 address only), which really goes through 127.0.0.1:8118.
Alternatives: `network_mode: host` for the container, or a TCP relay. Rejected: they would change production
networking or work around the proxy's access control.
**Update (final verification, 06:56 local):** after a host/Docker restart the same proxy accepted connections from the
Docker network (HTTP 200 from inside the AI container), and the whole final verification ran production-style
(app → `/ai-api` → AI container → `host.docker.internal:8118` → OpenRouter). The proxy's source ACL is therefore
environment-dependent: if it returns 403 again, allow the Docker subnet in the proxy configuration.

D12. **GOPROXY build argument.** proxy.golang.org returns 403 for some modules from this network; the AI image takes
`GOPROXY` as a build arg (default proxy.golang.org) so operators can use a mirror (tests used goproxy.cn).

D13. **Budget control in auto mode is conservative.** Budget events are only known when posting. If the company has an
active budget, expenses and payments always wait for a human (`no_budget_event` fails); a 422 soft limit during
execution turns into a new human approval with the confirmation header bound into the payload; hard limits stop
with a plain message (an authorised override must be done in the app). Alternative: read budget lines to predict.
Reason: never post into a budget event automatically.

D14. **Playbook = company-scope learned rules.** The "company playbook" is the list of rules with scope `company`,
editable in the widget's "What I've learned" screen by users with `settings.edit`; they are shown to the classifier as
suggestions. The static guide is `knowledge/*.md` + the generated `knowledge/ai-map.json`.

D15. **Invoices, purchases and manual journal entries are created as drafts and are never automatic.** Cancellations,
journal entries, checks and payroll are on the never-auto list. Reason: tax/inventory/manual postings need review.

D16. **UI path only when the form can express the exact payload.** E.g. the receipt form cannot put a party on a
ledger-account line, and the expense form lists only suppliers for credit; in such cases the assistant registers the
same approved payload through the API and says so in the chat. A UI attempt that creates nothing is reported as a
failure; "register directly instead" is an explicit user click and is executed at most once (idempotency key).

D17. **Language-independent tags from i18n keys.** `t()` records the key that produced each text; components derive
tags from their label. Collisions (two keys with the same text in one language) are caught by the coverage crawl,
which compares tags in fa and en; `LinkButton` is tagged by route and components accept an explicit `aiKey`.

D18. **Screenshots: SVG `foreignObject` → canvas, in the widget, no library.** It re-uses the live DOM and the
document's stylesheets, so the Vazir font and RTL layout are kept; html2canvas re-implements layout and handles
RTL/Persian shaping poorly; server-side Chromium cannot see the user's session state. Vision answers are a candidate
number from a "set of marks" overlay, never raw coordinates; screenshots are stored as files, referenced in the audit.

D19. **Generating a PDF needs no approval card** (it changes no accounting data); it renders through the app's own
`POST /pdf` as the user and is stored on the AI volume.

D20. **Attaching the original document is part of the approved payload** (the preview shows the file and the payload
hash includes the file's SHA-256).

D21. **Amounts are read twice.** After extraction a second, focused vision call reads the total; any disagreement makes
the amount low-confidence (human confirmation). Found necessary in live testing (a 120,000,000 receipt was read as
125,000,000 with self-reported confidence 0.99).

D22. **Customer advances** are credited to the company's "customer advances" liability account (if exactly one exists)
with the customer on the line; otherwise they stay as an unallocated credit on the customer's account (noted).

D23. **Loans from persons**: the standard chart has no shareholder-loan account; the assistant offers matching liability
accounts (loans, partners' current, other payables) and never pre-selects one until the user's answer is learned.

D24. **Speech default = OpenAI-compatible `/audio/transcriptions`** (configurable URL/key/model/proxy) because it needs no
new infrastructure and many providers (and self-hosted Whisper servers) expose it. Disabled when `SPEECH_BASE_URL` is empty.

D25. **Model output is parsed as JSON objects with validation**, not tool-calling: the model never sees or names write
tools; the orchestrator maps validated events onto allowlisted tools.

D26. **Conversation state (fix for "the assistant forgets").** Root cause: every typed message started a new task with
no history; only question *cards* were stateful; free-form questions were never stored; "none" was indistinguishable
from "not answered"; saves were last-writer-wins. Now: a persisted, versioned conversation (transcript, tool calls and
results, active operation) under optimistic locking plus a per-conversation lock; a structured frame per operation with
slot statuses known / null / inferred / unknown; follow-up messages are interpreted in *update mode* (state + history in
the prompt) and MERGED deterministically in Go (the operation type never changes unless the user explicitly switches;
explicit "no code" answers are also detected deterministically); only genuinely missing fields are asked, in one
question. Alternative: send the whole transcript to the model each turn and trust it. Rejected: the model may still
re-ask or reinterpret; the merge must not depend on it.

D27. **Inventory receipt from production** is a new operation on the app's existing APIs: create the product if it
does not exist (`POST /products`, code empty = numbered by the app) and a posted stock receipt (`POST /inventory/documents`,
Dr inventory / Cr the posting rule "inventory adjustment"). Default warehouse and value-per-unit are inferred (shown in the
preview, never asked); goods bought from a supplier are redirected to a purchase invoice. Never automatic.

D28. **The tool registry is generated from the application's source, not written by hand.** `service/cmd/capmap`
parses the backend's routes, route permissions, request structs, validator enums and in-handler permission checks, and
the frontend's API calls, into `internal/catalog/capabilities.json` (embedded) and `CAPABILITIES.md`. Each operation is
classified READ_ONLY / REQUIRES_USER_CONFIRMATION / REQUIRES_APPROVAL / NOT_SAFE_TO_AUTOMATE / NOT_SUPPORTED_BY_AI from its
HTTP method, action and module (users, roles, permissions, settings, backups, fiscal-year closing and deletes of
posted documents are never executed). Re-run capmap when the app changes; a test fails if the embedded catalog is unreadable.

D29. **One generic operation runner instead of hundreds of hand-written tools.** `tools/operations.go` calls any
executable catalog operation through the app's own API with the user's token: path/query/body keys are checked against
the catalog schema (unknown keys rejected), the user's permissions are checked with the app's rule (all route permissions,
any one in-handler permission), and the app does all validation and accounting. "Required" markers from validators are
hints only — the app's 422 is the authority and is fed back to the planner (≤2 repairs).

D30. **Multi-step planner with plans bound to approval.** The model reads (any READ_ONLY op), asks, answers, or proposes
a plan of steps that may reference earlier results (`$s1.id`). The plan is validated server-side, shown as a card, and the
approval token is bound to the hash of the exact pending steps; any re-plan invalidates the old token. Steps run in order,
each verified by reading the record back and audited with before/after; a failure stops the plan, keeps completed steps
and lets the user continue from there without redoing them.

D31. **Deterministic amount guard.** In a live run the model planned 150,000,000,000 rial for "15 million toman"
(10,000× too much); the approval card was the only defence. Now every money field (amount, unit_price, debit, credit, …)
in a plan must equal an amount the user wrote (Persian/English number words normalized; toman ×10 when the base currency
is rial), an amount from the uploaded document, a number the application returned, a reference to an earlier step, or a
stated total divided/multiplied by a stated quantity. Otherwise the plan is sent back with the allowed amounts; if the model
cannot fix it, nothing is proposed. Amounts are also shown formatted (with thousands separators) on the plan card.

D32. **OpenRouter 402 "in-flight" credit errors** are retried with a short backoff (bounded); a plain 402 is not. Users see
"the AI service's credit is not enough — ask the administrator" rather than a generic failure.

D33. **Default model `google/gemini-2.5-flash-lite` (cost), with OpenRouter fallback `google/gemini-2.0-flash-001`.** Requested by
the user because of price (~30× cheaper than Claude Sonnet). Consequences handled in code rather than prompts: (a) the smaller
model puts lookups (list/get tools) into plans — these are now run immediately as reads (no approval) and the model re-plans
only the changes with real ids; (b) the free shared capacity of flash-lite is sometimes "rate-limited upstream", so OpenRouter's
`models` fallback list is sent (`OPENROUTER_FALLBACK_MODELS`). The deterministic guards (catalog validation, permissions,
amount guard D31, approval binding) are model-independent, so a weaker model can fail to finish a task but cannot register a
wrong amount or an unapproved change. Expect lower task completion than with Claude on complex multi-step requests; set
`OPENROUTER_MODEL` back to a stronger model if that matters more than cost.

D34. **Capability parity.** The assistant is a second interface to the application: every operation the signed-in user
can perform in the app is executable through it, limited only by the user's permissions, approval/confirmation and the
app's own accounting rules. Former `NOT_SAFE_TO_AUTOMATE` operations (users, roles, settings, company, fiscal years and
periods, payroll rules, budget control rules, account mappings) are now `REQUIRES_APPROVAL` with a **critical** flag
(strong warning on the card, never automatic). Only sign-in, sign-out, first-time setup and changing one's own password
remain excluded: they are session/credential operations, not business operations. A catalog test enforces the invariant
(every non-session operation is executable and offered to the planner). Fixed-flow dead ends ("transfers are not
supported", "create the account first", "no warehouse") now hand over to the planner, which creates what is missing.

D35. **Passwords never pass through the model.** Password fields accept only `"$generate"`. The approval binds that
placeholder; the operation runner generates a 16-character password at execution, sends it to the app and returns it
in a result type that marshals without it. The widget shows it once in a "temporary password" card; it is not stored in
the task, the plan, the transcript or the audit log and never reaches the model (tested).

D36. **Missing product operations are added to the product.** Settling invoices with an existing receipt/payment did
not exist in the app (Phase-12 finding A). It was implemented in the application (backend service + route + UI + Postgres
integration test) on top of the existing allocation service, then exposed automatically through the generated catalog.
It is recorded as a product feature, not as part of the add-on, and survives removal of the add-on.

D37. **Upload operations** (attachments, company logo, attendance import) take `upload_id` = a file the user attached
in the assistant; the runner reads it from the user's own file store and sends it as multipart. capmap adds the field.

D38. **capmap "required" markers are scoped to the validated input type** (the function's typed parameter and the
types nested in it). Package-wide collection had marked e.g. `allocations` as required for creating a receipt because
a different function validates it for the new allocation operation; it also removed earlier false positives.

D39. **Active document context.** Every record the assistant creates, changes or reads back one-by-one is recorded
from the structured API result (never the reply text) as the conversation's active artifact, with its exact type
(`sales_quotation`, `treasury_receipt`, `journal_entry` …), plus a short deduplicated stack. «این / همین / PDF این /
رسیدش / تبدیلش کن» resolve against it before anything is searched: an explicit number the conversation knows, then a
document type named in the message, then the active document. Both the interpreter and the planner see it. «رسید» alone
is not read as a payment receipt.

D40. **Policy is data-driven; a mutation is not by itself a reason to ask.** Plans are decided per step by
`policy.DecidePlan` from the operation's contract: approval-class or never-auto steps → approval (an explicit
"directly" cannot skip it); a duplicate, a request read from a document, an unrequested change or "ask me this time" →
confirm; an explicit "directly" → direct; every step *routine* → direct; otherwise (money moves / entries post) → confirm.
*Routine* (`catalog.RoutineChange`) = confirmation-class create/update, not critical, not a financial document, and
this call writes no entry. Whether a call writes entries comes from the handler's **call graph** (capmap `posting.go`:
reaching the ledger's write API, with the request fields that guard it — `opening_balance`, `post`), replacing URL
guesses that were wrong (expense categories marked as posting, bank-account opening balances not). Employees are master
data, no longer approval-only by URL; D15 (payroll, checks, invoice/journal drafts) is unchanged. The user's typed
«مستقیم ثبت کن» / «روی صفحه نشانم بده» are now carried into the task (they were detected but dropped). After a preview,
«تأیید و ثبت» executes the SAME stored payload (hash-checked) instead of re-planning; a party that now exists (saved by
hand) is not created again. Party creation checks the app's own search by normalized name and phone first.

D41. **Real forms are discovered from the frontend source, not curated.** capmap `uiforms.go` finds, per function
component, the fields (`<Field label={t(key)}>` + the setter that writes the request field, with the control kind), the
dialog (title key), and how it opens — linked through the state variable that renders it: a create button (`setX('new')`),
a variant button (`setX({ kind: 'bank' })` → discriminator), the record's table row (`onRowClick`) or an edit button on the
record's page; page forms use their `/new` or `/:id/edit` route; senders are found through hooks too (`useCheckForm`).
Tags equal i18n keys, so the source key is the DOM tag. Variable API prefixes (`api: \`/${routeSide}/${routeKind}\``) are
expanded through their literal union types instead of being wildcards, which had marked ~120 operations as "used by the
UI" through the invoice pages. 44 create/update operations have a discovered form (was 2, hand-written).

D42. **Capability audit is generated and enforced.** capmap writes `CAPABILITY_AUDIT.md` and `internal/catalog/audit.json`:
per operation the app endpoint, AI tool, permission, policy, posting behaviour, UI-preview capability and one status
(FULLY_SUPPORTED, PARTIALLY_SUPPORTED, MISSING_UI_CAPABILITY, APPLICATION_LIMITATION, UNSAFE_TO_AUTOMATE), plus a
module × capability matrix. `cmd/capmap` tests fail when the committed catalog/audit is stale; `catalog` tests fail on any
new UI gap not listed with a reason in `knownUIGaps`. PDFs: journal entries and stock documents added (the app prints
them too).

D43. **Two execution modes, decided in one place: HUMAN_UI (default) and DIRECT.** `service/internal/execmode` resolves
*how* an operation runs: the user's words in the request («مستقیم», «با موس», "directly", "with the mouse") → an explicit
choice already attached to the operation (card button, earlier turn) → the user's persistent preference
(`exec_preference`, Settings) → the default, **HUMAN_UI**. An API existing never selects DIRECT.
- **HUMAN_UI** = the assistant operates the real screen: the widget's single engine (`widget/src/agent/engine.ts`,
  `executeHumanUIAction`) resolves the rendered element (data-ai tags → accessibility → vision), scrolls it into view, moves the
  visible pointer onto it (re-resolving after scrolls/re-renders), performs the real click/typing/selection and observes the
  result. The pointer is intrinsic to the engine, so a new module with a normal tagged UI gets it with no extra code. It performs
  real mutations (the form is submitted); afterwards the server reads the record back through the application and reports only
  verified state. Plans are built generically from the generated form map (`Operation.UI`, plus curated line grids in
  `catalog/ui.go`) by `agent/humanui.go`; the same builder serves UI_PREVIEW (stop before submit), which stays a separate action.
- **DIRECT** = the allowlisted application operations (services, never SQL), no pointer.
- Confirmation, approval, permission and mode are independent: a card is decided by `policy`, the mode only says how the approved
  work runs. HUMAN_UI **never falls back to DIRECT silently**: if the screen cannot express the work (a field without a control,
  no screen path yet, an element not found) the task stops with `ui_failed`, nothing is changed and the user may explicitly press
  «اجرای مستقیم» / say «مستقیم انجام بده» (audited as `mode_changed`).
- Also on screen by default: opening a document (list → row), print/PDF (the app's own button), cancel/undo (the app's own
  button + confirmation dialog), menu navigation. Known gaps (reported, not hidden): actions only reachable from a row's ⋯ menu,
  stock documents, workflow actions other than cancel, forms whose fields the generator cannot map.

D44. **An attached file is the operation's reference document, never just input.** Every file the user sends with a request is,
after the record is registered, attached as the ORIGINAL file to that same record in the application (`attach_document` →
`POST /attachments`), verified by listing the record's attachments, and returned in the result with the app's own download link
(`Result.Files`, shown on the result card and in the record's Attachments panel). Extraction only pre-fills the operation.
Applies to every path: built-in registrations, capability plans (direct and on-screen) — a plan's files go to the first document
it registered, or to the counterparty/employee only when it registered nothing else; a plan step `attachments.create` for the
same file is not repeated. All ten attachable record types of the app are supported (invoice, treasury transaction, expense,
journal entry, stock document, check, budget, payroll period, party, employee); a file without an accepted extension (pasted
"blob") gets the extension of its real type. Attaching is idempotent per task, file and record. A plan step that attaches the
user's own file (`attachments.create` with its upload) is carried out by the system in every mode — never shown as a form, since a
browser cannot pre-fill a file input — and is verified and reported the same way. A file sent in a turn that
registers nothing (a bare document, a question about it) is inherited by the next requests of the conversation (at most two
more turns) so "ثبتش کن" attaches it; when the registered record type takes no attachments, the user is told and the file stays
in the assistant's store (`/ai-api/files/{id}`). Every attach is audited with the file's hash.

D45. **Record text is written in the interface language.** With a Persian interface every description, note, item line, plan
summary and step purpose the assistant composes is Persian; with English, English. Enum values and identifiers stay as defined, and
words the user or the source document actually wrote (a Latin brand such as «NordVPN», a Persian name) are kept. Enforced, not only
prompted (`internal/textlang`): the planner gets a LANGUAGE CHECK feedback naming the foreign words once; if it insists, known words
are translated deterministically (glossary). On the built-in registration path the resolver localizes the model's description and
drops text that still has invented foreign words. Both prompts state the rule.

D46. **A HUMAN_UI form the user saves by hand is completed like one the assistant saved.** When "show me on screen" leaves the
filled form to the user, the server records the newest existing record id (baseline) and sends the form's save steps as `watch`.
The widget runs none of them; it watches the user's own save (the form closes / the page leaves it) and reports `user_saved`/`closed`.
The server then looks for the new record through the application (above the baseline; matched by the entered values, or the only new
record when the user edited them), reads it back, audits it as the user's act, makes it the conversation's document and attaches the
pending source file (D44). A chat message while the form is open runs the same check first, so «تأیید و ثبت» after a manual save
never registers twice. A form closed without saving, or a report with no new record, claims nothing and keeps the operation open.
Multi-step plans continue with the next step's form.

## Reliability audit (2026-10-08)

D47. **The business domain is decided before any tool.** `internal/intent` classifies the user's own words (never document text)
into the domain hierarchy (accounting · payroll · crm · configuration · documents · memory) by phrase-in-context rules — a word such
as «هزینه», «پرداخت», «حقوق» never decides alone («کمک هزینه سفر را به اجزای حقوق اضافه کن» is payroll configuration; «هزینه سفر را
ثبت کن» an accounting event). The decision (domain, intent, operation, target, required information, reason) is persisted on the
task (`State.Intent`) and audited. A request the rules place outside accounting never enters the accounting-event workflow (who
paid? from which account?) even when the model says "register" (`domain_override`), and a plan for it may not write to money
modules (`intent.CompatibleWrite`, checked in `validatePlan`). The interpreter prompt states the same rule.

D48. **Deterministic skills for exactly-classified requests.** Salary components (create or update, company structure) and an
employee's own component amount are built by code from the application's current state (`internal/agent/skills.go`): existing
components are read; an exact name is updated, a similar one is ASKED («حق سرپرستی» vs «حق سرپرستی ارشد» — never assumed), a new one
gets a free code and the next sequence; the employee record is read in full and only its override list changes. Only genuinely
missing values are asked (name, amount, which of several employees). The steps go through the normal plan approval, idempotency and
verification. An employee's pay (base salary, component amounts) is never executed without approval (`setsPay`).

D49. **Memory is a separate instruction with an explicit scope.** «به خاطر بسپار / برای دفعات بعد» is detected apart from the task.
When the task changes company configuration (payroll), that change is what applies next time — nothing is stored as personal memory,
and the user is told so. A memory-only message accepts the pending learning offers of the conversation's last task (user preference)
or says honestly that there is nothing to remember. Business configuration is never stored as a personal note.

D50. **Schema: only unconditional checks make a field required.** The generator (`cmd/capmap`) treats checks inside if/switch
branches, closures and disjunctions (`method != "x" || field != ""`) as conditional, and attributes an untyped helper's checks to the
types of its callers only. Before, 17 operations had impossible contracts (salary components required brackets/insurance_share/
quantity_field/effective_from; every check transition required party, reason and account; expenses required both party and cash
account), so valid plans were rejected round after round. The app's real per-variant rules are in the curated `variants` table.

D51. **A write is never sent twice by accident; success is never assumed.** Before a plan step writes, an in-flight marker records
the highest existing id and the time. An uncertain outcome (transport error, timeout, 5xx, restart mid-call) is reconciled through
the application — a record above the baseline carrying the step's values is adopted (`step_recovered`); none → "checked: not saved,
nothing duplicated", and only the user's «دوباره امتحان کن» re-offers the same step (no re-planning), which reconciles again first.
After a write the record is read back (by id, or from its list) and every sent value compared; a mismatch is shown and the step is
not marked verified. Resuming a screen task after a reload or closed tab first looks for a record saved before the interruption
(`/tasks/{id}/resume-ui`); a stale screen report continues from the persisted plan instead of "مرحله را گم کردم".

D52. **Never-automatic documents stay never-automatic in plans.** Checks, journal entries, sales/purchase documents and their
posting, and stock documents need a person's approval also as plan steps and also after «مستقیم ثبت کن» (`policy.NeverAutoTools`;
the legacy `NeverAuto` map was keyed by action names that plan steps never matched).

D53. **A new complete request is not an answer.** While a card waits for approval or an answer, a message the domain layer
recognises as a complete new operation closes the open one — the user is told it was not registered — and starts the new one
(before, it was merged into the open operation and lost).

D54. **Traceability.** `GET /ai-api/tasks/{id}/trace`: decision, interpretation, policy, plan (steps with tool, args, status,
verification, mismatches), the ordered audit timeline with offsets, and counters (planner rounds, plan rejections, tool failures,
recoveries, repairs, retries). Plan rejections are audited with their reason; the same rejection three times stops with that reason.
Provider errors are named precisely (credit/key limit exhausted ≠ unavailable).

D55. **Dates in plans are checked deterministically.** Every `civil.Date` field of a plan step (line items too) is read: a
YYYY-MM-DD / YYYY/MM/DD with a year 1300–1499 is Jalali and is converted to Gregorian; any other implausible date is refused before
the user is asked to confirm. (A hire date «۱۴۰۵/۰۷/۰۱» had been stored as the Gregorian year 1405 and "verified", because the
read-back matched what was sent.)

D56. **Invoices from documents are built from their printed lines.** The reader extracts the invoice's lines, subtotal, discount,
VAT amount and rate and total, and checks the document's own arithmetic (lines → subtotal → − discount + VAT → total = amount); an
inconsistency marks the amount low-confidence and is shown on the card. A purchase invoice is then built by code: printed lines,
the document discount spread over the lines, the VAT rate on each line, the supplier resolved (exact; duplicates and similar names
asked; a missing one created in the same plan), and the stored invoice's total compared with the lines' total after the write. An
unreadable reader answer is read again, then without the lines; the user is never shown "something went wrong".

D57. **Transfers between own accounts are deterministic.** Source and destination are taken from the user's words («از X به Y»,
or a generic «صندوق/بانک» when exactly one account of that kind exists); an unclear one is asked with the accounts as options.

D58. **What a model writes is checked, not trusted.** Amounts: a number followed by «تومان/تومانی» may only appear ×10 when the
base is rial (a transfer of «۵ میلیون تومان» had been stored as 5,000,000 rial); a zero/missing amount is asked, never planned.
Answers: every amount ≥ 1,000 in a written answer must be one the software returned (a balance of −120,000,000 had been answered
as «۱,۲۰۰,۰۰۰,۰۰۰-»); otherwise the planner is told, and an answer that stays ungrounded is replaced by an honest refusal.
Entities: a plan whose party id belongs to several same-named parties stops with a question; a new party whose name equals an
existing one without its legal form («پارس نوآور» / «شرکت پارس نوآور») is a duplicate. Unknown fields are dropped and shown on
the card. A chat answer that claims an action is followed by "nothing was registered"; "chat" with an explicitly requested complete
event is routed to registration.

D59. **One execution capability registry; no executor, no plan.** `agent/capabilities.go` derives, per operation, from the
real executors: *direct* = the tool registry has a runner; *ui* = the screen builder can build its steps (`uiCapability`, the
same predicate `buildStepPlan` uses); *verify* = read-by-id, its list, or (delete) the record is gone. The planner sees only
operations with an executor **and** a verification path (`plannerTools`), `validatePlan` refuses any other tool, and
`selectExecutor` picks the executor for the requested mode **before** the plan is shown: a missing screen executor selects
the direct one and the card says so (an explicit request for the screen always gets a card); no executor at all ends the
task as `NO_EXECUTOR` before anything is presented. The execution state (requested/selected mode, executor, attempts,
failure code) is persisted on the task (`State.Exec`) and shown in the trace. A bare «مستقیم اجرا کن» changes only the
mode of the open operation — same task, same steps, same approval; it never re-plans and never bypasses approval.
`Spec.SupportsDirect` (which meant "explicit direct may skip confirmation" but was read as "a direct executor exists", so
every approval-class operation looked unexecutable) is renamed `ExplicitDirectSkipsConfirm`. CI:
`TestCapabilityInvariantPlannerImpliesExecutor`, `TestUICapabilityMatchesScreenExecutor`. Report: `EXECUTOR_CAPABILITY_AUDIT.md`.

D60. **Targets are grounded, deletes are verified.** A step that changes or deletes an existing record must use an id the
application returned in this work (a read, a completed step, or a conversation document with the record's name); the
record is read before the card and its real name shown. A name in the id slot is resolved through the application (one
match → its id; several → ask; none → say so). Multi-word searches the application cannot match (it searches first OR last
name) are repeated word by word. A delete is verified only when the record reads back 404 (or deactivated) or is gone from
its list; a delete whose record is already gone is `already_completed`, never sent again. Step ids and `$ref`s the executor
cannot resolve are refused (a literal `"$c1.id"` had reached the application). Live finding behind this decision: the
production request «کارمند زهرا معتضد رو حذف کن» was planned as `employees.delete {id: 1}` without a lookup — employee 1 was
someone else — and the delete was reported "verified".

D61. **Tell/show/list is a read.** `intent.RequestAction` classifies the action: «بگو / نمایش بده / لیست کن» = read,
«کجاست / کجا / چطور پیدا کنم» = navigation, change verbs = neither. A read that the model labelled chat, navigation or "open the
screen" is answered by reading the application; a planner answer that only points at a place in the UI with no read is sent
back. «قواعد حقوق» is `GET /payroll/rules` (tax tables, insurance rules, settings) and «قواعد ثبت حسابداری حقوق» the payroll keys
of the account mappings: both are deterministic reads that report exactly what the application returned, including "nothing
is configured". Statutory rule values (brackets, rates, ceilings) are never invented: the user is asked.

D62. **Fresh-context recovery, deterministic and at most once.** `internal/ctxconflict` decides from persisted state only:
the same request already stopped in this conversation, or a self-contained new request while the current task is
stopped/blocked/executing on screen → NEW_CONTEXT; another domain or entity → a new task in the same conversation; answers,
retries, mode instructions and messages that refer back («بهش», «همین», «حقوقش») → CONTINUE. On NEW_CONTEXT the request is
re-submitted byte-for-byte (same upload ids, same `request_id`) to a new conversation; the old task is closed (or left alone
when its outcome is unknown) and nothing is executed again. One automatic reset per request (also counted per request text).
Every reset records the causing state, the affected decision and whether it is an architecture bug;
`GET /ai-api/requests/{id}/trace` shows the lifecycle and `GET /ai-api/metrics/context` the rates and repeated patterns. The
root bug behind the reported case is also fixed: a stopped/blocked task (and a plan on an approval card) no longer absorbs
a different complete request. The audit masker is JSON-aware: it had rewritten large amounts as `******0000`, breaking traces.

D63. **Postconditions, not responses.** After every plan step the application is read again: the record acted on is still the
approved one (else nothing is sent), every sent value reads back, a workflow action produced the status the application's
own state machine defines (curated from `payroll/periods.go`, `budgets/service.go`, `checks/machine.go`, post/cancel/close/
reopen), an update left every field the user did not ask to change unchanged, and the record's list shows no other record
changed or vanished and — for a create — exactly one new record. Updates are full-replacement PUTs in this application, so a
step's body is merged over the record's current values (live finding: a rename set an employee's base salary to 0 and was
"verified"). Screen-mode cancels verify the cancelled status; screen creates flag a second matching record. A summary for
anything unverified says so instead of «انجام شد». Per-step results are persisted (`steps[].checks`) and audited.

D64. **Values come from the application or the user.** Model-written plans: every `*_id` in the body and in line items must
be a `$ref` or an id the matching resource returned in this work (or the company configuration); otherwise the system reads
the record itself and accepts it only when its name is in the user's words, it is the only record of its kind, or it is the
application's default — else the plan is sent back. `upload_id` must be the user's own file. Identifiers (check number, Sayadi,
national id, IBAN, card/account number), check type, a check's due date, rates, percents, quantities, discounts, days and
hours must be stated by the user or returned by the application, else the user is asked. Names written into a plan follow
the user's spelling; search terms are corrected to the user's spelling; kind words («کارمند») are not part of a name; an
impossible date in the user's words is pointed out before planning. A check's bank is taken from the bank the user named.

D65. **Uncertain outcomes are reconciled, three-way, for every kind of mutation.** After a lost response or timeout (and
before resending a step an earlier attempt may have sent) the application decides: done (adopt, never resend), verified not
applied (resend allowed), or unknown (stop, never resent, the user is told exactly that). Deletes: gone/present; updates:
values applied or not; workflow actions: target status reached / unchanged since before; creates: matching record in a
completely read list. "Not saved" is only said after it was verified. A workflow whose record already shows the target
status is `already_completed`. The legacy receipt/payment/expense path reconciles the same way.

D66. **A5 root cause.** «۳ میلیون تومان به ایرانیکارت پرداخت کردیم» with two suppliers named ایرانیکارت: the expense workflow
dropped the payee the user named (a bank-paid expense needs no party), so neither was asked nor linked. The payee of a
cash/bank expense is now resolved like any party: one exact match is linked (and verified on read-back), several are asked,
an unregistered vendor asks nothing. Also: the catalog marked `treasury_account_id` required for every payroll period action
(they share an input type; only `pay` validates it) — corrected.
