-- release: v1.0.0
-- phase: expand
-- 문의함 기본 스키마
CREATE TABLE inquiries (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject        text NOT NULL,
    body           text NOT NULL,
    customer_email text NOT NULL,
    status         text NOT NULL DEFAULT 'open',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inquiries_status_idx ON inquiries (status, created_at DESC);

CREATE TABLE replies (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inquiry_id uuid NOT NULL REFERENCES inquiries(id) ON DELETE CASCADE,
    author     text NOT NULL,
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE attachments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inquiry_id  uuid NOT NULL REFERENCES inquiries(id) ON DELETE CASCADE,
    name        text NOT NULL,
    size_bytes  bigint NOT NULL,
    sha256      text NOT NULL,
    storage_key text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    username      text PRIMARY KEY,
    password_hash text NOT NULL,
    role          text NOT NULL DEFAULT 'agent'
);
