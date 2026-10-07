# حسابداری تفکر پردازان — Tafakor Pardazan Accounting

A web-based double-entry accounting system for Tafakor Pardazan (تفکر پردازان): chart of accounts, journal entries, sales and purchases, inventory, cash and bank, expenses, cost centers and financial reports. The interface is bilingual (Persian/RTL and English/LTR) with light and dark themes and the Jalali calendar.

```
Browser ──► Go REST API (serves the React app too) ──► PostgreSQL
```

No message queues, caches or extra services: two containers (`app`, `db`) and two volumes (`pgdata`, `attachments`).

## Contents
- [Requirements](#requirements)
- [Installation](#installation)
- [Environment variables](#environment-variables)
- [Docker commands](#docker-commands)
- [Database migrations](#database-migrations)
- [Development workflow](#development-workflow)
- [Production build](#production-build)
- [Backup](#backup)
- [Restore](#restore)
- [Troubleshooting](#troubleshooting)
- [Architecture](#architecture)
- [Accounting model](#accounting-model)

## Requirements
- Docker Engine 24+ with the Docker Compose plugin (`docker compose`)
- For local development only: Go 1.26+, Node.js 22+

## Installation
```sh
git clone <repository> tpaccounting
cd tpaccounting
cp .env.example .env        # then edit DB_PASSWORD and SESSION_SECRET
docker compose up -d
```
Open http://localhost:8085 (or the `APP_PORT` you chose). On first start you see the **setup wizard**: enter the company details, choose the calendar and currency, and create the administrator. By default the standard Iranian chart of accounts, posting rules, cost centers, expense categories and a warehouse are created, plus a fiscal year for the current year.

> The setup wizard is available only while no company exists. Complete it right after the first start, before exposing the port to a network.

Generate secrets with:
```sh
openssl rand -hex 16   # DB_PASSWORD
openssl rand -hex 32   # SESSION_SECRET
```

## Environment variables
All configuration is read from `.env` (see `.env.example`).

| Variable | Default | Purpose |
|---|---|---|
| `APP_PORT` | `8085` | HTTP port inside the container and on the host |
| `APP_BIND` | `127.0.0.1` | Host interface the port binds to (`0.0.0.0` to expose) |
| `APP_ENV` | `production` | Informational |
| `TZ` | `Asia/Tehran` | Time zone used for "today" |
| `DB_NAME`, `DB_USER`, `DB_PASSWORD` | – | PostgreSQL database credentials (password required) |
| `DB_HOST`, `DB_PORT`, `DB_SSLMODE` | `db`, `5432`, `disable` | Set by compose; or use `DATABASE_URL` instead |
| `SESSION_SECRET` | – | ≥32 chars; key for hashing session tokens (required) |
| `SESSION_TTL_HOURS` | `12` | Sliding session lifetime |
| `COOKIE_SECURE` | `false` | `true` when served over HTTPS |
| `TRUST_PROXY` | `false` | Trust `X-Forwarded-For` (only behind your own reverse proxy) |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated origins allowed cross-origin (normally none) |
| `LOGIN_RATE_LIMIT_PER_MINUTE` | `10` | Login attempts per IP per minute |
| `MAX_UPLOAD_MB` | `20` | Attachment size limit |
| `STORAGE_DIR` | `/data/attachments` | Attachment storage directory (a Docker volume) |

## Docker commands
```sh
docker compose up -d              # start (builds the image the first time)
docker compose up -d --build      # rebuild after pulling changes
docker compose ps                 # status (both services report "healthy")
docker compose logs -f app        # application logs (JSON)
docker compose down               # stop; data volumes are kept
docker compose down -v            # stop and DELETE all data (irreversible)
docker compose exec db psql -U tpaccounting tpaccounting   # SQL console
```

## Database migrations
Migrations are plain SQL files in `backend/migrations/` (`0001_core.sql`, …), embedded in the binary and applied automatically, in order and once each, every time the app starts (tracked in `schema_migrations`, serialised with an advisory lock). To apply them without starting the server:
```sh
docker compose run --rm app migrate
```
To change the schema, add a new numbered file such as `0004_something.sql`; never edit a migration that has already run.

## Development workflow
Run PostgreSQL in Docker and the API and frontend on the host:
```sh
docker compose up -d db
# expose the db port for local tools (one-off):
docker compose stop db && docker compose run -d --service-ports -p 127.0.0.1:5432:5432 db

cd backend
export DATABASE_URL="postgres://tpaccounting:<DB_PASSWORD>@localhost:5432/tpaccounting?sslmode=disable"
export SESSION_SECRET="dev-secret-dev-secret-dev-secret-123" APP_PORT=8085 STORAGE_DIR=./.data STATIC_DIR=../frontend/dist
go run ./cmd/server

cd ../frontend
npm install
npm run dev            # http://localhost:5173, proxies /api to http://localhost:8085 (override with API_URL)
```

Tests:
```sh
cd backend
go test ./...                                         # unit tests
TEST_DATABASE_URL="postgres://user:pass@localhost:5432/postgres?sslmode=disable" go test ./...
                                                      # + integration tests (creates and drops temporary databases)
cd ../frontend
npm test               # number/money formatting, calculation and calendar tests
npm run typecheck
```
The integration tests cover check lifecycles and their accounting, payroll calculation/posting/payment with cost-center allocation, budget versions, actuals and warning/soft/hard budget control, the full sales/purchase workflow, journal rules (balancing, posting, cancellation by reversal, duplicate numbers), moving-average inventory and cancellation, payment allocation and release, fiscal-year closing/reopening, the chart-of-accounts rules, authentication, CSRF and permissions.

## Production build
`docker compose build` produces one image (`tpaccounting:latest`): the React app is built with Vite, the Go server is compiled statically, and both are copied into a small Alpine image running as a non-root user. For production:
- put a TLS-terminating reverse proxy (nginx, Caddy, Traefik) in front and set `COOKIE_SECURE=true`, `TRUST_PROXY=true`;
- keep `APP_BIND=127.0.0.1` when the proxy runs on the same host;
- schedule backups (below).

## Backup
Accounting data lives in PostgreSQL (`pgdata` volume); attachments and the logo live in the `attachments` volume.

Database (custom-format dump, recommended):
```sh
scripts/backup.sh
# equivalent to:
docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner' > backups/tpaccounting.dump
```
Attachments:
```sh
docker run --rm -v tpaccounting_attachments:/data -v "$PWD/backups":/b alpine tar czf /b/attachments.tgz -C /data .
```
`pg_dump` takes a consistent snapshot, so backups can run while the system is in use. Copy the `backups/` folder off the server.

## Restore
**Restoring replaces all current data.**
```sh
scripts/restore.sh backups/tpaccounting-YYYYMMDD-HHMMSS.dump
# equivalent to:
docker compose stop app
docker compose exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction' < backups/tpaccounting.dump
docker compose start app
```
Attachments:
```sh
docker run --rm -v tpaccounting_attachments:/data -v "$PWD/backups":/b alpine sh -c 'rm -rf /data/* && tar xzf /b/attachments.tgz -C /data'
```

## Troubleshooting
| Symptom | Fix |
|---|---|
| `Set DB_PASSWORD in .env` / `SESSION_SECRET must be set` | Create `.env` from `.env.example` and fill in the secrets |
| Port already in use | Change `APP_PORT` in `.env`, then `docker compose up -d` |
| App restarts, logs say it cannot connect to the database | `docker compose logs db`; if you changed `DB_PASSWORD` after the first start, the database still has the old one — change it back or reset with `docker compose down -v` (deletes data) |
| Login says "Too many attempts" | Wait a minute (rate limit per IP) |
| Signed out right after login over plain HTTP | `COOKIE_SECURE=true` requires HTTPS; set it to `false` for plain HTTP |
| "No fiscal year covers this date" | Settings → Fiscal years → New fiscal year |
| "The posting rule … has no account" | Settings → Posting rules: assign an account to every rule |
| Need to start over completely | `docker compose down -v && docker compose up -d` (deletes all data) |

## Architecture
```
backend/
  cmd/server            entry point (migrate + serve, or `migrate`)
  migrations/           SQL migrations (embedded)
  internal/platform/    config, db (pool, transactions, numbering, filters), httpx (JSON, errors, paging),
                        migrate, storage (file storage abstraction), identity (principal, permission middleware), civil (dates)
  internal/money        pure accounting arithmetic (rounding, invoice lines, allocation, moving average)
  internal/jalali       Jalali calendar
  internal/ledger       journal entries — the double-entry core all modules post through
  internal/accounts, parties, costcenters, products, company, users, audit, attachments, setup, seed
  internal/invoices     sales & purchase quotations, invoices, returns + automatic accounting
  internal/treasury     cash/bank accounts, receipts, payments, transfers, invoice allocation
  internal/inventory    warehouses, stock documents, stock ledger (moving average)
  internal/expenses     expenses and categories
  internal/reports      journal, ledger, trial balance, P&L, balance sheet, cash, ageing, sales, purchases, expenses, cost centers, dashboard
  internal/checks       received & issued checks: state machine, posting per status change, dashboard, due calendar, reports
  internal/payroll      employees, configurable salary components, attendance inputs, payroll runs, payslips, payroll accounting, reports
  internal/budgets      versioned budgets, ledger-based actuals, variance/forecast analysis, budget control on posting
  internal/export       server-side report export (CSV, Excel, PDF) with company header, filters, totals and counts
  internal/platform/sheet  dependency-free CSV/XLSX reader and writer (exports, attendance import)
  internal/integration  end-to-end tests against PostgreSQL
frontend/src/
  styles/tokens.css     design tokens (brand palette → semantic colors, typography, spacing), light & dark
  i18n/                 fa.ts / en.ts dictionaries, provider, number/date formatting, error translation
  components/ui/        component library (Button, Field, ComboBox, DateField, DataTable, Dialog, Toast, …)
  components/pickers/   account / party / product / cost center / warehouse / treasury pickers
  features/<module>/    pages per module
  lib/                  number.ts (the single number/money formatting and parsing system), decimal math, Jalali calendar, hooks
```
New endpoints: `GET /checks/summary`, `GET /checks/export`; `GET /payroll/rules`, `POST|PUT|DELETE /payroll/tax-tables[/{id}]`, `POST|PUT|DELETE /payroll/insurance-rules[/{id}]`, `GET /payroll/liabilities[/{id}]`, `POST /payroll/liabilities/{id}/pay`, `GET /payroll/attendance/template`, `POST /payroll/periods/{id}/attendance/import` (multipart, `commit=true` to store), `GET /payroll/periods/{id}/attendance/imports`, `GET /payroll/periods/{id}/payments[/export]`; `GET|POST /budgets/lines/{id}/forecasts`, `GET /budgets/control/events/{id}`. New permissions: `payroll.settings`, `budgets.forecast`.

API: REST under `/api/v1` — `auth`, `setup`, `company`, `accounts`, `parties`, `cost-centers`, `journal-entries`, `products`, `inventory`, `sales/{invoices|quotations|returns}`, `purchases/{invoices|returns}`, `treasury` (cash and bank), `expenses`, `reports`, `attachments`, `users`, `roles`, `audit`, `dashboard`, `checks`, `employees`, `payroll`, `budgets`. Lists accept `page`, `page_size`, `sort`, `order`, `q` and module filters and return `{items,total,page,page_size}`. Errors are `{"error":{"code","message","fields","params"}}`; the frontend translates the code, so raw database messages never reach users.

Security: bcrypt password hashing; opaque session tokens stored HMAC-hashed in `sessions` (revocable logout, sign-out on password change or user deactivation); `HttpOnly`/`SameSite` cookies; CSRF double-submit token on every state-changing request; per-IP login rate limiting; module × action permissions (view, create, edit, post, cancel, delete, export) checked on the server; parameterised SQL only; strict security headers and CSP; uploads restricted by type and size and served with a sandboxing CSP.

## Accounting model
- **Chart of accounts**: group → general → subsidiary → detailed (levels 1–4). Children inherit the parent's type, codes extend the parent code, only leaf accounts (level ≥ 2) accept postings, and an account with postings cannot get children.
- **Journal entries**: draft → posted → cancelled. Posting requires debits = credits; this is enforced in Go, by a `CHECK` constraint and by a deferred trigger that re-sums the lines at commit. Posted entries cannot be edited or deleted (database triggers); cancelling creates a posted reversal entry.
- **Automatic accounting**: invoices, returns, receipts, payments, transfers, expenses and stock documents create their journal entries in the same database transaction as the document and its stock movements. The accounts used come from **Settings → Posting rules**, not from code.
  - Sales invoice: Dr receivable (party) / Cr sales (gross), Dr sales discounts, Cr output VAT; Dr COGS / Cr inventory at moving-average cost.
  - Purchase invoice: Dr inventory (stock items) or purchases (services), Dr input VAT / Cr payable (party).
  - Receipt: Dr cash/bank / Cr receivable; payment: Dr payable / Cr cash/bank; settlements are allocated to invoices (oldest first, or chosen by the user).
- **Fiscal years and periods**: postings must fall into an open fiscal year and an unlocked monthly period. Closing a year moves income and expenses to retained earnings; it can be reopened.
- **Reports** read posted journal lines directly; party balances are the sum of lines tagged with the party.
- **Cost centers** have a kind: cost center, department or project. Journal lines keep one `cost_center_id`, so departments and projects are reported, budgeted and allocated through the same dimension.
- **Checks** (`/checks`): received checks are booked when registered (Dr checks receivable / Cr customer) and move through custody → deposit (Dr checks in collection / Cr checks receivable) → clear (Dr bank / Cr checks in collection), or are cashed, endorsed to another party (Dr that party / Cr checks receivable), returned, lost or cancelled. Issued checks start as drafts; issuing books Dr supplier / Cr checks payable and clearing Dr checks payable / Cr bank. Returning, losing, cancelling or replacing a booked check cancels every live entry of the check through the ledger's reversal mechanism and releases its invoice settlements. Invalid transitions are rejected; every transition is recorded with its entries and audited. Duplicate Sayadi IDs and check numbers are rejected.
- **Payroll** (`/employees`, `/payroll`): salary components (earnings, deductions, employer contributions) are configured with a calculation method (fixed, % of base, daily/hourly wage × attendance, per child, % of insurable earnings, progressive tax brackets, monthly manual amounts). A payroll run goes draft → calculated → approved → posted → paid → closed. Posting books salary and employer expense split by each employee's cost-center/project allocation, against salaries, social security, payroll tax and loan/advance accounts; paying books Dr salaries payable / Cr cash or bank. Approved payslips are frozen by database triggers. Payroll has its own permissions and is not granted to accountants or viewers by default (a *Payroll Officer* role is provided).
- **Payroll rules are configuration, not code** (`/payroll/settings`, permission `payroll.settings`): income-tax tables (tax-free threshold, brackets, monthly or annual mode) and social-security rules (employee, employer and unemployment percentages, insurable ceiling, eligible components) each have an effective-from/to period; overlapping active rules are rejected. A calculation uses the rules in force at the payroll month's end and records them, with the day basis (actual calendar days, fixed 30-day month, or a configured divisor) and hourly divisor, in the payroll's *calculation basis*. Components can be fixed or variable, have effective dates and can use a formula (`+ − × ÷`, `min/max/abs`, variables such as `base_salary`, `working_days`, `overtime_hours`, `rate`). The tax brackets and insurance rates installed for a new company are **illustrative examples** and must be replaced with the official figures.
- **Attendance import** (`/payroll/periods/:id`): CSV or Excel with one row per employee and day (code, date, working hours, overtime, absence, leave, unpaid leave, mission, holiday work; Persian or Latin digits, Jalali or Gregorian dates). The whole file is validated and previewed (total/valid/invalid rows, warnings, per-employee totals, downloadable error report); it is stored only when every row is valid, in one transaction (`attendance_records`, inputs with source `import:<id>`).
- **Payroll liabilities**: posting a payroll creates its liabilities per kind (income tax withheld, employee and employer social security, other deductions credited to liability accounts). They are settled with ordinary treasury payments (Dr liability / Cr cash or bank); the paid amount is derived from live treasury transactions, so cancelling the payment in treasury reopens the liability, and a payroll with paid liabilities cannot be unposted.
- **Payment instructions**: an approved payroll lists one transfer per employee (national ID, bank, IBAN with mod-97 check, account, amount, date, period) and exports it as CSV/Excel in a pluggable layout (`generic` bank upload or `report`). Exporting is logged and never posts anything — the salary payment remains one aggregated entry.
- **Check exports** (`GET /checks/export?format=csv|xlsx|pdf&…filters`) contain the entire filtered result, generated on the server with company name, title, generation time, applied filters, totals and record count. Check transitions cannot be dated before the current status, and a replacement check can replace only one check.
- **Budget forecasts** are pluggable (`forecast_method=straight_line|historical_average|previous_year|manual`); each row states its method, basis and data coverage. They are arithmetic on ledger actuals, not statistical predictions. Managers (`budgets.forecast`) can set a full-year forecast per line with a reason; the history is kept.
- **Budget override audit**: every warning, confirmation, approved override and *blocked* attempt is stored with budget, account, cost center, transaction (source document, entry), threshold, limit, budget, actual, requested and exceeded amounts, user, role, reason, decision and time (`/budgets/control/events/{id}`). Blocked postings roll back, so their events are written after the response.
- **Budgets** (`/budgets`): a budget belongs to a fiscal year and has lines per account (group accounts budget their subtree) and optional cost center, with an amount per accounting period (manual, equal, percentage or last year's pattern). Versions go draft → submitted → approved → active → closed/archived; submitted and later versions are immutable (also enforced by triggers) and changes are made as a revision that archives the previous version when activated. Actuals are always computed from posted ledger lines. **Budget control** runs inside every posting transaction: spending over an active budget line (by period, year to date or annual basis and a threshold) warns, requires confirmation (soft limit, `X-Budget-Confirm` header) or is blocked (hard limit) unless a user with `budgets.override` gives a reason (`X-Budget-Override`); rules can target a budget, account, cost center and role, and every warning, confirmation and override is logged.
- New posting rules (Settings → Posting rules): checks receivable, checks in collection, checks payable, salary expense, employer social security expense, salaries payable, social security payable, payroll tax payable, employee loans and advances. Migration `0005` adds the default accounts (1204, 1205, 3203, 8110) and these rules to existing companies when the standard chart is in use.
- Amounts are `NUMERIC(20,2)` and handled as exact decimals end to end (Go `shopspring/decimal`, frontend `big.js`); rounding is half away from zero to the company's decimal places (0 for Rial by default).

## License
Fonts: Vazir by Saber Rastikerdar (SIL Open Font License, see `frontend/src/assets/fonts/LICENSE-Vazir.txt`).
