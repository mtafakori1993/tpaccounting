# Tafakor Pardazan Accounting — accounting model (assistant guide)

Built from the application's code (`backend/internal/*`) and README. Static file; the AI service sends a
compact excerpt of it to the model. The company's real accounts always come from the API, never from here.

## Double entry
- Chart of accounts: group → general → subsidiary → detailed (levels 1–4). Only leaf accounts (level ≥ 2, no
  children) accept postings ("postable"). Account types: asset, liability, equity, income, expense.
- Journal entries: draft → posted → cancelled. Posted entries are immutable; **cancelling creates a posted
  reversal entry** (cancel-by-reversal). Debits must equal credits.
- Amounts are exact decimals in the company's base currency (default IRR, 0 decimals). Documents have **no
  currency field**: foreign amounts are converted with a rate the user confirms; the original amount goes in the description.

## Automatic accounting (accounts come from Settings → Posting rules)
| Document | API | Entry |
|---|---|---|
| Receipt (party) | `POST /treasury/transactions` kind=receipt, party_id | Dr cash/bank · Cr party receivable (or party's own receivable account) |
| Receipt (account) | kind=receipt, counter_account_id | Dr cash/bank · Cr chosen account (e.g. loan from shareholder) |
| Payment | kind=payment | Dr payable/chosen account · Cr cash/bank |
| Transfer | kind=transfer | Dr destination · Cr source |
| Expense | `POST /expenses` post=true | Dr category's expense account (+cost center) · Cr cash/bank, or Cr payable of a party (payment_method=credit) |
| Sales invoice | `POST /sales/invoices` | Dr receivable · Cr sales, Cr output VAT; COGS/inventory for stock items |
| Purchase invoice | `POST /purchases/invoices` | Dr inventory/purchases, Dr input VAT · Cr payable |

Posting-rule keys: receivable, payable, sales, service_revenue, sales_returns, sales_discount, inventory, cogs,
purchases, tax_output, tax_input, opening_equity, retained_earnings, cash_parent, bank_parent, checks_*, payroll_*.

## Common events → where to register
- **Loan received from a person/company** → Treasury receipt with *counter account* = a liability account such as
  "Loans from shareholders/partners" (ask the user which one; never guess; remember the answer). Party = the lender.
- **Owner capital contribution** → receipt with counter account = an equity account (capital / partners' current).
- **Customer pays an invoice** → receipt with party = customer, automatic allocation to open invoices (oldest first).
- **Customer advance / prepayment** (پیش‌دریافت) → receipt with party = customer, **no allocation**; project = cost
  center of kind *project*.
- **Expense paid by company card/bank/cash** → Expense with category, payment_method bank/cash and the treasury account.
- **Expense paid personally by an employee/owner** → Expense with payment_method=credit and party = that person
  (the company now owes them).
- **Supplier payment** → payment with party = supplier.
- **Transfer between own accounts** → Treasury → New transfer (not automated by the assistant).

## Controls that always apply (enforced by the API, surfaced in plain language)
- Fiscal years and monthly periods: postings must fall in an **open fiscal year** and an **unlocked period**.
- **Budget control** runs inside every posting: warnings (`X-Budget-Warnings`), soft limit
  (`budget_confirmation_required`, needs `X-Budget-Confirm`) or hard limit (`budget_exceeded`, needs an authorised override).
- Cash boxes cannot go below zero (`insufficient_funds`).
- Permissions: module × action (view, create, edit, post, cancel, delete, export).
- Checks, payroll and manual journal entries are specialised workflows; the assistant never automates them.

## Cost centers
Kinds: cost_center, department, project. Journal lines carry one cost_center_id; projects are reported and
budgeted through the same dimension.
