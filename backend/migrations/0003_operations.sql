-- Operations: products, inventory, invoices, treasury, expenses, attachments.

CREATE TABLE product_categories (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT      NOT NULL REFERENCES companies (id),
    name        TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT product_categories_name_unique UNIQUE (company_id, name)
);

CREATE TABLE products (
    id              BIGSERIAL PRIMARY KEY,
    company_id      BIGINT         NOT NULL REFERENCES companies (id),
    kind            TEXT           NOT NULL DEFAULT 'goods' CHECK (kind IN ('goods', 'service')),
    sku             TEXT           NOT NULL CHECK (length(trim(sku)) > 0),
    name            TEXT           NOT NULL CHECK (length(trim(name)) > 0),
    name_en         TEXT           NOT NULL DEFAULT '',
    unit            TEXT           NOT NULL DEFAULT '',
    category_id     BIGINT REFERENCES product_categories (id),
    purchase_price  NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (purchase_price >= 0),
    sales_price     NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (sales_price >= 0),
    tax_rate        NUMERIC(5, 2)  NOT NULL DEFAULT 0 CHECK (tax_rate BETWEEN 0 AND 100),
    track_inventory BOOLEAN        NOT NULL DEFAULT TRUE,
    is_active       BOOLEAN        NOT NULL DEFAULT TRUE,
    description     TEXT           NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT products_sku_unique UNIQUE (company_id, sku),
    CHECK (kind = 'goods' OR NOT track_inventory)
);
CREATE INDEX products_name ON products (company_id, lower(name));

CREATE TABLE warehouses (
    id         BIGSERIAL PRIMARY KEY,
    company_id BIGINT      NOT NULL REFERENCES companies (id),
    code       TEXT        NOT NULL,
    name       TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    address    TEXT        NOT NULL DEFAULT '',
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    is_default BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT warehouses_code_unique UNIQUE (company_id, code)
);

-- Current on-hand quantity and value per product/warehouse (moving average cost).
CREATE TABLE stock_balances (
    product_id   BIGINT         NOT NULL REFERENCES products (id),
    warehouse_id BIGINT         NOT NULL REFERENCES warehouses (id),
    quantity     NUMERIC(18, 3) NOT NULL DEFAULT 0,
    value        NUMERIC(20, 2) NOT NULL DEFAULT 0,
    PRIMARY KEY (product_id, warehouse_id)
);

CREATE TABLE stock_documents (
    id                 BIGSERIAL PRIMARY KEY,
    company_id         BIGINT      NOT NULL REFERENCES companies (id),
    kind               TEXT        NOT NULL CHECK (kind IN ('receipt', 'issue', 'transfer', 'adjustment')),
    number             BIGINT      NOT NULL,
    date               DATE        NOT NULL,
    warehouse_id       BIGINT      NOT NULL REFERENCES warehouses (id),
    to_warehouse_id    BIGINT REFERENCES warehouses (id),
    counter_account_id BIGINT REFERENCES accounts (id),
    description        TEXT        NOT NULL DEFAULT '',
    reference          TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    journal_entry_id   BIGINT REFERENCES journal_entries (id),
    created_by         BIGINT REFERENCES users (id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    posted_at          TIMESTAMPTZ,
    cancelled_at       TIMESTAMPTZ,
    CONSTRAINT stock_documents_number_unique UNIQUE (company_id, kind, number),
    CHECK (kind <> 'transfer' OR (to_warehouse_id IS NOT NULL AND to_warehouse_id <> warehouse_id))
);

CREATE TABLE stock_document_lines (
    id          BIGSERIAL PRIMARY KEY,
    document_id BIGINT         NOT NULL REFERENCES stock_documents (id) ON DELETE CASCADE,
    line_no     INT            NOT NULL,
    product_id  BIGINT         NOT NULL REFERENCES products (id),
    quantity    NUMERIC(18, 3) NOT NULL,
    unit_cost   NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (unit_cost >= 0),
    description TEXT           NOT NULL DEFAULT ''
);

-- Immutable stock ledger. quantity and value are signed (+ in, - out).
CREATE TABLE stock_movements (
    id           BIGSERIAL PRIMARY KEY,
    company_id   BIGINT         NOT NULL REFERENCES companies (id),
    product_id   BIGINT         NOT NULL REFERENCES products (id),
    warehouse_id BIGINT         NOT NULL REFERENCES warehouses (id),
    date         DATE           NOT NULL,
    quantity     NUMERIC(18, 3) NOT NULL CHECK (quantity <> 0),
    unit_cost    NUMERIC(20, 2) NOT NULL DEFAULT 0,
    value        NUMERIC(20, 2) NOT NULL,
    source_type  TEXT           NOT NULL,
    source_id    BIGINT         NOT NULL,
    description  TEXT           NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_product ON stock_movements (product_id, warehouse_id, date, id);
CREATE INDEX stock_movements_source ON stock_movements (source_type, source_id);

-- Sales and purchase documents share one structure; side + kind distinguish them.
CREATE TABLE invoices (
    id                 BIGSERIAL PRIMARY KEY,
    company_id         BIGINT         NOT NULL REFERENCES companies (id),
    side               TEXT           NOT NULL CHECK (side IN ('sales', 'purchase')),
    kind               TEXT           NOT NULL CHECK (kind IN ('quotation', 'invoice', 'return')),
    number             BIGINT         NOT NULL CHECK (number > 0),
    date               DATE           NOT NULL,
    due_date           DATE,
    party_id           BIGINT         NOT NULL REFERENCES parties (id),
    warehouse_id       BIGINT REFERENCES warehouses (id),
    cost_center_id     BIGINT REFERENCES cost_centers (id),
    related_invoice_id BIGINT REFERENCES invoices (id),
    reference          TEXT           NOT NULL DEFAULT '',
    notes              TEXT           NOT NULL DEFAULT '',
    status             TEXT           NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    subtotal           NUMERIC(20, 2) NOT NULL DEFAULT 0,
    discount_total     NUMERIC(20, 2) NOT NULL DEFAULT 0,
    tax_total          NUMERIC(20, 2) NOT NULL DEFAULT 0,
    total              NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (total >= 0),
    paid_amount        NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (paid_amount >= 0),
    journal_entry_id   BIGINT REFERENCES journal_entries (id),
    created_by         BIGINT REFERENCES users (id),
    created_at         TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ    NOT NULL DEFAULT now(),
    posted_at          TIMESTAMPTZ,
    cancelled_at       TIMESTAMPTZ,
    CONSTRAINT invoices_number_unique UNIQUE (company_id, side, kind, number),
    CHECK (side = 'sales' OR kind <> 'quotation'),
    CHECK (paid_amount <= total)
);
CREATE INDEX invoices_party ON invoices (party_id);
CREATE INDEX invoices_company_date ON invoices (company_id, side, kind, date);

CREATE TABLE invoice_items (
    id          BIGSERIAL PRIMARY KEY,
    invoice_id  BIGINT         NOT NULL REFERENCES invoices (id) ON DELETE CASCADE,
    line_no     INT            NOT NULL,
    product_id  BIGINT REFERENCES products (id),
    description TEXT           NOT NULL DEFAULT '',
    quantity    NUMERIC(18, 3) NOT NULL CHECK (quantity > 0),
    unit        TEXT           NOT NULL DEFAULT '',
    unit_price  NUMERIC(20, 2) NOT NULL CHECK (unit_price >= 0),
    discount    NUMERIC(20, 2) NOT NULL DEFAULT 0 CHECK (discount >= 0),
    tax_rate    NUMERIC(5, 2)  NOT NULL DEFAULT 0 CHECK (tax_rate BETWEEN 0 AND 100),
    net_amount  NUMERIC(20, 2) NOT NULL,
    tax_amount  NUMERIC(20, 2) NOT NULL,
    total       NUMERIC(20, 2) NOT NULL,
    CHECK (discount <= quantity * unit_price)
);
CREATE INDEX invoice_items_invoice ON invoice_items (invoice_id);
CREATE INDEX invoice_items_product ON invoice_items (product_id);

-- Cash boxes and bank accounts. Each has its own ledger account; its balance is the ledger balance.
CREATE TABLE treasury_accounts (
    id               BIGSERIAL PRIMARY KEY,
    company_id       BIGINT         NOT NULL REFERENCES companies (id),
    kind             TEXT           NOT NULL CHECK (kind IN ('cash', 'bank')),
    name             TEXT           NOT NULL CHECK (length(trim(name)) > 0),
    account_id       BIGINT         NOT NULL UNIQUE REFERENCES accounts (id),
    bank_name        TEXT           NOT NULL DEFAULT '',
    branch           TEXT           NOT NULL DEFAULT '',
    account_number   TEXT           NOT NULL DEFAULT '',
    iban             TEXT           NOT NULL DEFAULT '',
    card_number      TEXT           NOT NULL DEFAULT '',
    account_holder   TEXT           NOT NULL DEFAULT '',
    opening_balance  NUMERIC(20, 2) NOT NULL DEFAULT 0,
    opening_date     DATE,
    opening_entry_id BIGINT REFERENCES journal_entries (id),
    description      TEXT           NOT NULL DEFAULT '',
    is_active        BOOLEAN        NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE TABLE treasury_transactions (
    id                     BIGSERIAL PRIMARY KEY,
    company_id             BIGINT         NOT NULL REFERENCES companies (id),
    kind                   TEXT           NOT NULL CHECK (kind IN ('receipt', 'payment', 'transfer')),
    number                 BIGINT         NOT NULL,
    date                   DATE           NOT NULL,
    treasury_account_id    BIGINT         NOT NULL REFERENCES treasury_accounts (id),
    to_treasury_account_id BIGINT REFERENCES treasury_accounts (id),
    party_id               BIGINT REFERENCES parties (id),
    counter_account_id     BIGINT REFERENCES accounts (id),
    cost_center_id         BIGINT REFERENCES cost_centers (id),
    amount                 NUMERIC(20, 2) NOT NULL CHECK (amount > 0),
    description            TEXT           NOT NULL DEFAULT '',
    reference              TEXT           NOT NULL DEFAULT '',
    status                 TEXT           NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'cancelled')),
    journal_entry_id       BIGINT REFERENCES journal_entries (id),
    created_by             BIGINT REFERENCES users (id),
    created_at             TIMESTAMPTZ    NOT NULL DEFAULT now(),
    cancelled_at           TIMESTAMPTZ,
    CONSTRAINT treasury_transactions_number_unique UNIQUE (company_id, kind, number),
    CHECK (kind <> 'transfer' OR (to_treasury_account_id IS NOT NULL AND to_treasury_account_id <> treasury_account_id)),
    CHECK (kind = 'transfer' OR party_id IS NOT NULL OR counter_account_id IS NOT NULL)
);
CREATE INDEX treasury_transactions_account ON treasury_transactions (treasury_account_id, date);
CREATE INDEX treasury_transactions_party ON treasury_transactions (party_id);

-- Settlement of invoices by payments (transaction_id) or by credit notes / returns (credit_invoice_id).
CREATE TABLE payment_allocations (
    id                BIGSERIAL PRIMARY KEY,
    company_id        BIGINT         NOT NULL REFERENCES companies (id),
    invoice_id        BIGINT         NOT NULL REFERENCES invoices (id),
    transaction_id    BIGINT REFERENCES treasury_transactions (id),
    credit_invoice_id BIGINT REFERENCES invoices (id),
    amount            NUMERIC(20, 2) NOT NULL CHECK (amount > 0),
    created_at        TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CHECK ((transaction_id IS NULL) <> (credit_invoice_id IS NULL))
);
CREATE INDEX payment_allocations_invoice ON payment_allocations (invoice_id);
CREATE INDEX payment_allocations_transaction ON payment_allocations (transaction_id);

CREATE TABLE expense_categories (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT      NOT NULL REFERENCES companies (id),
    name        TEXT        NOT NULL CHECK (length(trim(name)) > 0),
    name_en     TEXT        NOT NULL DEFAULT '',
    account_id  BIGINT      NOT NULL REFERENCES accounts (id),
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT expense_categories_name_unique UNIQUE (company_id, name)
);

CREATE TABLE expenses (
    id                  BIGSERIAL PRIMARY KEY,
    company_id          BIGINT         NOT NULL REFERENCES companies (id),
    number              BIGINT         NOT NULL,
    date                DATE           NOT NULL,
    category_id         BIGINT         NOT NULL REFERENCES expense_categories (id),
    account_id          BIGINT         NOT NULL REFERENCES accounts (id),
    amount              NUMERIC(20, 2) NOT NULL CHECK (amount > 0),
    description         TEXT           NOT NULL DEFAULT '',
    reference           TEXT           NOT NULL DEFAULT '',
    cost_center_id      BIGINT REFERENCES cost_centers (id),
    payment_method      TEXT           NOT NULL CHECK (payment_method IN ('cash', 'bank', 'credit')),
    treasury_account_id BIGINT REFERENCES treasury_accounts (id),
    party_id            BIGINT REFERENCES parties (id),
    generate_entry      BOOLEAN        NOT NULL DEFAULT TRUE,
    status              TEXT           NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    journal_entry_id    BIGINT REFERENCES journal_entries (id),
    created_by          BIGINT REFERENCES users (id),
    created_at          TIMESTAMPTZ    NOT NULL DEFAULT now(),
    posted_at           TIMESTAMPTZ,
    cancelled_at        TIMESTAMPTZ,
    CONSTRAINT expenses_number_unique UNIQUE (company_id, number),
    CHECK (payment_method = 'credit' OR treasury_account_id IS NOT NULL),
    CHECK (payment_method <> 'credit' OR party_id IS NOT NULL)
);
CREATE INDEX expenses_company_date ON expenses (company_id, date);

CREATE TABLE attachments (
    id           BIGSERIAL PRIMARY KEY,
    company_id   BIGINT      NOT NULL REFERENCES companies (id),
    entity_type  TEXT        NOT NULL CHECK (entity_type IN ('journal_entry', 'invoice', 'expense', 'party', 'treasury_transaction', 'stock_document')),
    entity_id    BIGINT      NOT NULL,
    file_name    TEXT        NOT NULL,
    content_type TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL CHECK (size_bytes >= 0),
    storage_key  TEXT        NOT NULL UNIQUE,
    uploaded_by  BIGINT REFERENCES users (id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX attachments_entity ON attachments (company_id, entity_type, entity_id);
