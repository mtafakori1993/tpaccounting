-- Payroll: employees, configurable salary components, attendance inputs,
-- payroll periods (runs) and payslips. Accounting goes through the ledger.

CREATE TABLE employees (
    id                  BIGSERIAL PRIMARY KEY,
    company_id          BIGINT         NOT NULL REFERENCES companies (id),
    code                TEXT           NOT NULL CHECK (length(trim(code)) > 0),
    first_name          TEXT           NOT NULL CHECK (length(trim(first_name)) > 0),
    last_name           TEXT           NOT NULL CHECK (length(trim(last_name)) > 0),
    national_id         TEXT           NOT NULL DEFAULT '' CHECK (national_id = '' OR national_id ~ '^[0-9]{10}$'),
    personnel_number    TEXT           NOT NULL DEFAULT '',
    mobile              TEXT           NOT NULL DEFAULT '',
    email               TEXT           NOT NULL DEFAULT '',
    address             TEXT           NOT NULL DEFAULT '',
    bank_name           TEXT           NOT NULL DEFAULT '',
    bank_account_number TEXT           NOT NULL DEFAULT '',
    iban                TEXT           NOT NULL DEFAULT '',
    department_id       BIGINT REFERENCES cost_centers (id),
    cost_center_id      BIGINT REFERENCES cost_centers (id),
    position            TEXT           NOT NULL DEFAULT '',
    employment_type     TEXT           NOT NULL DEFAULT 'full_time' CHECK (employment_type IN ('full_time', 'part_time', 'contract', 'hourly', 'intern')),
    hire_date           DATE           NOT NULL,
    termination_date    DATE,
    base_salary         NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (base_salary >= 0),
    children_count      SMALLINT       NOT NULL DEFAULT 0 CHECK (children_count BETWEEN 0 AND 20),
    marital_status      TEXT           NOT NULL DEFAULT 'single' CHECK (marital_status IN ('single', 'married')),
    insurance_number    TEXT           NOT NULL DEFAULT '',
    insurance_exempt    BOOLEAN        NOT NULL DEFAULT FALSE,
    tax_exempt          BOOLEAN        NOT NULL DEFAULT FALSE,
    is_active           BOOLEAN        NOT NULL DEFAULT TRUE,
    notes               TEXT           NOT NULL DEFAULT '',
    created_by          BIGINT REFERENCES users (id),
    updated_by          BIGINT REFERENCES users (id),
    created_at          TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT employees_code_unique UNIQUE (company_id, code),
    CHECK (termination_date IS NULL OR termination_date >= hire_date)
);
CREATE UNIQUE INDEX employees_national_id_unique ON employees (company_id, national_id) WHERE national_id <> '';
CREATE INDEX employees_department ON employees (department_id);

-- Split of an employee's salary cost between cost centers / projects (percentages add up to 100).
CREATE TABLE employee_allocations (
    employee_id    BIGINT        NOT NULL REFERENCES employees (id) ON DELETE CASCADE,
    cost_center_id BIGINT        NOT NULL REFERENCES cost_centers (id),
    percent        NUMERIC(5, 2) NOT NULL CHECK (percent > 0 AND percent <= 100),
    PRIMARY KEY (employee_id, cost_center_id)
);

-- Configurable earnings, deductions and employer contributions.
CREATE TABLE salary_components (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT         NOT NULL REFERENCES companies (id),
    code        TEXT           NOT NULL CHECK (code ~ '^[a-z0-9_]+$'),
    name        TEXT           NOT NULL CHECK (length(trim(name)) > 0),
    name_en     TEXT           NOT NULL DEFAULT '',
    kind        TEXT           NOT NULL CHECK (kind IN ('earning', 'deduction', 'employer')),
    -- Accounting role of the component; decides which posting rule it uses by default.
    category    TEXT           NOT NULL DEFAULT 'other' CHECK (category IN ('salary', 'allowance', 'overtime', 'bonus', 'insurance', 'tax', 'loan', 'advance', 'unpaid_leave', 'other')),
    method      TEXT           NOT NULL CHECK (method IN ('fixed', 'base_percent', 'daily_base', 'hourly_base', 'per_unit', 'per_child', 'percent_of_insurable', 'percent_of_gross', 'tax_brackets', 'manual')),
    rate        NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (rate >= 0),
    -- Attendance quantity used by daily/hourly/per-unit methods.
    quantity_field TEXT        NOT NULL DEFAULT '' CHECK (quantity_field IN ('', 'working_days', 'absence_days', 'unpaid_leave_days', 'paid_leave_days', 'overtime_hours', 'mission_days', 'holiday_work_hours')),
    -- Progressive brackets for tax_brackets: [{"upto": "…" | null, "rate": "10"}].
    brackets    JSONB          NOT NULL DEFAULT '[]',
    -- Monthly exemption for tax_brackets / cap for percent_of_insurable (0 = none).
    exemption   NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (exemption >= 0),
    ceiling     NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (ceiling >= 0),
    taxable     BOOLEAN        NOT NULL DEFAULT TRUE,
    insurable   BOOLEAN        NOT NULL DEFAULT TRUE,
    -- Optional account override (expense for earnings/employer, asset or liability for deductions).
    account_id  BIGINT REFERENCES accounts (id),
    seq         INT            NOT NULL DEFAULT 100,
    is_active   BOOLEAN        NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT salary_components_code_unique UNIQUE (company_id, code),
    -- Only earnings form the taxable / insurable base.
    CHECK (kind = 'earning' OR (NOT taxable AND NOT insurable))
);

-- Employee-specific amounts or rates that replace the component default.
CREATE TABLE employee_components (
    employee_id  BIGINT         NOT NULL REFERENCES employees (id) ON DELETE CASCADE,
    component_id BIGINT         NOT NULL REFERENCES salary_components (id),
    rate         NUMERIC(20, 4) NOT NULL CHECK (rate >= 0),
    PRIMARY KEY (employee_id, component_id)
);

CREATE TABLE payroll_periods (
    id                  BIGSERIAL PRIMARY KEY,
    company_id          BIGINT         NOT NULL REFERENCES companies (id),
    fiscal_year_id      BIGINT         NOT NULL REFERENCES fiscal_years (id),
    year                INT            NOT NULL,
    month               INT            NOT NULL CHECK (month BETWEEN 1 AND 12),
    name                TEXT           NOT NULL,
    start_date          DATE           NOT NULL,
    end_date            DATE           NOT NULL,
    status              TEXT           NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'calculated', 'approved', 'posted', 'paid', 'closed')),
    payment_date        DATE,
    treasury_account_id BIGINT REFERENCES treasury_accounts (id),
    gross_total         NUMERIC(20, 2) NOT NULL DEFAULT 0,
    deduction_total     NUMERIC(20, 2) NOT NULL DEFAULT 0,
    net_total           NUMERIC(20, 2) NOT NULL DEFAULT 0,
    employer_total      NUMERIC(20, 2) NOT NULL DEFAULT 0,
    employee_count      INT            NOT NULL DEFAULT 0,
    journal_entry_id    BIGINT REFERENCES journal_entries (id),
    payment_entry_id    BIGINT REFERENCES journal_entries (id),
    notes               TEXT           NOT NULL DEFAULT '',
    calculated_at       TIMESTAMPTZ,
    calculated_by       BIGINT REFERENCES users (id),
    approved_at         TIMESTAMPTZ,
    approved_by         BIGINT REFERENCES users (id),
    posted_at           TIMESTAMPTZ,
    posted_by           BIGINT REFERENCES users (id),
    paid_at             TIMESTAMPTZ,
    paid_by             BIGINT REFERENCES users (id),
    closed_at           TIMESTAMPTZ,
    closed_by           BIGINT REFERENCES users (id),
    created_by          BIGINT REFERENCES users (id),
    created_at          TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT payroll_periods_month_unique UNIQUE (company_id, year, month),
    CHECK (end_date >= start_date),
    CHECK (net_total = gross_total - deduction_total),
    CHECK (status NOT IN ('posted', 'paid', 'closed') OR journal_entry_id IS NOT NULL),
    CHECK (status NOT IN ('paid', 'closed') OR payment_entry_id IS NOT NULL)
);

-- Working data per employee and period. source allows future attendance-system imports.
CREATE TABLE payroll_inputs (
    period_id          BIGINT        NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    employee_id        BIGINT        NOT NULL REFERENCES employees (id),
    working_days       NUMERIC(6, 2) NOT NULL DEFAULT 0 CHECK (working_days >= 0),
    absence_days       NUMERIC(6, 2) NOT NULL DEFAULT 0 CHECK (absence_days >= 0),
    unpaid_leave_days  NUMERIC(6, 2) NOT NULL DEFAULT 0 CHECK (unpaid_leave_days >= 0),
    paid_leave_days    NUMERIC(6, 2) NOT NULL DEFAULT 0 CHECK (paid_leave_days >= 0),
    overtime_hours     NUMERIC(7, 2) NOT NULL DEFAULT 0 CHECK (overtime_hours >= 0),
    mission_days       NUMERIC(6, 2) NOT NULL DEFAULT 0 CHECK (mission_days >= 0),
    holiday_work_hours NUMERIC(7, 2) NOT NULL DEFAULT 0 CHECK (holiday_work_hours >= 0),
    source             TEXT          NOT NULL DEFAULT 'manual',
    PRIMARY KEY (period_id, employee_id)
);

-- Amounts for manual components (bonus, commission, loan installment, ...).
CREATE TABLE payroll_input_items (
    period_id    BIGINT         NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    employee_id  BIGINT         NOT NULL REFERENCES employees (id),
    component_id BIGINT         NOT NULL REFERENCES salary_components (id),
    amount       NUMERIC(20, 2) NOT NULL CHECK (amount >= 0),
    PRIMARY KEY (period_id, employee_id, component_id)
);

-- Calculated payslips: a snapshot that does not change when master data changes.
CREATE TABLE payslips (
    id                BIGSERIAL PRIMARY KEY,
    period_id         BIGINT         NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    employee_id       BIGINT         NOT NULL REFERENCES employees (id),
    employee_code     TEXT           NOT NULL,
    employee_name     TEXT           NOT NULL,
    department_id     BIGINT REFERENCES cost_centers (id),
    position          TEXT           NOT NULL DEFAULT '',
    base_salary       NUMERIC(20, 2) NOT NULL,
    working_days      NUMERIC(6, 2)  NOT NULL DEFAULT 0,
    gross             NUMERIC(20, 2) NOT NULL,
    total_deductions  NUMERIC(20, 2) NOT NULL,
    net               NUMERIC(20, 2) NOT NULL,
    employer_cost     NUMERIC(20, 2) NOT NULL,
    taxable_income    NUMERIC(20, 2) NOT NULL DEFAULT 0,
    insurable_income  NUMERIC(20, 2) NOT NULL DEFAULT 0,
    iban              TEXT           NOT NULL DEFAULT '',
    bank_account_number TEXT         NOT NULL DEFAULT '',
    -- Cost-center split used for the expense lines: [{"cost_center_id": 1, "percent": "60"}].
    allocations       JSONB          NOT NULL DEFAULT '[]',
    CONSTRAINT payslips_unique UNIQUE (period_id, employee_id),
    CHECK (net = gross - total_deductions)
);

CREATE TABLE payslip_lines (
    id             BIGSERIAL PRIMARY KEY,
    payslip_id     BIGINT         NOT NULL REFERENCES payslips (id) ON DELETE CASCADE,
    component_id   BIGINT         NOT NULL REFERENCES salary_components (id),
    kind           TEXT           NOT NULL CHECK (kind IN ('earning', 'deduction', 'employer')),
    category       TEXT           NOT NULL,
    code           TEXT           NOT NULL,
    name           TEXT           NOT NULL,
    name_en        TEXT           NOT NULL DEFAULT '',
    quantity       NUMERIC(20, 2) NOT NULL DEFAULT 0,
    rate           NUMERIC(20, 4) NOT NULL DEFAULT 0,
    amount         NUMERIC(20, 2) NOT NULL CHECK (amount >= 0),
    account_id     BIGINT REFERENCES accounts (id)
);
CREATE INDEX payslip_lines_payslip ON payslip_lines (payslip_id);

-- A posted payroll's payslips are frozen.
CREATE FUNCTION protect_posted_payslips() RETURNS trigger AS $$
DECLARE
    st TEXT;
BEGIN
    SELECT status INTO st FROM payroll_periods WHERE id = COALESCE(OLD.period_id, NEW.period_id);
    IF st IN ('approved', 'posted', 'paid', 'closed') THEN
        RAISE EXCEPTION 'payslips of an approved payroll are immutable' USING ERRCODE = 'P0001', CONSTRAINT = 'payroll_locked';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER payslips_protect BEFORE INSERT OR UPDATE OR DELETE ON payslips
    FOR EACH ROW EXECUTE FUNCTION protect_posted_payslips();
CREATE TRIGGER payroll_inputs_protect BEFORE INSERT OR UPDATE OR DELETE ON payroll_inputs
    FOR EACH ROW EXECUTE FUNCTION protect_posted_payslips();
CREATE TRIGGER payroll_input_items_protect BEFORE INSERT OR UPDATE OR DELETE ON payroll_input_items
    FOR EACH ROW EXECUTE FUNCTION protect_posted_payslips();

-- Permissions: payroll data is sensitive, so only managers get it by default.
INSERT INTO permissions (code, module, action)
SELECT 'payroll.' || a, 'payroll', a
FROM unnest(ARRAY['view', 'create', 'edit', 'calculate', 'approve', 'post', 'pay', 'close', 'delete', 'export']) AS a;

INSERT INTO roles (code, name, name_en, description, is_system) VALUES
    ('payroll_officer', 'کارشناس حقوق و دستمزد', 'Payroll Officer', 'Maintains employees and prepares payroll', TRUE)
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r JOIN permissions p ON p.module = 'payroll'
WHERE r.code IN ('admin', 'accounting_manager')
   OR (r.code = 'payroll_officer' AND p.action IN ('view', 'create', 'edit', 'calculate', 'export'))
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r JOIN permissions p ON p.code IN ('cost_centers.view', 'company.view')
WHERE r.code = 'payroll_officer'
ON CONFLICT DO NOTHING;
