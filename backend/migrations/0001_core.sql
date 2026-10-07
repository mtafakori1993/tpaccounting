-- Core: companies, fiscal years, users, roles, permissions, sessions, audit log.

CREATE TABLE companies (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    name_en              TEXT        NOT NULL DEFAULT '',
    legal_name           TEXT        NOT NULL DEFAULT '',
    registration_number  TEXT        NOT NULL DEFAULT '',
    national_id          TEXT        NOT NULL DEFAULT '',
    economic_code        TEXT        NOT NULL DEFAULT '',
    address              TEXT        NOT NULL DEFAULT '',
    postal_code          TEXT        NOT NULL DEFAULT '',
    phone                TEXT        NOT NULL DEFAULT '',
    email                TEXT        NOT NULL DEFAULT '',
    website              TEXT        NOT NULL DEFAULT '',
    logo_key             TEXT,
    base_currency        TEXT        NOT NULL DEFAULT 'IRR',
    amount_decimals      SMALLINT    NOT NULL DEFAULT 0 CHECK (amount_decimals BETWEEN 0 AND 2),
    calendar             TEXT        NOT NULL DEFAULT 'jalali' CHECK (calendar IN ('jalali', 'gregorian')),
    allow_negative_stock BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE fiscal_years (
    id               BIGSERIAL PRIMARY KEY,
    company_id       BIGINT      NOT NULL REFERENCES companies (id),
    name             TEXT        NOT NULL,
    start_date       DATE        NOT NULL,
    end_date         DATE        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    closed_at        TIMESTAMPTZ,
    closing_entry_id BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_date > start_date),
    UNIQUE (company_id, name)
);
CREATE INDEX fiscal_years_company_dates ON fiscal_years (company_id, start_date, end_date);

CREATE TABLE accounting_periods (
    id             BIGSERIAL PRIMARY KEY,
    company_id     BIGINT  NOT NULL REFERENCES companies (id),
    fiscal_year_id BIGINT  NOT NULL REFERENCES fiscal_years (id) ON DELETE CASCADE,
    seq            INT     NOT NULL,
    name           TEXT    NOT NULL,
    start_date     DATE    NOT NULL,
    end_date       DATE    NOT NULL,
    is_locked      BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK (end_date >= start_date),
    UNIQUE (fiscal_year_id, seq)
);
CREATE INDEX accounting_periods_dates ON accounting_periods (company_id, start_date, end_date);

-- Permissions are a fixed catalogue of module.action codes.
CREATE TABLE permissions (
    code   TEXT PRIMARY KEY,
    module TEXT NOT NULL,
    action TEXT NOT NULL,
    UNIQUE (module, action)
);

CREATE TABLE roles (
    id          BIGSERIAL PRIMARY KEY,
    code        TEXT        NOT NULL UNIQUE,
    name        TEXT        NOT NULL,
    name_en     TEXT        NOT NULL DEFAULT '',
    description TEXT        NOT NULL DEFAULT '',
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id         BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_code TEXT   NOT NULL REFERENCES permissions (code) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_code)
);

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    company_id    BIGINT      NOT NULL REFERENCES companies (id),
    email         TEXT        NOT NULL,
    full_name     TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    role_id       BIGINT      NOT NULL REFERENCES roles (id),
    is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
    language      TEXT        NOT NULL DEFAULT 'fa' CHECK (language IN ('fa', 'en')),
    theme         TEXT        NOT NULL DEFAULT 'light' CHECK (theme IN ('light', 'dark')),
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_unique ON users (lower(email));

CREATE TABLE sessions (
    id           BIGSERIAL PRIMARY KEY,
    token_hash   BYTEA       NOT NULL UNIQUE,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    company_id   BIGINT      NOT NULL REFERENCES companies (id),
    ip           TEXT        NOT NULL DEFAULT '',
    user_agent   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT REFERENCES companies (id),
    user_id     BIGINT REFERENCES users (id),
    action      TEXT        NOT NULL,
    entity      TEXT        NOT NULL,
    entity_id   BIGINT,
    ip          TEXT        NOT NULL DEFAULT '',
    user_agent  TEXT        NOT NULL DEFAULT '',
    old_value   JSONB,
    new_value   JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_company_created ON audit_logs (company_id, created_at DESC);
CREATE INDEX audit_logs_entity ON audit_logs (entity, entity_id);

-- Per-company numbering sequences (journal entries, invoices, ...).
CREATE TABLE doc_sequences (
    company_id     BIGINT NOT NULL REFERENCES companies (id),
    scope          TEXT   NOT NULL,
    fiscal_year_id BIGINT NOT NULL DEFAULT 0,
    next_number    BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (company_id, scope, fiscal_year_id)
);

-- Permission catalogue and system roles.
INSERT INTO permissions (code, module, action)
SELECT m || '.' || a, m, a
FROM unnest(ARRAY['company', 'accounts', 'parties', 'journal', 'sales', 'purchases', 'products', 'inventory',
                  'treasury', 'expenses', 'cost_centers', 'reports', 'users', 'roles', 'audit', 'settings']) AS m,
     unnest(ARRAY['view', 'create', 'edit', 'post', 'cancel', 'delete', 'export']) AS a;

INSERT INTO roles (code, name, name_en, description, is_system) VALUES
    ('admin', 'مدیر سیستم', 'Administrator', 'Full access to every module', TRUE),
    ('accounting_manager', 'مدیر مالی', 'Accounting Manager', 'All accounting operations including cancellation', TRUE),
    ('accountant', 'حسابدار', 'Accountant', 'Daily accounting operations', TRUE),
    ('viewer', 'مشاهده‌گر', 'Viewer', 'Read-only access with export', TRUE);

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r CROSS JOIN permissions p WHERE r.code = 'admin';

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r CROSS JOIN permissions p
WHERE r.code = 'accounting_manager' AND p.module NOT IN ('users', 'roles');

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r CROSS JOIN permissions p
WHERE r.code = 'accountant'
  AND p.module NOT IN ('users', 'roles', 'audit', 'settings', 'company')
  AND p.action IN ('view', 'create', 'edit', 'post', 'delete', 'export');

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r CROSS JOIN permissions p
WHERE r.code = 'accountant' AND p.code IN ('company.view', 'settings.view');

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r CROSS JOIN permissions p
WHERE r.code = 'viewer' AND p.action IN ('view', 'export') AND p.module NOT IN ('users', 'roles', 'audit');
