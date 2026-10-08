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
