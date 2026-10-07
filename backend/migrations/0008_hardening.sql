-- Production hardening of payroll, checks and budgets:
--  * payroll statutory rules (income-tax tables, social-security rates) become
--    dated configuration instead of component constants;
--  * salary components gain fixed/variable, formula and effective dates;
--  * attendance imports, payroll liabilities settled through treasury,
--    payment-instruction export log;
--  * budget control events record the full override context;
--  * manual budget forecasts.

-- ---------- Payroll rules ----------

CREATE TABLE payroll_tax_tables (
    id             BIGSERIAL PRIMARY KEY,
    company_id     BIGINT         NOT NULL REFERENCES companies (id),
    name           TEXT           NOT NULL CHECK (length(trim(name)) > 0),
    effective_from DATE           NOT NULL,
    effective_to   DATE,
    -- Whether exemption and brackets are monthly or annual amounts.
    mode           TEXT           NOT NULL DEFAULT 'monthly' CHECK (mode IN ('monthly', 'annual')),
    -- Tax-free threshold deducted from the taxable income before the brackets.
    exemption      NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (exemption >= 0),
    -- [{"upto": "…" | null, "rate": "10"}], ascending.
    brackets       JSONB          NOT NULL DEFAULT '[]',
    notes          TEXT           NOT NULL DEFAULT '',
    is_active      BOOLEAN        NOT NULL DEFAULT TRUE,
    created_by     BIGINT REFERENCES users (id),
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);
CREATE INDEX payroll_tax_tables_company ON payroll_tax_tables (company_id, effective_from);

CREATE TABLE payroll_insurance_rules (
    id                BIGSERIAL PRIMARY KEY,
    company_id        BIGINT         NOT NULL REFERENCES companies (id),
    name              TEXT           NOT NULL CHECK (length(trim(name)) > 0),
    effective_from    DATE           NOT NULL,
    effective_to      DATE,
    employee_rate     NUMERIC(7, 4)  NOT NULL DEFAULT 0 CHECK (employee_rate BETWEEN 0 AND 100),
    employer_rate     NUMERIC(7, 4)  NOT NULL DEFAULT 0 CHECK (employer_rate BETWEEN 0 AND 100),
    unemployment_rate NUMERIC(7, 4)  NOT NULL DEFAULT 0 CHECK (unemployment_rate BETWEEN 0 AND 100),
    -- Monthly cap of the insurable base (0 = none).
    ceiling           NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (ceiling >= 0),
    -- Earning component codes forming the base; empty = components marked insurable.
    eligible_codes    TEXT[]         NOT NULL DEFAULT '{}',
    notes             TEXT           NOT NULL DEFAULT '',
    is_active         BOOLEAN        NOT NULL DEFAULT TRUE,
    created_by        BIGINT REFERENCES users (id),
    created_at        TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);
CREATE INDEX payroll_insurance_rules_company ON payroll_insurance_rules (company_id, effective_from);

ALTER TABLE salary_components
    ADD COLUMN variability     TEXT NOT NULL DEFAULT 'fixed' CHECK (variability IN ('fixed', 'variable')),
    ADD COLUMN formula         TEXT NOT NULL DEFAULT '',
    -- Share of the insurance rule used by method insurance_rule.
    ADD COLUMN insurance_share TEXT NOT NULL DEFAULT '' CHECK (insurance_share IN ('', 'employee', 'employer', 'unemployment')),
    ADD COLUMN effective_from  DATE,
    ADD COLUMN effective_to    DATE,
    ADD CONSTRAINT salary_components_effective CHECK (effective_to IS NULL OR effective_from IS NULL OR effective_to >= effective_from);
ALTER TABLE salary_components DROP CONSTRAINT salary_components_method_check;
ALTER TABLE salary_components ADD CONSTRAINT salary_components_method_check CHECK (method IN (
    'fixed', 'base_percent', 'daily_base', 'hourly_base', 'per_unit', 'per_child', 'percent_of_insurable', 'percent_of_gross',
    'tax_brackets', 'manual', 'formula', 'tax_table', 'insurance_rule'));
ALTER TABLE salary_components DROP CONSTRAINT salary_components_quantity_field_check;
ALTER TABLE salary_components ADD CONSTRAINT salary_components_quantity_field_check CHECK (quantity_field IN (
    '', 'working_days', 'working_hours', 'absence_days', 'unpaid_leave_days', 'paid_leave_days', 'overtime_hours', 'mission_days', 'holiday_work_hours'));

-- Manual / bonus / commission / loan style components vary month to month.
UPDATE salary_components SET variability = 'variable'
WHERE method IN ('manual', 'hourly_base', 'per_unit', 'daily_base') OR category IN ('overtime', 'bonus', 'loan', 'advance', 'unpaid_leave');

-- Existing companies: move the tax brackets and insurance rates held on the
-- default components into dated rule tables, effective from the first fiscal year.
INSERT INTO payroll_tax_tables (company_id, name, effective_from, mode, exemption, brackets, notes)
SELECT c.company_id, 'جدول مالیات حقوق (منتقل‌شده — با جدول رسمی سال بررسی شود)',
       COALESCE((SELECT min(start_date) FROM fiscal_years f WHERE f.company_id = c.company_id), CURRENT_DATE), 'monthly', c.exemption, c.brackets,
       'Migrated from the income_tax salary component. Verify against the official table for the year.'
FROM salary_components c WHERE c.code = 'income_tax' AND c.method = 'tax_brackets';
UPDATE salary_components SET method = 'tax_table' WHERE code = 'income_tax' AND method = 'tax_brackets';

INSERT INTO payroll_insurance_rules (company_id, name, effective_from, employee_rate, employer_rate, unemployment_rate, ceiling, notes)
SELECT co.id, 'بیمه تأمین اجتماعی (منتقل‌شده — نرخ‌ها بررسی شود)',
       COALESCE((SELECT min(start_date) FROM fiscal_years f WHERE f.company_id = co.id), CURRENT_DATE),
       COALESCE(emp.rate, 0), COALESCE(er.rate, 0), COALESCE(un.rate, 0), COALESCE(emp.ceiling, 0),
       'Migrated from the insurance salary components. Verify the rates and set the insurable ceiling.'
FROM companies co
LEFT JOIN salary_components emp ON emp.company_id = co.id AND emp.code = 'insurance_employee' AND emp.method = 'percent_of_insurable'
LEFT JOIN salary_components er ON er.company_id = co.id AND er.code = 'insurance_employer' AND er.method = 'percent_of_insurable'
LEFT JOIN salary_components un ON un.company_id = co.id AND un.code = 'unemployment' AND un.method = 'percent_of_insurable'
WHERE emp.id IS NOT NULL OR er.id IS NOT NULL OR un.id IS NOT NULL;
UPDATE salary_components SET method = 'insurance_rule', insurance_share = 'employee', rate = 0 WHERE code = 'insurance_employee' AND method = 'percent_of_insurable';
UPDATE salary_components SET method = 'insurance_rule', insurance_share = 'employer', rate = 0 WHERE code = 'insurance_employer' AND method = 'percent_of_insurable';
UPDATE salary_components SET method = 'insurance_rule', insurance_share = 'unemployment', rate = 0 WHERE code = 'unemployment' AND method = 'percent_of_insurable';

-- What a calculation used (day basis, divisor, rule ids) and what it warned about.
ALTER TABLE payroll_periods
    ADD COLUMN calc_basis JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN warnings   JSONB NOT NULL DEFAULT '[]';

ALTER TABLE payroll_inputs ADD COLUMN working_hours NUMERIC(7, 2) NOT NULL DEFAULT 0 CHECK (working_hours >= 0);

-- ---------- Attendance import ----------

CREATE TABLE payroll_attendance_imports (
    id             BIGSERIAL PRIMARY KEY,
    company_id     BIGINT      NOT NULL REFERENCES companies (id),
    period_id      BIGINT      NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    file_name      TEXT        NOT NULL DEFAULT '',
    file_format    TEXT        NOT NULL CHECK (file_format IN ('csv', 'xlsx')),
    total_rows     INT         NOT NULL,
    valid_rows     INT         NOT NULL,
    invalid_rows   INT         NOT NULL,
    warning_count  INT         NOT NULL,
    imported_rows  INT         NOT NULL,
    employee_count INT         NOT NULL,
    created_by     BIGINT REFERENCES users (id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payroll_attendance_imports_period ON payroll_attendance_imports (period_id, id);

-- Daily attendance behind the period inputs.
CREATE TABLE attendance_records (
    id                 BIGSERIAL PRIMARY KEY,
    period_id          BIGINT        NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    import_id          BIGINT REFERENCES payroll_attendance_imports (id) ON DELETE SET NULL,
    employee_id        BIGINT        NOT NULL REFERENCES employees (id),
    date               DATE          NOT NULL,
    working_hours      NUMERIC(5, 2) NOT NULL DEFAULT 0 CHECK (working_hours BETWEEN 0 AND 24),
    overtime_hours     NUMERIC(5, 2) NOT NULL DEFAULT 0 CHECK (overtime_hours BETWEEN 0 AND 24),
    absence_days       NUMERIC(4, 2) NOT NULL DEFAULT 0 CHECK (absence_days BETWEEN 0 AND 1),
    paid_leave_days    NUMERIC(4, 2) NOT NULL DEFAULT 0 CHECK (paid_leave_days BETWEEN 0 AND 1),
    unpaid_leave_days  NUMERIC(4, 2) NOT NULL DEFAULT 0 CHECK (unpaid_leave_days BETWEEN 0 AND 1),
    mission_days       NUMERIC(4, 2) NOT NULL DEFAULT 0 CHECK (mission_days BETWEEN 0 AND 1),
    holiday_work_hours NUMERIC(5, 2) NOT NULL DEFAULT 0 CHECK (holiday_work_hours BETWEEN 0 AND 24),
    CONSTRAINT attendance_records_unique UNIQUE (period_id, employee_id, date)
);
CREATE TRIGGER attendance_records_protect BEFORE INSERT OR UPDATE OR DELETE ON attendance_records
    FOR EACH ROW EXECUTE FUNCTION protect_posted_payslips();

-- ---------- Payroll liabilities (tax, insurance) settled through treasury ----------

CREATE TABLE payroll_liabilities (
    id               BIGSERIAL PRIMARY KEY,
    company_id       BIGINT         NOT NULL REFERENCES companies (id),
    period_id        BIGINT         NOT NULL REFERENCES payroll_periods (id),
    kind             TEXT           NOT NULL CHECK (kind IN ('employee_tax', 'employee_insurance', 'employer_insurance', 'other')),
    account_id       BIGINT         NOT NULL REFERENCES accounts (id),
    amount           NUMERIC(20, 2) NOT NULL CHECK (amount > 0),
    journal_entry_id BIGINT REFERENCES journal_entries (id),
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT payroll_liabilities_unique UNIQUE (period_id, kind, account_id)
);
CREATE INDEX payroll_liabilities_company ON payroll_liabilities (company_id, period_id);

-- Each settlement is an ordinary treasury payment (Dr liability / Cr cash or bank).
-- Paid amounts are derived from the live (not cancelled) treasury transactions.
CREATE TABLE payroll_liability_payments (
    id                      BIGSERIAL PRIMARY KEY,
    liability_id            BIGINT         NOT NULL REFERENCES payroll_liabilities (id),
    treasury_transaction_id BIGINT         NOT NULL UNIQUE REFERENCES treasury_transactions (id),
    amount                  NUMERIC(20, 2) NOT NULL CHECK (amount > 0),
    created_by              BIGINT REFERENCES users (id),
    created_at              TIMESTAMPTZ    NOT NULL DEFAULT now()
);
CREATE INDEX payroll_liability_payments_liability ON payroll_liability_payments (liability_id);

-- ---------- Payment instructions (bank transfer files) ----------

CREATE TABLE payroll_payment_exports (
    id           BIGSERIAL PRIMARY KEY,
    company_id   BIGINT         NOT NULL REFERENCES companies (id),
    period_id    BIGINT         NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    layout       TEXT           NOT NULL,
    format       TEXT           NOT NULL,
    row_count    INT            NOT NULL,
    total_amount NUMERIC(20, 2) NOT NULL,
    created_by   BIGINT REFERENCES users (id),
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT now()
);
CREATE INDEX payroll_payment_exports_period ON payroll_payment_exports (period_id, id);

-- ---------- Budget control audit ----------

ALTER TABLE budget_control_events DROP CONSTRAINT budget_control_events_outcome_check;
ALTER TABLE budget_control_events ADD CONSTRAINT budget_control_events_outcome_check CHECK (outcome IN ('warned', 'confirmed', 'overridden', 'blocked'));
ALTER TABLE budget_control_events
    ADD COLUMN threshold_percent NUMERIC(6, 2),
    ADD COLUMN basis             TEXT NOT NULL DEFAULT '',
    -- The limit the rule enforces: budget × threshold.
    ADD COLUMN limit_amount      NUMERIC(20, 2),
    ADD COLUMN exceeded_amount   NUMERIC(20, 2),
    -- The business document behind the posting (invoice, expense, …).
    ADD COLUMN source_type       TEXT NOT NULL DEFAULT '',
    ADD COLUMN source_id         BIGINT,
    ADD COLUMN entry_reference   TEXT NOT NULL DEFAULT '',
    ADD COLUMN entry_description TEXT NOT NULL DEFAULT '',
    ADD COLUMN role_code         TEXT NOT NULL DEFAULT '';
UPDATE budget_control_events SET exceeded_amount = GREATEST(actual_amount - budget_amount, 0) WHERE exceeded_amount IS NULL;
CREATE INDEX budget_control_events_outcome ON budget_control_events (company_id, outcome, created_at DESC);

-- ---------- Manual forecasts ----------

CREATE TABLE budget_forecasts (
    id         BIGSERIAL PRIMARY KEY,
    company_id BIGINT         NOT NULL REFERENCES companies (id),
    line_id    BIGINT         NOT NULL REFERENCES budget_lines (id) ON DELETE CASCADE,
    amount     NUMERIC(20, 2) NOT NULL CHECK (amount >= 0),
    reason     TEXT           NOT NULL CHECK (length(trim(reason)) > 0),
    created_by BIGINT REFERENCES users (id),
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now()
);
-- The newest row per line is the forecast in force; older rows are the history.
CREATE INDEX budget_forecasts_line ON budget_forecasts (line_id, id DESC);

-- ---------- Permissions ----------

INSERT INTO permissions (code, module, action) VALUES
    ('payroll.settings', 'payroll', 'settings'),
    ('budgets.forecast', 'budgets', 'forecast')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r JOIN permissions p ON p.code IN ('payroll.settings', 'budgets.forecast')
WHERE r.code IN ('admin', 'accounting_manager')
ON CONFLICT DO NOTHING;

-- Paying payroll liabilities goes through treasury; payroll officers may view treasury accounts to choose one.
INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r JOIN permissions p ON p.code = 'treasury.view'
WHERE r.code = 'payroll_officer'
ON CONFLICT DO NOTHING;

-- Report queries
CREATE INDEX IF NOT EXISTS checks_company_type_due ON checks (company_id, check_type, due_date);
CREATE INDEX IF NOT EXISTS payslips_employee ON payslips (employee_id);
