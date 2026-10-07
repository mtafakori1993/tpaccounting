-- Generic per-company settings (JSON documents), e.g. the PDF letterhead.
CREATE TABLE app_settings (
    company_id BIGINT      NOT NULL REFERENCES companies (id),
    key        TEXT        NOT NULL,
    value      JSONB       NOT NULL,
    updated_by BIGINT REFERENCES users (id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, key)
);
