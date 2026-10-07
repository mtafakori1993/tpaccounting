-- Check management: received and issued checks with a status state machine.
-- Also adds the analysis dimension kind to cost centers (department / project)
-- and the posting rules for checks and payroll to existing companies.

-- Cost centers double as departments and projects; journal lines keep a single
-- cost_center_id, so the ledger itself is unchanged.
ALTER TABLE cost_centers
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'cost_center' CHECK (kind IN ('cost_center', 'department', 'project'));
CREATE INDEX cost_centers_kind ON cost_centers (company_id, kind);

-- National catalogue of Iranian banks (shared by all companies).
CREATE TABLE banks (
    id        BIGSERIAL PRIMARY KEY,
    code      TEXT    NOT NULL UNIQUE,
    name      TEXT    NOT NULL,
    name_en   TEXT    NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

INSERT INTO banks (code, name, name_en) VALUES
    ('melli', 'بانک ملی ایران', 'Bank Melli Iran'),
    ('sepah', 'بانک سپه', 'Bank Sepah'),
    ('mellat', 'بانک ملت', 'Bank Mellat'),
    ('tejarat', 'بانک تجارت', 'Tejarat Bank'),
    ('saderat', 'بانک صادرات ایران', 'Bank Saderat Iran'),
    ('keshavarzi', 'بانک کشاورزی', 'Bank Keshavarzi'),
    ('maskan', 'بانک مسکن', 'Bank Maskan'),
    ('refah', 'بانک رفاه کارگران', 'Refah Bank'),
    ('post', 'پست بانک ایران', 'Post Bank of Iran'),
    ('tosee_saderat', 'بانک توسعه صادرات', 'Export Development Bank'),
    ('sanat_madan', 'بانک صنعت و معدن', 'Bank of Industry and Mine'),
    ('parsian', 'بانک پارسیان', 'Parsian Bank'),
    ('pasargad', 'بانک پاسارگاد', 'Bank Pasargad'),
    ('saman', 'بانک سامان', 'Saman Bank'),
    ('eghtesad_novin', 'بانک اقتصاد نوین', 'EN Bank'),
    ('karafarin', 'بانک کارآفرین', 'Karafarin Bank'),
    ('sina', 'بانک سینا', 'Sina Bank'),
    ('sarmayeh', 'بانک سرمایه', 'Sarmayeh Bank'),
    ('shahr', 'بانک شهر', 'Shahr Bank'),
    ('dey', 'بانک دی', 'Dey Bank'),
    ('ayandeh', 'بانک آینده', 'Ayandeh Bank'),
    ('khavarmianeh', 'بانک خاورمیانه', 'Middle East Bank'),
    ('gardeshgari', 'بانک گردشگری', 'Tourism Bank'),
    ('iran_zamin', 'بانک ایران زمین', 'Iran Zamin Bank'),
    ('resalat', 'بانک قرض‌الحسنه رسالت', 'Resalat Bank'),
    ('mehr_iran', 'بانک قرض‌الحسنه مهر ایران', 'Mehr Iran Bank'),
    ('tosee_taavon', 'بانک توسعه تعاون', 'Cooperative Development Bank'),
    ('other', 'سایر', 'Other');

CREATE TABLE checks (
    id                      BIGSERIAL PRIMARY KEY,
    company_id              BIGINT         NOT NULL REFERENCES companies (id),
    check_type              TEXT           NOT NULL CHECK (check_type IN ('received', 'issued')),
    -- Internal register number, sequential per type.
    number                  BIGINT         NOT NULL CHECK (number > 0),
    check_number            TEXT           NOT NULL CHECK (length(trim(check_number)) > 0),
    serial_number           TEXT           NOT NULL DEFAULT '',
    -- 16-digit national unique check identifier (شناسه صیادی).
    sayadi_id               TEXT           NOT NULL DEFAULT '' CHECK (sayadi_id = '' OR sayadi_id ~ '^[0-9]{16}$'),
    bank_id                 BIGINT REFERENCES banks (id),
    bank_name               TEXT           NOT NULL DEFAULT '',
    branch                  TEXT           NOT NULL DEFAULT '',
    account_number          TEXT           NOT NULL DEFAULT '',
    account_holder          TEXT           NOT NULL DEFAULT '',
    party_id                BIGINT         NOT NULL REFERENCES parties (id),
    -- Overrides the party's receivable/payable account.
    counter_account_id      BIGINT REFERENCES accounts (id),
    amount                  NUMERIC(20, 2) NOT NULL CHECK (amount > 0),
    currency                TEXT           NOT NULL DEFAULT 'IRR',
    issue_date              DATE           NOT NULL,
    due_date                DATE           NOT NULL,
    received_date           DATE,
    -- Issued: the bank account the check is drawn on. Received: where it was deposited/cashed.
    treasury_account_id     BIGINT REFERENCES treasury_accounts (id),
    -- Received checks endorsed to another party.
    holder_party_id         BIGINT REFERENCES parties (id),
    invoice_id              BIGINT REFERENCES invoices (id),
    treasury_transaction_id BIGINT REFERENCES treasury_transactions (id),
    project_id              BIGINT REFERENCES cost_centers (id),
    description             TEXT           NOT NULL DEFAULT '',
    notes                   TEXT           NOT NULL DEFAULT '',
    status                  TEXT           NOT NULL,
    status_date             DATE,
    replaced_by_id          BIGINT REFERENCES checks (id),
    journal_entry_id        BIGINT REFERENCES journal_entries (id),
    created_by              BIGINT REFERENCES users (id),
    updated_by              BIGINT REFERENCES users (id),
    created_at              TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ    NOT NULL DEFAULT now(),
    cancelled_at            TIMESTAMPTZ,
    cancelled_by            BIGINT REFERENCES users (id),
    CONSTRAINT checks_number_unique UNIQUE (company_id, check_type, number),
    CONSTRAINT checks_status_valid CHECK (
        (check_type = 'received' AND status IN ('received', 'in_custody', 'deposited', 'cleared', 'transferred', 'returned', 'cancelled', 'lost'))
        OR (check_type = 'issued' AND status IN ('draft', 'issued', 'delivered', 'cleared', 'returned', 'cancelled', 'replaced'))),
    CHECK (check_type = 'received' OR treasury_account_id IS NOT NULL),
    CHECK (check_type = 'issued' OR received_date IS NOT NULL),
    CHECK (due_date >= issue_date),
    CHECK (replaced_by_id IS NULL OR replaced_by_id <> id)
);
CREATE INDEX checks_due ON checks (company_id, due_date);
CREATE INDEX checks_status ON checks (company_id, check_type, status);
CREATE INDEX checks_party ON checks (party_id);
CREATE INDEX checks_bank ON checks (company_id, bank_id);
-- A live check can only be registered once.
CREATE UNIQUE INDEX checks_sayadi_unique ON checks (company_id, sayadi_id)
    WHERE sayadi_id <> '' AND status NOT IN ('cancelled', 'replaced');
CREATE UNIQUE INDEX checks_issued_unique ON checks (company_id, treasury_account_id, check_number)
    WHERE check_type = 'issued' AND status NOT IN ('cancelled', 'replaced');
CREATE UNIQUE INDEX checks_received_unique ON checks (company_id, COALESCE(bank_id, 0), lower(bank_name), account_number, check_number)
    WHERE check_type = 'received' AND status <> 'cancelled';

-- Every status change, with the journal entries it produced. Never updated.
CREATE TABLE check_transitions (
    id                  BIGSERIAL PRIMARY KEY,
    company_id          BIGINT      NOT NULL REFERENCES companies (id),
    check_id            BIGINT      NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    action              TEXT        NOT NULL,
    from_status         TEXT,
    to_status           TEXT        NOT NULL,
    date                DATE        NOT NULL,
    treasury_account_id BIGINT REFERENCES treasury_accounts (id),
    party_id            BIGINT REFERENCES parties (id),
    journal_entry_id    BIGINT REFERENCES journal_entries (id),
    reversal_entry_ids  BIGINT[]    NOT NULL DEFAULT '{}',
    reason              TEXT        NOT NULL DEFAULT '',
    created_by          BIGINT REFERENCES users (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX check_transitions_check ON check_transitions (check_id, id);

-- Checks can settle invoices like receipts and payments do.
ALTER TABLE payment_allocations ADD COLUMN check_id BIGINT REFERENCES checks (id);
ALTER TABLE payment_allocations DROP CONSTRAINT payment_allocations_check;
ALTER TABLE payment_allocations ADD CONSTRAINT payment_allocations_source CHECK (num_nonnulls(transaction_id, credit_invoice_id, check_id) = 1);
CREATE INDEX payment_allocations_check ON payment_allocations (check_id) WHERE check_id IS NOT NULL;

ALTER TABLE attachments DROP CONSTRAINT attachments_entity_type_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_entity_type_check CHECK (entity_type IN (
    'journal_entry', 'invoice', 'expense', 'party', 'treasury_transaction', 'stock_document', 'check', 'employee', 'payroll_period', 'budget'));

-- Default accounts used by checks and payroll for companies created before this
-- release. Only added when the parent exists and the code is free.
INSERT INTO accounts (company_id, parent_id, code, name, name_en, type, level)
SELECT p.company_id, p.id, v.code, v.name, v.name_en, p.type, p.level + 1
FROM (VALUES
    ('12', '1204', 'اسناد در جریان وصول', 'Checks in collection'),
    ('12', '1205', 'مساعده و وام کارکنان', 'Employee advances and loans'),
    ('32', '3203', 'مالیات حقوق پرداختنی', 'Payroll tax payable'),
    ('81', '8110', 'حق بیمه سهم کارفرما', 'Employer social security')
) AS v (parent, code, name, name_en)
JOIN accounts p ON p.code = v.parent AND p.level < 4
WHERE NOT EXISTS (SELECT 1 FROM accounts x WHERE x.company_id = p.company_id AND x.code = v.code)
  AND NOT EXISTS (SELECT 1 FROM journal_entry_lines l WHERE l.account_id = p.id);
UPDATE accounts a SET path = p.path || a.id || '/' FROM accounts p WHERE a.parent_id = p.id AND a.path = '';

INSERT INTO account_mappings (company_id, key, account_id)
SELECT a.company_id, v.key, a.id
FROM (VALUES
    ('checks_receivable', '1202'), ('checks_in_collection', '1204'), ('checks_payable', '3102'),
    ('payroll_salary_expense', '8101'), ('payroll_employer_expense', '8110'), ('payroll_salaries_payable', '3301'),
    ('payroll_insurance_payable', '3302'), ('payroll_tax_payable', '3203'), ('payroll_other_deductions', '1205')
) AS v (key, code)
JOIN accounts a ON a.code = v.code AND a.is_active AND NOT EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)
ON CONFLICT (company_id, key) DO NOTHING;

-- Permissions.
INSERT INTO permissions (code, module, action)
SELECT 'checks.' || a, 'checks', a FROM unnest(ARRAY['view', 'create', 'edit', 'post', 'cancel', 'delete', 'export']) AS a;

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, p.code FROM roles r JOIN permissions p ON p.module = 'checks'
WHERE r.code IN ('admin', 'accounting_manager')
   OR (r.code = 'accountant' AND p.action IN ('view', 'create', 'edit', 'post', 'delete', 'export'))
   OR (r.code = 'viewer' AND p.action IN ('view', 'export'))
ON CONFLICT DO NOTHING;
