CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS categories (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (id, user_id),
    UNIQUE (user_id, name)
);

CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id UUID,
    kind TEXT NOT NULL CHECK (kind IN ('income', 'expense')),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    currency CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    occurred_on DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (category_id, user_id) REFERENCES categories(id, user_id)
);
CREATE INDEX IF NOT EXISTS transactions_user_date_idx
    ON transactions (user_id, occurred_on DESC, id);
CREATE INDEX IF NOT EXISTS transactions_user_kind_date_idx
    ON transactions (user_id, kind, occurred_on DESC);
CREATE INDEX IF NOT EXISTS transactions_user_category_date_idx
    ON transactions (user_id, category_id, occurred_on DESC);

CREATE TABLE IF NOT EXISTS budgets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id UUID,
    month DATE NOT NULL CHECK (EXTRACT(DAY FROM month) = 1),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    currency CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (category_id, user_id) REFERENCES categories(id, user_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS budgets_overall_unique
    ON budgets (user_id, month, currency) WHERE category_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS budgets_category_unique
    ON budgets (user_id, month, category_id, currency) WHERE category_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS budgets_user_month_idx ON budgets (user_id, month);
