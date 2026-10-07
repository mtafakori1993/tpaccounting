-- Accounting foundation: chart of accounts, cost centers, parties, journal entries.

CREATE TABLE accounts (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT      NOT NULL REFERENCES companies (id),
    parent_id   BIGINT REFERENCES accounts (id),
    code        TEXT        NOT NULL CHECK (code ~ '^[0-9]+$'),
    name        TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    name_en     TEXT        NOT NULL DEFAULT '',
    type        TEXT        NOT NULL CHECK (type IN ('asset', 'liability', 'equity', 'revenue', 'cost_of_sales', 'expense')),
    level       SMALLINT    NOT NULL CHECK (level BETWEEN 1 AND 4),
    -- Materialised id path, e.g. '/3/17/52/'. Used to aggregate balances of a subtree.
    path        TEXT        NOT NULL DEFAULT '',
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT accounts_code_unique UNIQUE (company_id, code),
    CHECK ((parent_id IS NULL) = (level = 1))
);
CREATE INDEX accounts_parent ON accounts (parent_id);
CREATE INDEX accounts_path ON accounts (path text_pattern_ops);

-- Configurable posting rules: which account is used for each automatic-accounting role.
CREATE TABLE account_mappings (
    company_id BIGINT NOT NULL REFERENCES companies (id),
    key        TEXT   NOT NULL,
    account_id BIGINT NOT NULL REFERENCES accounts (id),
    PRIMARY KEY (company_id, key)
);

CREATE TABLE cost_centers (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT      NOT NULL REFERENCES companies (id),
    code        TEXT        NOT NULL,
    name        TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    name_en     TEXT        NOT NULL DEFAULT '',
    description TEXT        NOT NULL DEFAULT '',
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cost_centers_code_unique UNIQUE (company_id, code)
);

CREATE TABLE parties (
    id                    BIGSERIAL PRIMARY KEY,
    company_id            BIGINT      NOT NULL REFERENCES companies (id),
    code                  TEXT        NOT NULL,
    kind                  TEXT        NOT NULL DEFAULT 'company' CHECK (kind IN ('person', 'company')),
    name                  TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    is_customer           BOOLEAN     NOT NULL DEFAULT FALSE,
    is_supplier           BOOLEAN     NOT NULL DEFAULT FALSE,
    is_other              BOOLEAN     NOT NULL DEFAULT FALSE,
    national_id           TEXT        NOT NULL DEFAULT '',
    economic_code         TEXT        NOT NULL DEFAULT '',
    registration_number   TEXT        NOT NULL DEFAULT '',
    phone                 TEXT        NOT NULL DEFAULT '',
    mobile                TEXT        NOT NULL DEFAULT '',
    email                 TEXT        NOT NULL DEFAULT '',
    address               TEXT        NOT NULL DEFAULT '',
    postal_code           TEXT        NOT NULL DEFAULT '',
    bank_name             TEXT        NOT NULL DEFAULT '',
    bank_account_number   TEXT        NOT NULL DEFAULT '',
    iban                  TEXT        NOT NULL DEFAULT '',
    notes                 TEXT        NOT NULL DEFAULT '',
    receivable_account_id BIGINT REFERENCES accounts (id),
    payable_account_id    BIGINT REFERENCES accounts (id),
    is_active             BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT parties_code_unique UNIQUE (company_id, code),
    CHECK (is_customer OR is_supplier OR is_other)
);
CREATE INDEX parties_name ON parties (company_id, lower(name));

CREATE TABLE journal_entries (
    id             BIGSERIAL PRIMARY KEY,
    company_id     BIGINT         NOT NULL REFERENCES companies (id),
    fiscal_year_id BIGINT         NOT NULL REFERENCES fiscal_years (id),
    number         BIGINT         NOT NULL CHECK (number > 0),
    date           DATE           NOT NULL,
    description    TEXT           NOT NULL DEFAULT '',
    reference      TEXT           NOT NULL DEFAULT '',
    status         TEXT           NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    -- Origin of the entry: manual, sales_invoice, purchase_invoice, treasury, expense, stock, opening, closing, reversal.
    source_type    TEXT           NOT NULL DEFAULT 'manual',
    source_id      BIGINT,
    reversal_of_id BIGINT REFERENCES journal_entries (id),
    reversed_by_id BIGINT REFERENCES journal_entries (id),
    total_debit    NUMERIC(20, 2) NOT NULL DEFAULT 0,
    total_credit   NUMERIC(20, 2) NOT NULL DEFAULT 0,
    posted_at      TIMESTAMPTZ,
    posted_by      BIGINT REFERENCES users (id),
    cancelled_at   TIMESTAMPTZ,
    cancelled_by   BIGINT REFERENCES users (id),
    created_by     BIGINT REFERENCES users (id),
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT journal_entries_number_unique UNIQUE (company_id, fiscal_year_id, number),
    -- A posted (or once-posted, now cancelled) entry must always be balanced.
    CONSTRAINT journal_entries_balanced CHECK (posted_at IS NULL OR total_debit = total_credit),
    CHECK (status = 'draft' OR posted_at IS NOT NULL OR status = 'cancelled')
);
CREATE INDEX journal_entries_company_date ON journal_entries (company_id, date, number);
CREATE INDEX journal_entries_source ON journal_entries (source_type, source_id);

CREATE TABLE journal_entry_lines (
    id             BIGSERIAL PRIMARY KEY,
    entry_id       BIGINT         NOT NULL REFERENCES journal_entries (id) ON DELETE CASCADE,
    line_no        INT            NOT NULL,
    account_id     BIGINT         NOT NULL REFERENCES accounts (id),
    party_id       BIGINT REFERENCES parties (id),
    cost_center_id BIGINT REFERENCES cost_centers (id),
    description    TEXT           NOT NULL DEFAULT '',
    debit          NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (debit >= 0),
    credit         NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (credit >= 0),
    CHECK ((debit = 0) <> (credit = 0))
);
CREATE INDEX journal_entry_lines_entry ON journal_entry_lines (entry_id);
CREATE INDEX journal_entry_lines_account ON journal_entry_lines (account_id);
CREATE INDEX journal_entry_lines_party ON journal_entry_lines (party_id) WHERE party_id IS NOT NULL;
CREATE INDEX journal_entry_lines_cost_center ON journal_entry_lines (cost_center_id) WHERE cost_center_id IS NOT NULL;

-- Posted lines are immutable: the only permitted change to a posted entry is cancellation.
CREATE FUNCTION protect_posted_lines() RETURNS trigger AS $$
DECLARE
    posted TIMESTAMPTZ;
BEGIN
    SELECT posted_at INTO posted FROM journal_entries WHERE id = COALESCE(OLD.entry_id, NEW.entry_id);
    IF posted IS NOT NULL THEN
        RAISE EXCEPTION 'posted journal entry lines are immutable' USING ERRCODE = 'P0001', CONSTRAINT = 'posted_entry_immutable';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER journal_entry_lines_protect
    BEFORE UPDATE OR DELETE ON journal_entry_lines
    FOR EACH ROW EXECUTE FUNCTION protect_posted_lines();

CREATE FUNCTION protect_posted_entries() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.posted_at IS NOT NULL THEN
            RAISE EXCEPTION 'posted journal entries cannot be deleted' USING ERRCODE = 'P0001', CONSTRAINT = 'posted_entry_immutable';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.posted_at IS NOT NULL AND (
        NEW.date <> OLD.date OR NEW.total_debit <> OLD.total_debit OR NEW.total_credit <> OLD.total_credit
        OR NEW.posted_at IS DISTINCT FROM OLD.posted_at OR NEW.number <> OLD.number) THEN
        RAISE EXCEPTION 'posted journal entries are immutable' USING ERRCODE = 'P0001', CONSTRAINT = 'posted_entry_immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER journal_entries_protect
    BEFORE UPDATE OR DELETE ON journal_entries
    FOR EACH ROW EXECUTE FUNCTION protect_posted_entries();

-- At commit time, every posted entry's lines must match its (balanced) totals.
CREATE FUNCTION check_entry_lines_balanced() RETURNS trigger AS $$
DECLARE
    d NUMERIC;
    c NUMERIC;
    n INT;
BEGIN
    IF NEW.posted_at IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT COALESCE(SUM(debit), 0), COALESCE(SUM(credit), 0), COUNT(*) INTO d, c, n
    FROM journal_entry_lines WHERE entry_id = NEW.id;
    IF n < 2 OR d <> c OR d <> NEW.total_debit OR c <> NEW.total_credit THEN
        RAISE EXCEPTION 'journal entry % is not balanced', NEW.id USING ERRCODE = 'P0001', CONSTRAINT = 'journal_entries_balanced';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER journal_entries_lines_balanced
    AFTER INSERT OR UPDATE ON journal_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_entry_lines_balanced();
