-- Budgets: versioned plans per fiscal year (account × cost center × month),
-- compared with posted ledger actuals, and budget control rules applied when
-- journal entries are posted.

CREATE TABLE budgets (
    id             BIGSERIAL PRIMARY KEY,
    company_id     BIGINT      NOT NULL REFERENCES companies (id),
    fiscal_year_id BIGINT      NOT NULL REFERENCES fiscal_years (id),
    -- All versions of a budget share the root (NULL on the first version).
    root_id        BIGINT REFERENCES budgets (id),
    previous_id    BIGINT REFERENCES budgets (id),
    version        INT         NOT NULL DEFAULT 1 CHECK (version > 0),
    code           TEXT        NOT NULL CHECK (length(trim(code)) > 0),
    name           TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    name_en        TEXT        NOT NULL DEFAULT '',
    description    TEXT        NOT NULL DEFAULT '',
    -- Optional owner dimension (department / project / cost center).
    cost_center_id BIGINT REFERENCES cost_centers (id),
    revision_reason TEXT       NOT NULL DEFAULT '',
    status         TEXT        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'submitted', 'approved', 'active', 'closed', 'archived')),
    submitted_at   TIMESTAMPTZ,
    submitted_by   BIGINT REFERENCES users (id),
    approved_at    TIMESTAMPTZ,
    approved_by    BIGINT REFERENCES users (id),
    activated_at   TIMESTAMPTZ,
    activated_by   BIGINT REFERENCES users (id),
    closed_at      TIMESTAMPTZ,
    closed_by      BIGINT REFERENCES users (id),
    archived_at    TIMESTAMPTZ,
    created_by     BIGINT REFERENCES users (id),
    updated_by     BIGINT REFERENCES users (id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((root_id IS NULL) = (version = 1))
);
CREATE UNIQUE INDEX budgets_version_unique ON budgets (company_id, COALESCE(root_id, id), version);
CREATE UNIQUE INDEX budgets_code_unique ON budgets (company_id, fiscal_year_id, code) WHERE root_id IS NULL;
-- Only one version of a budget can be active.
CREATE UNIQUE INDEX budgets_one_active ON budgets (COALESCE(root_id, id)) WHERE status = 'active';
CREATE INDEX budgets_company_year ON budgets (company_id, fiscal_year_id, status);

CREATE TABLE budget_lines (
    id                BIGSERIAL PRIMARY KEY,
    budget_id         BIGINT         NOT NULL REFERENCES budgets (id) ON DELETE CASCADE,
    line_no           INT            NOT NULL,
    -- Any account; group accounts budget their whole subtree.
    account_id        BIGINT         NOT NULL REFERENCES accounts (id),
    cost_center_id    BIGINT REFERENCES cost_centers (id),
    allocation_method TEXT           NOT NULL DEFAULT 'manual' CHECK (allocation_method IN ('manual', 'equal', 'percentage', 'historical')),
    annual_amount     NUMERIC(20, 2) NOT NULL CHECK (annual_amount >= 0),
    notes             TEXT           NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX budget_lines_unique ON budget_lines (budget_id, account_id, COALESCE(cost_center_id, 0));
CREATE INDEX budget_lines_account ON budget_lines (account_id);

-- Planned amount per accounting period (seq of the fiscal year's periods).
CREATE TABLE budget_line_amounts (
    line_id BIGINT         NOT NULL REFERENCES budget_lines (id) ON DELETE CASCADE,
    seq     INT            NOT NULL CHECK (seq BETWEEN 1 AND 24),
    amount  NUMERIC(20, 2) NOT NULL CHECK (amount >= 0),
    PRIMARY KEY (line_id, seq)
);

-- Submitted, approved and later budgets are never silently overwritten: changes need a new version.
CREATE FUNCTION protect_budget_lines() RETURNS trigger AS $$
DECLARE
    st TEXT;
BEGIN
    IF TG_TABLE_NAME = 'budget_lines' THEN
        SELECT status INTO st FROM budgets WHERE id = COALESCE(NEW.budget_id, OLD.budget_id);
    ELSE
        SELECT b.status INTO st FROM budget_lines l JOIN budgets b ON b.id = l.budget_id WHERE l.id = COALESCE(NEW.line_id, OLD.line_id);
    END IF;
    IF st IS NOT NULL AND st <> 'draft' THEN
        RAISE EXCEPTION 'only draft budgets can be changed' USING ERRCODE = 'P0001', CONSTRAINT = 'budget_locked';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER budget_lines_protect BEFORE INSERT OR UPDATE OR DELETE ON budget_lines
    FOR EACH ROW EXECUTE FUNCTION protect_budget_lines();
CREATE TRIGGER budget_line_amounts_protect BEFORE INSERT OR UPDATE OR DELETE ON budget_line_amounts
    FOR EACH ROW EXECUTE FUNCTION protect_budget_lines();

-- What happens when posting pushes actual spending over budget.
CREATE TABLE budget_control_rules (
    id                BIGSERIAL PRIMARY KEY,
    company_id        BIGINT        NOT NULL REFERENCES companies (id),
    name              TEXT          NOT NULL DEFAULT '',
    -- NULL dimensions match everything; the most specific matching rule wins.
    budget_id         BIGINT REFERENCES budgets (id) ON DELETE CASCADE,
    account_id        BIGINT REFERENCES accounts (id),
    cost_center_id    BIGINT REFERENCES cost_centers (id),
    role_id           BIGINT REFERENCES roles (id) ON DELETE CASCADE,
    policy            TEXT          NOT NULL CHECK (policy IN ('none', 'warning', 'soft', 'hard')),
    threshold_percent NUMERIC(6, 2) NOT NULL DEFAULT 100 CHECK (threshold_percent > 0),
    -- Budget compared against: the posting's period, year to date, or the whole year.
    basis             TEXT          NOT NULL DEFAULT 'ytd' CHECK (basis IN ('period', 'ytd', 'annual')),
    is_active         BOOLEAN       NOT NULL DEFAULT TRUE,
    created_by        BIGINT REFERENCES users (id),
    created_at        TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX budget_control_rules_company ON budget_control_rules (company_id) WHERE is_active;

-- Log of budget warnings, confirmations and overrides at posting time.
CREATE TABLE budget_control_events (
    id               BIGSERIAL PRIMARY KEY,
    company_id       BIGINT         NOT NULL REFERENCES companies (id),
    journal_entry_id BIGINT REFERENCES journal_entries (id),
    rule_id          BIGINT REFERENCES budget_control_rules (id) ON DELETE SET NULL,
    budget_id        BIGINT REFERENCES budgets (id),
    budget_line_id   BIGINT REFERENCES budget_lines (id) ON DELETE SET NULL,
    account_id       BIGINT REFERENCES accounts (id),
    cost_center_id   BIGINT REFERENCES cost_centers (id),
    policy           TEXT           NOT NULL,
    outcome          TEXT           NOT NULL CHECK (outcome IN ('warned', 'confirmed', 'overridden')),
    amount           NUMERIC(20, 2) NOT NULL,
    budget_amount    NUMERIC(20, 2) NOT NULL,
    actual_amount    NUMERIC(20, 2) NOT NULL,
    reason           TEXT           NOT NULL DEFAULT '',
    user_id          BIGINT REFERENCES users (id),
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT now()
);
CREATE INDEX budget_control_events_company ON budget_control_events (company_id, created_at DESC);

INSERT INTO permissions (code, module, action)
SELECT 'budgets.' || a, 'budgets', a
FROM unnest(ARRAY['view', 'create', 'edit', 'submit', 'approve', 'activate', 'close', 'override', 'delete', 'export']) AS a;

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r JOIN permissions p ON p.module = 'budgets'
WHERE r.code IN ('admin', 'accounting_manager')
   OR (r.code = 'accountant' AND p.action IN ('view', 'create', 'edit', 'submit', 'delete', 'export'))
   OR (r.code = 'viewer' AND p.action IN ('view', 'export'))
ON CONFLICT DO NOTHING;
