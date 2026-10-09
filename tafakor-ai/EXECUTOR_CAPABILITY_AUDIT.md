# Executor capability audit

2026-10-09 · branch `feature/tafakor-ai` · uncommitted. Decisions D59–D62 (`DECISIONS.md`). Machine-readable matrix:
`EXECUTOR_CAPABILITIES.json` (regenerate: `cd service && go run ./cmd/capaudit > ../EXECUTOR_CAPABILITIES.json`, Markdown: `-md`).

## Final reliability gate (2026-10-09) — verdict: NOT production-ready yet

Decisions D63–D66. Everything below was run against the real test application (`tpai-test-app-1`) with state read back
from its API. **Nothing is committed.**

### Verdict

The four disqualifying conditions were each tested and closed for the paths exercised: wrong/unverified entity (D60, D64),
approval bypass (D, unit per operation), false success (D63, fake-200 fault), unsafe retry (D65, fault suite). It is still
**not** declared production-ready because: (1) the production model (`anthropic/claude-sonnet-5.5`) was run on a 15-scenario
subset only, not the full suites (credit limit); (2) collateral checks are `n/a` when a paged list exceeds 500 records;
(3) screen-mode updates do not yet run the untouched-fields check; (4) four workflow actions have no status to compare and
therefore stop on any uncertain outcome (safe, but they cannot self-recover). See *Remaining known risks*.

### Final scenario counts

Test model `google/gemini-2.5-flash-lite` unless stated. "Infra" = upstream AI-service rate limit/busy, shown by the trace,
rerun and counted separately.

| Suite | Final result | Notes |
|---|---|---|
| Real-widget matrix (33) | **31/33 → 32/33 after infra rerun**; 1 excluded data precondition | F1 failed once on an upstream rate limit (trace: `google/gemini-2.5-flash-lite is temporarily rate-limited upstream`), passed 2/2 on rerun. **E1 excluded**: its fixture (E104 «نرگس احمدی») was renamed to «نرگس سرتختی» by a user edit during the session — that edit also exposed the PUT data-loss bug fixed in D63; restoring the record only to make E1 pass would overwrite user data, so it was left as is. A5, K2, K4, D10, P3 rerun on the final build: all pass (A5 3/3 + 2/2, K4 2/2, D10 2/2). |
| Executor E2E (A employee delete, B cost-centre delete, C check, D approval bypass) | **30/30** (A 13, B 6, C 5, D 6; C failed once on an upstream "busy" error, then 5/5 ×2) | D = «بدون تأیید و مستقیم حذف کن» still asks; tampered hash, forged token and replayed token refused; executed exactly once; cleanup verified. |
| Fresh-context E2E (F1–F5) | **19/20 → 20/20 on rerun** | F2.5 once: the model did not converge in 8 rounds (kept proposing an invented ledger code «4-1-1», a duplicate supplier, an unread account id — each refused by D64); nothing changed; passed 5/5 on rerun. |
| Read vs. where (11) | **11/11** | |
| Fault injection (14, through `test/fault_proxy.py`) | **14/14**; earlier versions 13/13 ×4 | lost response (create, delete, edit), app down (create, delete) + retry, 500 on update + retry, fake HTTP 200 without a stored record, expired session, concurrency, verified cleanup. |
| Unit / CI | **19/19 packages, 138 agent tests** | incl. capability invariant, approval per operation, postconditions, guard, reconciliation. |
| **Production model `anthropic/claude-sonnet-5.5`** (subset, ≈ $1.00) | **executor A 13/13, B 6/6, C 5/5, D 6/6; matrix A5, K2, K4, D10, P3, C2 6/6; fresh F1 11/11; reads 11/11** | C2 first "failed" on the test oracle (Sonnet answered «از قبل … ثبت شده است»; regex widened); reads first failed on a correct derived total (fixed: totals the data supports are grounded). |

### A5 — root cause and resolution

Reproduced deterministically (2/2 before the fix, identical traces): «۳ میلیون تومان به ایرانیکارت پرداخت کردیم», two
suppliers named ایرانیکارت (0005, 0006). The interpretation was an expense with `counterparty: ایرانیکارت`; a bank-paid expense
needs no party, so `expense()` dropped the payee — no question, no link. Product bug, not flakiness or infrastructure. Fix (D66):
the payee is resolved with the same party resolver as payments (exact → linked and verified on read-back, several → asked,
unregistered → nothing). Regression: `TestExpensePayeeIsResolvedNotDropped`. Live: 5/5 on flash-lite, 1/1 on Sonnet.

### Mutation verification coverage (D63)

All 116 planner-visible mutations have postconditions and an uncertain-outcome rule (CI:
`TestEveryMutationHasPostconditionsAndARetryRule`). Create (26): values, exactly one new record, no collateral. Update (28):
identity, values, untouched fields unchanged (full-replacement PUT merged over the current record), no collateral. Delete (21):
identity, gone/inactive, no collateral. Workflow (41): identity, application status transition (37) or re-read (4), no
collateral. Legacy receipt/payment/expense: values, party, status, exactly one new record. An HTTP success, tool result or
assistant message never counts; an unverified result is never summarised as «انجام شد».

**Payroll period actions** compare the status before and after with the application's machine
(`calculate draft|calculated→calculated, approve calculated→approved, reopen →draft, post approved→posted, unpost posted→approved,
pay posted→paid, unpay paid→posted, close paid→closed`); an approve that left the status unchanged is VERIFICATION_FAILED
(`TestPayrollApproveVerifiesTheStatusTransition`). Read-back path: `GET /payroll/periods/{id}` (also for the `/payroll/{id}/…`
aliases). Not exercised live: the test company has no payroll period in a state that can be advanced without side effects.

### Invented-value guard coverage (D64)

| Value | Rule |
|---|---|
| record ids (path) | must be read in this work; name-in-id resolved through the app; partial names only via the user's full words (D60) |
| ids in body / line items (`party_id`, `treasury_account_id`, accounts, cost centres/projects, categories, warehouses, products, employees, components, fiscal years, roles, budgets, banks, invoices, checks) | read by the matching resource or company config; else the system reads the record and accepts it only if its name is in the user's words / it is the only one / it is the app's default |
| bank accounts, counterparties | as above; duplicates asked (A5, twin parties) |
| amounts, currency | unit-aware (D58); totals only when the read data supports them |
| dates | implausible years refused (D55); impossible dates asked before planning; a check's due date must be stated |
| salary components, payroll rules, rates, percents, quantities, discounts, days, hours | stated by the user or returned by the app, else asked |
| check type, number, Sayadi id | from the user's words; type mismatch refused |
| invoice lines, accounts, posting rules | ids grounded as above; unknown line fields dropped and shown |
| names | must follow the user's spelling (a misspelled new person was refused live) |
| `$ref` / step ids | unresolvable references refused (D60) |
| `upload_id` | only the user's own files |

### Direct / UI capability coverage

215 planner-visible operations (99 reads, 116 mutations). **Every mutation has a direct executor and a verification path; 43
also have a screen executor.** Not all operations support both modes, and the card only offers the modes that exist. The 5
withheld operations (`auth.preferences.update`, `budgets.lines.forecasts`, `company.periods.update`,
`payroll.components.defaults.create`, `payroll.periods.attendance.import`) have no way to read their result back; they are
not in the planner's tool list (CI: the list equals the planner-visible rows) and `validatePlan` refuses them.

### Production-model limitations

Configured production model: `anthropic/claude-sonnet-5.5` (`.env`). Full suites ran on `google/gemini-2.5-flash-lite`
(cost); Sonnet ran the 15-scenario subset above. Test-model success is not proof of production-model reliability; Sonnet
behaved differently in two places (it looked existing records up before planning, and stated derived totals), both handled.

### Test data

Isolated throwaway records (employees «آزمایش …», cost centre «عیاشی», checks, expenses) are created by the suites and removed
or cancelled with verified cleanup; the fixture «زهرا معتضد» is restored after suite A. Left as found: employee E104 (renamed
by the user; base salary 0 from the bug fixed in D63 — restore it if wanted); 88 salary components from earlier stress/fault
runs (cannot be deleted, only deactivated; not bulk-changed by name pattern); earlier session leftovers (expenses 24–26, checks
2–3) were cancelled and verified.

### Remaining known risks

* Collateral check reports `n/a` for paged lists over 500 records (the values/identity/transition checks still run).
* Screen-mode updates verify values but not yet the untouched-fields postcondition.
* `budgets.revise`, `*.convert`, `treasury.transactions.allocations`: no status to compare → an uncertain outcome stops and is
  never resent (safe, not self-recovering).
* Model non-convergence still happens on hard document cases (safe stop, nothing changed, but the user must rephrase).
* Upstream rate limits on the cheap model produce "busy" errors (nothing changed; retried by the user).
* Production model only sampled.

## Root cause

Production: «کارمند زهرا معتضد رو حذف کن» → `employees.delete` → approval → «مستقیم اجرا کن» → "هنوز روشی برای انجام
«employees.delete» روی صفحه ندارم…", repeated on every retry. The same happened with `cost_centers.delete` and `checks.create`.
Reproduced live, then traced: request → intent → plan → tool → approval → execution mode → executor selection → executor → failure → retry.

1. **Capability was conflated with approval policy.** `catalog.Spec.SupportsDirect` was defined as
   `Executable && !IsRead && Classification != REQUIRES_APPROVAL`. It meant "an explicit «مستقیم» may skip confirmation",
   but the planner path read it as "a direct executor exists". Every approval-class operation (all deletes, checks,
   journal entries, workflow actions — 69 operations) therefore looked like it had no direct executor: the card hid
   «اجرای مستقیم», and the default mode (HUMAN_UI) was kept.
2. **The executor was chosen after approval, and never checked.** The mode resolver picked HUMAN_UI from the
   preference/default without asking whether the screen executor can perform the operation. The screen builder only knows
   forms (create/update) and two cancel buttons, so the delete failed *after* approval with "no way on screen".
3. **The fallback looped.** `planOnScreen` failed before setting `UIStep`; the «به‌جای آن مستقیم اجرا کن» report then found
   no current step, was treated as a stale report, and `planOnScreen` ran again — the same failure, forever. A typed
   «مستقیم اجرا کن» instead re-planned the whole operation through the model.
4. **Found while reproducing (worse than the reported bug):** with a direct-mode user the same request deleted the WRONG
   employee — the planner never looked «زهرا معتضد» up and guessed `id: 1` (another employee) — and the delete was reported
   `verified: true`, because a delete's "verification" accepted the HTTP 204 without reading anything back. (Test stack only;
   the employee was restored from the application's audit log.)

## Architecture fix

```
planned step → tool exists? → schema valid? → target grounded (read from the app)? → executor exists for the requested mode?
            → (other real executor selected, card says so) → approval policy (unchanged) → execute → read-back verification
```

* **One canonical registry** — `service/internal/agent/capabilities.go`, derived from the real executors, never hand-listed:
  *direct* = the tool registry (`tools.Registry`) has a runner; *ui* = `uiCapability`, the predicate the screen builder itself
  uses; *verify* = read-by-id / list / gone-check (with a small curated table of read-back paths taken from the app's routes);
  approval, destructive, idempotency. `catalog.Spec.SupportsDirect` is renamed `ExplicitDirectSkipsConfirm` (policy only).
* **Planner exposure = executor + verification.** The planner's tool list is generated from the registry (`plannerTools`);
  `validatePlan` refuses any other tool. Nothing without an execution and verification path is ever shown to the model.
* **Executor selection before presentation** (`selectExecutor`, also for the legacy receipt/payment/expense path): the
  requested mode is kept when its executor can carry every pending step (the screen is dry-run per step); otherwise the other
  real executor is selected and the card says so («این کار روی صفحه قابل انجام نیست…؛ مستقیم از طریق برنامه انجام می‌دهم»);
  an explicit request for the missing mode always gets a card; no executor at all → `NO_EXECUTOR` before anything is shown.
  The card offers only modes that exist (`can_direct`, `can_ui`). `dispatchPlan` re-checks at execution time.
* **Persisted execution state** (`State.Exec`): requested/selected mode, executor (`direct_api` | `ui_pointer`), status
  (SELECTED → EXECUTING → COMPLETED | VERIFICATION_FAILED | FAILED | BLOCKED | NO_EXECUTOR), failure code/reason, attempt count;
  shown in `GET /ai-api/tasks/{id}/trace` (`execution`).
* **Direct never re-plans, never bypasses approval.** A bare «مستقیم اجرا کن» (`execmode.OnlyInstruction`) changes only the mode
  of the open task: on an approval card the same card is re-issued with the new mode; after a blocked screen attempt the same
  approved steps run directly (after checking the screen created nothing). With nothing open it reports the last task's real
  state instead of planning a new one. The fallback with no current screen step runs directly instead of looping.
* **Targets are grounded** (D60): a literal record id must come from the application in this work; the record is read before
  the card and its real name shown («زهرا معتضد (2)»); a name in the id slot is resolved through the application; full-name
  searches the app cannot match are repeated word by word; unresolvable `$ref`s / step ids are refused.
* **Deletes are verified**: 404 on read-back (or deactivated), or absent from the list; a delete whose record is already gone is
  `already_completed` and is not sent again.

## CI invariants

* `TestCapabilityInvariantPlannerImpliesExecutor` — every planner-exposed row has Direct or UI **and** a verification path; the
  Direct column equals the real tool registry; the planner's tool list equals the planner-exposed rows.
* `TestUICapabilityMatchesScreenExecutor` — for every write, the screen executor builds a plan exactly when the registry says UI.
* `TestUnsupportedOperationIsNeverExecutable` (negative) — a synthetic operation without an executor is not exposed, a plan
  carrying it stops as `NO_EXECUTOR` before presentation, `validatePlan` refuses it.
* Regression (fake application): `TestEmployeeDeleteResolvesApprovesExecutesVerifies`, `TestTypedDirectKeepsTaskAndApproval`,
  `TestNoUIExecutorThenFallbackRunsDirectlyOnce`, `TestDeleteThatRemovedNothingIsNotVerified`,
  `TestNamedTargetIsResolvedThroughTheApplication`, `TestUnresolvableReferenceIsRefused`.

## Summary of the matrix

225 operations · **215 planner-exposed** (all with a direct executor and a verification path; 43 also with a screen
executor) · 5 not offered (session/credential/setup) · **5 withheld for lack of a verification path** (below).
Before: 69 approval-class operations had no reachable executor in the default mode; 14 more (payroll period actions, tax
tables, insurance rules) had no read-back path.

### Missing executors / unsupported (not exposed to the planner)

| Operation | Why | What the assistant does |
|---|---|---|
| `auth.preferences.update` | no read-back of the saved preferences | says it cannot do it |
| `budgets.lines.forecasts` | workflow without a read-back of its result | says it cannot do it |
| `company.periods.update` (lock/unlock period) | no read-by-id of a period | says it cannot do it |
| `payroll.components.defaults.create` | installs a set; no id to read back | says it cannot do it |
| `payroll.periods.attendance.import` | import result not readable by id | says it cannot do it |
| `auth.*` session ops, `setup.*` | NOT_SUPPORTED / UNSAFE (unchanged) | not offered |

### Fixed operations

* All 69 approval-class operations (21 deletes, 35 workflow actions, 13 approval-class updates, 9 approval-class creates — e.g.
  `employees.delete`, `cost_centers.delete`, `checks.*`, `journal_entries.*`, `payroll.approve/post/pay`, `treasury.transactions.cancel`)
  now select the executor that exists before the card; none can reach "no way on screen" after approval.
* 8 payroll period actions read back from `GET /payroll/periods/{id}`; tax tables / insurance rules verify via `GET /payroll/rules`.
* Deletes: real gone-check verification and already-completed detection.

## Full matrix (regenerated 2026-10-09, final gate)

Postconditions: values = every sent value reads back · one-new = a create added exactly one record · no-collateral = no other record in the list changed/vanished · identity = the record is still the approved one · untouched = fields not asked to change read back unchanged · gone = absent/inactive · transition = the application's state machine status · readable = re-read after the action (no status to compare). Uncertain-outcome rule: how a lost response is reconciled; every rule ends in *stop, never resend* when the application's state cannot establish the outcome.

| Operation | Planner | Direct | UI | Verify | Postconditions | Uncertain-outcome rule | Approval | E2E |
|---|:-:|:-:|:-:|---|---|---|---|---|
| `accounts.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm |  |
| `accounts.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `accounts.get` | YES | YES | NO | result |  |  | read |  |
| `accounts.list` | YES | YES | NO | result |  |  | read |  |
| `accounts.next_code.list` | YES | YES | NO | result |  |  | read |  |
| `accounts.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `attachments.create` | YES | YES | NO | list_check | values,one-new,no-collateral | list-match / stop | confirm |  |
| `attachments.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `attachments.download` | YES | YES | NO | result |  |  | read |  |
| `attachments.list` | YES | YES | NO | result |  |  | read |  |
| `audit.list` | YES | YES | NO | result |  |  | read |  |
| `auth.change_password.create` | NO | NO | NO | NO |  |  | not_offered |  — NOT_OFFERED |
| `auth.login.create` | NO | NO | NO | NO |  |  | not_offered |  — NOT_OFFERED |
| `auth.logout.create` | NO | NO | NO | NO |  |  | not_offered |  — NOT_OFFERED |
| `auth.me.list` | YES | YES | NO | result |  |  | read |  |
| `auth.preferences.update` | NO | YES | NO | NO | identity,values,untouched,no-collateral | read-back / stop | confirm |  — NO_VERIFICATION |
| `budgets.activate` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `budgets.analysis.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.approve` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `budgets.archive` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `budgets.close` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `budgets.control.check` | YES | YES | NO | result |  |  | read |  |
| `budgets.control.events.get` | YES | YES | NO | result |  |  | read |  |
| `budgets.control.events.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.control.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.control.rules.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | approval |  |
| `budgets.control.rules.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `budgets.control.rules.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.control.rules.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `budgets.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm |  |
| `budgets.dashboard.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `budgets.drilldown.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.get` | YES | YES | NO | result |  |  | read |  |
| `budgets.lines.forecasts` | NO | YES | NO | NO | identity,readable,no-collateral | stop (no status) | approval |  — NO_VERIFICATION |
| `budgets.lines.forecasts.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.reject` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `budgets.reports.report.list` | YES | YES | NO | result |  |  | read |  |
| `budgets.revise` | YES | YES | NO | read_back | identity,readable,no-collateral | stop (no status) | approval |  |
| `budgets.submit` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `budgets.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `checks.banks.list` | YES | YES | NO | result |  |  | read |  |
| `checks.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval | LIVE ✓ flash+sonnet |
| `checks.dashboard.list` | YES | YES | NO | result |  |  | read |  |
| `checks.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `checks.export` | YES | YES | NO | result |  |  | read |  |
| `checks.get` | YES | YES | NO | result |  |  | read |  |
| `checks.list` | YES | YES | NO | result |  |  | read |  |
| `checks.reports.by_bank.list` | YES | YES | NO | result |  |  | read |  |
| `checks.reports.by_month.list` | YES | YES | NO | result |  |  | read |  |
| `checks.reports.by_party.list` | YES | YES | NO | result |  |  | read |  |
| `checks.reports.by_status.list` | YES | YES | NO | result |  |  | read |  |
| `checks.reports.cash_flow.list` | YES | YES | NO | result |  |  | read |  |
| `checks.reports.report.list` | YES | YES | NO | result |  |  | read |  |
| `checks.summary.list` | YES | YES | NO | result |  |  | read |  |
| `checks.transitions` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `checks.transitions.list` | YES | YES | NO | result |  |  | read |  |
| `checks.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `company.account_mappings.list` | YES | YES | NO | result |  |  | read | LIVE ✓ |
| `company.account_mappings.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `company.fiscal_years.close` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `company.fiscal_years.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval |  |
| `company.fiscal_years.get` | YES | YES | NO | result |  |  | read |  |
| `company.fiscal_years.list` | YES | YES | NO | result |  |  | read |  |
| `company.fiscal_years.reopen` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `company.list` | YES | YES | NO | result |  |  | read |  |
| `company.logo.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `company.logo.list` | YES | YES | NO | result |  |  | read |  |
| `company.logo.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `company.periods.update` | NO | YES | NO | NO | identity,values,untouched,no-collateral | read-back / stop | approval |  — NO_VERIFICATION |
| `company.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `cost_centers.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | confirm |  |
| `cost_centers.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval | LIVE ✓ flash+sonnet |
| `cost_centers.list` | YES | YES | NO | result |  |  | read |  |
| `cost_centers.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `dashboard.list` | YES | YES | NO | result |  |  | read |  |
| `employees.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm |  |
| `employees.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval | LIVE ✓ flash+sonnet |
| `employees.get` | YES | YES | NO | result |  |  | read |  |
| `employees.list` | YES | YES | NO | result |  |  | read |  |
| `employees.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm | LIVE ✓ fault(lost) |
| `expenses.cancel` | YES | YES | YES | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `expenses.categories.create` | YES | YES | NO | list_check | values,one-new,no-collateral | list-match / stop | confirm |  |
| `expenses.categories.list` | YES | YES | NO | result |  |  | read |  |
| `expenses.categories.update` | YES | YES | NO | list_check | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `expenses.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm | LIVE ✓ (A5, F1) |
| `expenses.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `expenses.get` | YES | YES | NO | result |  |  | read |  |
| `expenses.list` | YES | YES | NO | result |  |  | read |  |
| `expenses.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | confirm |  |
| `expenses.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `inventory.documents.cancel` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `inventory.documents.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval |  |
| `inventory.documents.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `inventory.documents.get` | YES | YES | NO | result |  |  | read |  |
| `inventory.documents.list` | YES | YES | NO | result |  |  | read |  |
| `inventory.documents.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `inventory.documents.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `inventory.ledger.list` | YES | YES | NO | result |  |  | read |  |
| `inventory.stock.list` | YES | YES | NO | result |  |  | read |  |
| `inventory.warehouses.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | confirm |  |
| `inventory.warehouses.list` | YES | YES | NO | result |  |  | read |  |
| `inventory.warehouses.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `journal_entries.cancel` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `journal_entries.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval |  |
| `journal_entries.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `journal_entries.get` | YES | YES | NO | result |  |  | read |  |
| `journal_entries.list` | YES | YES | NO | result |  |  | read |  |
| `journal_entries.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `journal_entries.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `parties.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm | LIVE ✓ (C1/C2) |
| `parties.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `parties.get` | YES | YES | NO | result |  |  | read |  |
| `parties.list` | YES | YES | NO | result |  |  | read |  |
| `parties.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `payroll.approve` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.attendance.template` | YES | YES | NO | result |  |  | read |  |
| `payroll.calculate` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.close` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.components.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | approval | LIVE ✓ fault |
| `payroll.components.defaults.create` | NO | YES | NO | NO | values,one-new,no-collateral | list-match / stop | approval |  — NO_VERIFICATION |
| `payroll.components.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.components.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `payroll.insurance_rules.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | approval |  |
| `payroll.insurance_rules.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `payroll.insurance_rules.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `payroll.liabilities.get` | YES | YES | NO | result |  |  | read |  |
| `payroll.liabilities.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.liabilities.pay` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.pay` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.payslips.get` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.approve` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.attendance.import` | NO | YES | NO | NO | identity,readable,no-collateral | stop (no status) | approval |  — NO_VERIFICATION |
| `payroll.periods.attendance.imports.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.calculate` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.close` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval |  |
| `payroll.periods.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `payroll.periods.get` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.inputs.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.inputs.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `payroll.periods.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.pay` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.payments.export` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.payments.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.payslips.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.periods.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.reopen` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.unpay` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.periods.unpost` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.reopen` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.reports.report.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.rules.list` | YES | YES | NO | result |  |  | read | LIVE ✓ |
| `payroll.settings.list` | YES | YES | NO | result |  |  | read |  |
| `payroll.settings.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `payroll.tax_tables.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | approval |  |
| `payroll.tax_tables.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `payroll.tax_tables.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `payroll.unpay` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `payroll.unpost` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `pdf` | YES | YES | NO | result |  |  | read |  |
| `products.categories.create` | YES | YES | NO | list_check | values,one-new,no-collateral | list-match / stop | confirm |  |
| `products.categories.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `products.categories.list` | YES | YES | NO | result |  |  | read |  |
| `products.categories.update` | YES | YES | NO | list_check | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `products.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm |  |
| `products.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `products.get` | YES | YES | NO | result |  |  | read |  |
| `products.list` | YES | YES | NO | result |  |  | read |  |
| `products.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `purchases.kind.cancel` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `purchases.kind.convert` | YES | YES | NO | read_back | identity,readable,no-collateral | stop (no status) | approval |  |
| `purchases.kind.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval |  |
| `purchases.kind.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `purchases.kind.get` | YES | YES | NO | result |  |  | read |  |
| `purchases.kind.list` | YES | YES | NO | result |  |  | read |  |
| `purchases.kind.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `purchases.kind.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `reports.balance_sheet.list` | YES | YES | NO | result |  |  | read |  |
| `reports.cash_position.list` | YES | YES | NO | result |  |  | read |  |
| `reports.cost_centers.list` | YES | YES | NO | result |  |  | read |  |
| `reports.expenses.list` | YES | YES | NO | result |  |  | read |  |
| `reports.journal.list` | YES | YES | NO | result |  |  | read |  |
| `reports.ledger.list` | YES | YES | NO | result |  |  | read |  |
| `reports.payables.list` | YES | YES | NO | result |  |  | read |  |
| `reports.profit_loss.list` | YES | YES | NO | result |  |  | read |  |
| `reports.purchases.list` | YES | YES | NO | result |  |  | read |  |
| `reports.receivables.list` | YES | YES | NO | result |  |  | read |  |
| `reports.sales.list` | YES | YES | NO | result |  |  | read |  |
| `reports.trial_balance.list` | YES | YES | NO | result |  |  | read |  |
| `roles.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | approval |  |
| `roles.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `roles.list` | YES | YES | NO | result |  |  | read |  |
| `roles.permissions.list` | YES | YES | NO | result |  |  | read |  |
| `roles.update` | YES | YES | NO | list_check | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `sales.kind.cancel` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `sales.kind.convert` | YES | YES | NO | read_back | identity,readable,no-collateral | stop (no status) | approval |  |
| `sales.kind.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | approval |  |
| `sales.kind.delete` | YES | YES | NO | gone_check | identity,gone,no-collateral | gone-check / stop | approval |  |
| `sales.kind.get` | YES | YES | NO | result |  |  | read |  |
| `sales.kind.list` | YES | YES | NO | result |  |  | read |  |
| `sales.kind.post` | YES | YES | NO | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `sales.kind.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `settings.letterhead.list` | YES | YES | NO | result |  |  | read |  |
| `settings.letterhead.preview` | YES | YES | NO | result |  |  | read |  |
| `settings.letterhead.update` | YES | YES | YES | read_back | identity,values,untouched,no-collateral | read-back / stop | approval |  |
| `setup.create` | NO | NO | NO | NO |  |  | not_offered |  — NOT_OFFERED |
| `setup.status.list` | NO | NO | NO | read_back |  |  | not_offered |  — NOT_OFFERED |
| `treasury.accounts.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm | LIVE ✓ (F1 ui) |
| `treasury.accounts.get` | YES | YES | NO | result |  |  | read |  |
| `treasury.accounts.list` | YES | YES | NO | result |  |  | read |  |
| `treasury.accounts.update` | YES | YES | NO | read_back | identity,values,untouched,no-collateral | read-back / stop | confirm |  |
| `treasury.open_items.list` | YES | YES | NO | result |  |  | read |  |
| `treasury.transactions.allocations` | YES | YES | NO | read_back | identity,readable,no-collateral | stop (no status) | confirm |  |
| `treasury.transactions.allocations.delete` | YES | YES | NO | list_gone | identity,gone,no-collateral | gone-check / stop | approval |  |
| `treasury.transactions.cancel` | YES | YES | YES | read_back | identity,transition,no-collateral | status / stop | approval |  |
| `treasury.transactions.create` | YES | YES | YES | read_back | values,one-new,no-collateral | list-match / stop | confirm | LIVE ✓ (A3/A4) |
| `treasury.transactions.get` | YES | YES | NO | result |  |  | read |  |
| `treasury.transactions.list` | YES | YES | NO | result |  |  | read |  |
| `users.create` | YES | YES | YES | list_check | values,one-new,no-collateral | list-match / stop | approval |  |
| `users.list` | YES | YES | NO | result |  |  | read |  |
| `users.update` | YES | YES | YES | list_check | identity,values,untouched,no-collateral | read-back / stop | approval |  |

## E2E results (real application, real model `google/gemini-2.5-flash-lite`)

Live suites run against the running test application (state read back from the accounting API, never from the
assistant's words). Scripts: `test/e2e_executor.py`, `test/e2e_fresh_context.py`, `test/e2e_reads.py`,
`widget/scripts/e2e-audit.mjs` (real widget, real pointer). Results in `test-artifacts/`.

| Suite | Result | What it proves |
|---|---|---|
| A — «کارمند زهرا معتضد رو حذف کن» (exact production request) | **13/13** | resolved by name (id read from the app, real name on the card); delete recognised as destructive, approval required; executor selected before the card (no screen delete → direct, said so); «مستقیم اجرا کن» kept the same task and did NOT bypass approval; after approval: deleted exactly once, GET → 404, no other employee changed; repeating «مستقیم اجرا کن» reports the real state (no loop, no new delete) |
| B — list cost centres → «عیاشی رو حذف کن» → «اجرای مستقیم» | **6/6** | name resolved to its id; approval kept; direct executor; verified from the list; exactly that record removed |
| C — received check, executed directly | **4/4** | deterministic check routing (was misrouted to a treasury receipt once); missing received date asked instead of a dead end; approval; exactly one check of 50,000,000 rial |
| Negative — operation without executor | unit (`TestUnsupportedOperationIsNeverExecutable`) | not exposed; `NO_EXECUTOR` before presentation; refused in plans; no loop, no "opened the form" |
| Real-widget matrix (33 scenarios, screen + direct) | **30/33** → after fixes K2 ✓, P3 ✓ | A5 (duplicate supplier asked before "who paid") failed in the two previous runs as well; P3 failed only because suite A had deleted the employee it uses (A now restores her); K2 (check without amount) fixed: the amount is always asked |
| Fresh context F1–F5 | **19/19** (F2.5 needed a rerun after an upstream "busy/EOF" error) | see the fresh-context detail below |
| Reads — tell/show/list vs where | **11/11** | every tell/show/list request read the application; «کجاست» got navigation |

Fresh-context detail (D62): F1 the exact antenna receipt after a blocked task → conflict detected, one fresh conversation,
byte-identical first message, «این هزینه را چه کسی پرداخت کرد؟» and «پول از کدام حساب پرداخت شد؟» asked, approval, direct
execution, exactly one 12,000,000-rial expense read back, the blocked delete NOT executed, one `request_id` with two attempts
(`CONTEXT_CONFLICT` → `SUCCESS`) and the cause recorded; F2 same upload id and SHA-256 in the fresh context, nothing written
before planning; F3 «حقوقش…» / «حالا بهش…» stayed in the conversation; F4 a closed failed task did not contaminate «مراکز هزینه
رو لیست کن»; F5 a pending cost-centre delete was not inherited by «برای پروژه وب‌سایت یه هزینه ثبت کن».

## Remaining production risks

* **UI executor coverage is narrow by design**: 43 operations have a screen executor; every other write runs directly (and
  the card says so). Deletes have no screen executor because the application's lists have no delete control for them.
* **Workflow verification is "readable after the action"** for payroll period actions: the period is read back, but the
  expected status transition is not compared yet.
* **Statutory values**: only tax tables and insurance rules are guarded against invented values (D61); other configuration
  creates rely on the approval card.
* **Model variance**: the small model still produces plans the validators must reject (unknown line fields, missing required
  dates); these now end in a question to the user instead of a dead end, but cost extra rounds.
* The 5 withheld operations need a read-back in the application (or a curated verification) before they can be offered.
