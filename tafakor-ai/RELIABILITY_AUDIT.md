# Tafakor AI — reliability audit (2026-10-08)

Scope: the in-app AI assistant (`tafakor-ai/service`, `tafakor-ai/widget`) on the throwaway test stack (`tpai-test`, demo
company), driven through the real widget in real Chrome with the real model (`google/gemini-2.5-flash-lite` via OpenRouter —
the default model; chosen for the live runs because the key had $0.27 left with the configured `claude-sonnet-5.5`).
The core accounting app (backend, frontend) was not changed.

## A. What was tested

| Kind | How | Scenarios / runs |
|---|---|---|
| Real browser, user's Chrome (Claude in Chrome) | typed into the real widget, approved the real card, watched the virtual pointer fill the real payroll form; reloaded mid-task; closed the tab mid-task | payroll component (exact regression), UI failure, reload/resume, tab closed during execution |
| Real browser, Playwright + Chrome (`widget/scripts/e2e-audit.mjs`) | every turn typed into the widget, questions answered and cards approved by clicking, results checked by reading the app's API | 33 scenarios: payroll 7, memory 2, accounting 5, CRM 2, checks 4, configuration 1, reports 2, documents 12 (+ repeat ×2) |
| Document uploads | 16 generated fixtures (`test/fixtures`, `generate.py`): clean PDF, Persian digits, English digits, VAT+discount+6 lines, scanned (noise/blur/skew), low quality JPEG, rotated 90°, stamp over the total (ambiguous OCR), missing date/total, POS receipt, Parsian card-to-card receipt, payment receipt PDF, Sayad check | 12 per matrix run, uploaded through the widget's file input |
| Failure injection, live (`test/fault_scenarios.py` + `fault_proxy.py`) | proxy between AI service and app: lost response after commit, app down, HTTP 500 on update, expired session, two concurrent requests in one conversation | 7 checks |
| Stress (`test/stress_payroll.py`) | 20 salary-component + 20 employee-compensation workflows; Persian/English digits, `,`/`٬`, «میلیون», تومان/ریال/none, 4 phrasings | 40 workflows |
| Automated | Go (18 packages, `-race`), widget (vitest) | 202 Go test functions, 12 widget tests |

Realistic data (`test/seed_audit.py`): employees زهرا معتضد / محمد رضایی / علی رضایی (valid national ids), suppliers شرکت پارس نوآور,
فروشگاه لوازم اداری آریا, تعمیرگاه خودرو صادقی, customer شرکت داده‌پردازان سپهر, «بانک پارسیان — جاری ۸۸۱۲», «صندوق دفتر مرکزی»; the
demo company's existing duplicates (two «ایرانیکارت», two «علی رضایی») were kept on purpose as ambiguity cases.

## B. Failures found (each reproduced live, fixed, covered by a test, re-run)

| # | Reproduction (live) | Root cause (class) | Fix | Regression test |
|---|---|---|---|---|
| 1 | «به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان» → 8 planner rounds → "نتوانستم راه مطمئنی…" | tool schema: generator marked conditional checks required (17 ops: salary components, every check transition, expenses, stock docs…) | `cmd/capmap` counts only unconditional checks; helper checks scoped to caller type; curated variants (D50) | `TestSalaryComponentConditionalRequired`, `TestBranchLocalChecksAreNotRequired`, `TestRequiredOnlyFromUnconditionalChecks` |
| 2 | «برای کارمند قاسم مرادی کمک هزینه سفر ۳ میلیون ثبت کن» → «این هزینه را چه کسی پرداخت کرد؟» | intent: keyword-driven event type, no domain | domain layer before tools; payroll never enters the accounting-event flow; domain guard on plans (D47) | `internal/intent/*_test.go` (6), `TestSalaryComponentRequestIsPayrollConfiguration`, `TestEmployeeCompensationCreatesComponentAndOverride` |
| 3 | an employee existed with code `"$generate"` | tool schema / hallucinated value (password placeholder in a code) | placeholder only in password fields | `TestPlanGuardsDomainAndPlaceholders` |
| 4 | screen agent: «field payroll.exemption not found» (create, then update) | UI automation: default 0 typed into a control the form hides | default-value steps optional; read-back still compares | `TestScreenPlanMarksDefaultValuesOptional`, `TestScreenUpdatePlanMarksDefaultValuesOptional`, widget `engine.test.ts` |
| 5 | resume after reload/closed tab replays the whole form | duplicate execution risk | `/tasks/{id}/resume-ui` looks for a record saved before the interruption first | `TestResumeAfterInterruptionFindsTheSavedRecord`, `TestResumeWithoutSaveReplaysTheScreenPlan` |
| 6 | stale screen report → «مرحله را گم کردم» | state/recovery | continue from the persisted plan | `TestStaleScreenReportContinuesFromState` |
| 7 | (injected) lost response / app down / 500 | verification only checked existence; no outcome reconciliation | in-flight marker, reconciliation, value read-back, verified "not saved", safe retry (D51) | `TestLostResponseIsReconciledNotDuplicated`, `TestFailedWriteIsReportedAndRetriedSafely`, `TestReadBackMismatchIsNotReportedAsSuccess` + live 7/7 |
| 8 | plan rejections invisible; generic stop | observability | `plan_rejected` audit, specific stop after 3 identical rejections, `/trace` (D54) | `TestRepeatedPlanRejectionIsTracedAndExplained`, `TestTraceShowsDecisionPlanStepsAndVerification` |
| 9 | key limit → "AI service unavailable"; upstream rate limit not retried; «مشکلی پیش آمد» | error handling | `ErrNoCredit`, rate-limit mapping + backoff, specific messages | `TestKeyLimitIsReportedAsNoCredit`, `TestUpstreamRateLimitIsRetried`, `TestCreditInFlight402IsRetriedThenSucceeds` |
| 10 | «حق سرپرستی زرقم» answered "«حق سرپرستی» already set — nothing changed" | entity resolution (substring match) | similar names are asked | `TestSimilarComponentNameIsAskedNotAssumed` |
| 11 | an employee's pay override ran without a card | approval boundary | pay changes never auto (`setsPay`) | `TestEmployeePayChangeAlwaysNeedsApproval` |
| 12 | checks / journal entries / invoices executable with «مستقیم ثبت کن» in plans | approval boundary (NeverAuto keyed by legacy names) | `policy.NeverAutoTools` (D52) | `TestNeverAutoToolsNeedApprovalEvenWhenDirect`, `TestNeverAutoToolsExistInTheCatalog` |
| 13 | second complete request during an approval merged into the first and lost | state/race | explicit supersede with a message (D53) | `TestNewRequestDuringApprovalSupersedesExplicitly` + live concurrency check |
| 14 | «برای محمد رضایی (کارمند) …» fell through to the model (8/40 stress failures) | intent: punctuation | punctuation-insensitive matching | `TestPunctuationDoesNotHideContext` |
| 15 | hire date «۱۴۰۵/۰۷/۰۱» stored as year 1405 AD, "verified" | planner date / verification | Jalali→Gregorian guard (D55) | `TestJalaliDatesInPlansAreConverted` |
| 16 | purchase invoices from documents: same malformed JSON 7×, VAT lumped into one line | planning / extraction | itemised extraction + arithmetic check + deterministic invoice (D56) | `TestPurchaseInvoiceFromDocumentIsBuiltFromItsLines`, `TestDuplicateSupplierIsAskedForDocumentInvoices`, `TestInvoiceStructureAndArithmetic`, `TestFallbackPromptOmitsInvoiceBlock` |
| 17 | «۵ میلیون تومان … منتقل کردیم» → "باشه، ثبت می‌کنم" and nothing registered | hallucinated success (chat intent) | chat+requested event → registration; chat claim flagged | `TestChatWithACompleteRequestedEventIsARegistration`, `TestChatActionClaimIsDetected` |
| 18 | same transfer later stored as 5,000,000 rial | amount guard accepted raw + ×10 | unit-aware guard; deterministic transfer (D57, D58) | `TestTomanAmountMustBeConverted`, `TestTransferBetweenNamedAccountsIsDeterministic` |
| 19 | «منظورتان «شرکت داده‌پردازان سپهر» است؟» for the exact typed name | needless clarification | user's exact words decide | `TestPartyNamedExactlyInTheUsersWordsIsNotAsked` |
| 20 | check without amount offered with amount 0 | invented value | zero/missing amount is asked | `TestZeroAmountIsAskedNotPlanned` |
| 21 | same cash box created twice, no warning | duplicate protection (parties only) | generic duplicate rules (accounts, employees, products, checks, components) | `TestDuplicateCashBoxIsWarnedBeforeApproval` |
| 22 | two «ایرانیکارت»: plan silently used one | entity resolution | same-named parties in a plan are asked | `TestTwinCounterpartiesInAPlanAreAsked` |
| 23 | unknown field «reference» on a check → stop | schema loop | unknown fields dropped and shown | `TestUnknownFieldIsDroppedAndShown` |
| 24 | balance −120,000,000 answered as «۱,۲۰۰,۰۰۰,۰۰۰-» | hallucinated number | answer numbers must be in read results | `TestAnswerNumbersMustComeFromTheData` |
| 25 | «پارس نوآور» created next to «شرکت پارس نوآور» automatically | duplicate protection | legal-form-insensitive comparison | `TestPartyDuplicateIgnoresLegalForm` |
| 26 | Persian free-text answers: LTR numeric input, options hidden | frontend | `dir=auto`, numeric only for amounts, options shown | covered live (P4 answered by clicking an option) — no unit test |

## C. Architecture before

```
user text ─► LLM interpret (intent + event) ─┬─ register → resolver (accounting events only) ─► approval ─► API ─► "verified" = record exists
                                              ├─ operate/query → LLM planner (≤8 rounds; rejections silent) ─► approval ─► API
                                              └─ chat → LLM free text (could promise actions)
schema: generated, conditional fields marked required · amounts: raw or ×10 · no outcome reconciliation · resume = replay
```

## D. Architecture after

```
user text ─► DOMAIN DECISION (internal/intent: rules on the user's words, persisted + audited)
              ├─ exact skill (payroll component · employee pay · transfer · invoice from document) ─► typed payload from app state
              └─ LLM interpret / planner ─► GUARDS: domain↔tool, schema (unconditional), dates, unit-aware amounts,
                                              entities (twins, similar, legal form), duplicates, placeholders, grounded answers
          ─► POLICY (never-auto docs, pay changes) ─► approval card ─► EXECUTION per step:
              in-flight marker → API → (uncertain? reconcile: adopt | "not saved") → read-back value comparison → audit
          ─► TRACE /ai-api/tasks/{id}/trace (decision, plan, steps, timings, recoveries)   ─► resume/retry = reconcile first
```

## E. Remaining known issues (not hidden)

1. **Expense vs supplier payment is model-dependent.** «۳ میلیون تومان به ایرانیکارت پرداخت کردیم» is booked as an expense («سایر»)
   from the bank account rather than a payment against the supplier's account; so the twin-«ایرانیکارت» question never comes (A5 fails
   3/3 runs). No deterministic rule was added because paying a gateway can legitimately be an expense.
2. **Upstream rate limits** of the small model still fail some runs (honestly: "busy, nothing registered"): 3 of 66 in the repeat run.
3. **Human-like UI mode needs a visible tab.** In the user's Chrome the tab was reported `hidden`; Chrome throttles timers (~1 step
   per minute) and screenshots time out. Headless Chrome runs at normal speed. A task left `ui_running` stays so until the user resumes.
4. **Read-back verification proves what was written, not that the plan was right.** Semantic errors are caught only by the guards
   (dates, amounts, entities, answers); others would pass verification.
5. **«۳ میلیون» without a unit is taken in the base currency (rial)**; many users mean toman. The card states both units.
6. **New salary components default to taxable + insurable**; travel allowances may be exempt — the card says so, the user must change it.
7. **Invoices from documents use description-only lines** (no product link), so stock is not updated for goods.
8. Test data in the throwaway company contains duplicates created **before** the fixes (two «شرکت آرمان تجارت کیش» — one by the
   harness overriding the warning, two «صندوق شعبه کرج») and many stress components.
9. All changes are uncommitted in the working tree (together with the earlier uncommitted work on this branch).

## F. Test results (not rounded, from the runs' `results.json`)

| Run | Passed | Failed | Notes |
|---|---|---|---|
| E2E matrix BEFORE (first run with credit) | 18 / 33 | 15 | |
| E2E after round 1 of fixes | 25 / 33 | 8 | |
| E2E after round 2 | 25 / 33 | 8 | several were correct duplicate warnings on repeat runs (harness updated) |
| E2E AFTER | **31 / 33** | 2 | A5 (issue E1), C2 → fixed afterwards (#25) |
| E2E AFTER, repeat ×2 | **61 / 66** | 5 | A5 ×2 (E1), 3 × upstream rate limit (E2) |
| Live failure injection | 7 / 7 | 0 | |
| Stress (40 workflows) | 32 → **40 / 40** | 0 | 8 failures fixed (#14) |
| Go (18 packages, `-race`) | all | 0 | 202 test functions |
| Widget (vitest) + typecheck | 12 / 12 | 0 | |

Known limitations: 3 (E1, E2, E3). Model cost of a full 33-scenario matrix: ≈ $0.05.

## G. Files changed (tafakor-ai only)

New: `service/internal/intent/` · `service/internal/agent/{skills,docskill,transfer,reliability,record_duplicates}.go` ·
`test/{fault_proxy,fault_scenarios,stress_payroll,seed_audit}.py` · `test/fixtures/{generate.py,invoices,receipts,checks}` ·
`widget/scripts/e2e-audit.mjs` · `RELIABILITY_AUDIT.md`.
Changed: `service/cmd/capmap/main.go` (+ regenerated `internal/catalog/capabilities.json`, `CAPABILITIES.md`) ·
`internal/catalog/spec.go` · `internal/agent/{agent,planner,humanui,conversation,flow,amounts,party_duplicates,routes,state,ui}.go` ·
`internal/classify/{interpret,resolve}.go` · `internal/extract/extract.go` · `internal/policy/policy.go` ·
`internal/provider/provider.go` · `widget/src/{app.ts,agent/engine.ts}` · `test/{docker-compose.test.yml,run-host-ai.sh}` ·
`DECISIONS.md` (D47–D58) · `README.md`.

## H. Tests added

Go: `internal/intent/intent_test.go` (6) · `internal/agent/skills_test.go` (30) · `internal/agent/docskill_test.go` (2) ·
`catalog/spec_test.go` (+2) · `cmd/capmap/main_test.go` (+1) · `classify/resolve_test.go` (+2) · `extract/extract_test.go` (+2) ·
`policy/plan_test.go` (+2) · `provider/provider_test.go` (+2). Widget: `engine.test.ts` (+1). Live: the E2E matrix, fault and stress scripts.

## I. Remaining risks (accounting)

- Anything still produced by the model planner (multi-step operations without a skill) depends on the guards; a plausible but wrong
  account, category or party that is internally consistent would pass and is visible only on the approval card.
- Reconciliation adopts a record created after the baseline with the same values; two identical genuine operations within seconds
  of an uncertain failure would be treated as one (the user is told which record was adopted, in the trace and the result).
- Unit ambiguity (toman/rial without a unit) and VAT treatment of document lines remain human-review items on the card.
- Learning and memory: only explicit instructions are stored; business configuration is never stored as personal memory.
