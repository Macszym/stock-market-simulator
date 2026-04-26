-- +goose Up
CREATE TABLE bank_stocks (
    name     TEXT   PRIMARY KEY,
    quantity BIGINT NOT NULL CHECK (quantity >= 0)
);

CREATE TABLE wallets (
    id TEXT PRIMARY KEY
);

-- wallet_stocks.stock_name celowo bez FK do bank_stocks(name).
-- POST /stocks nadpisuje cały bank (TRUNCATE+INSERT). Z FK musielibyśmy
-- albo CASCADE (kasowałoby portfele), albo failowałby TRUNCATE.
-- Bank traktujemy jak fluktuujący katalog, portfele jak historię własności.
CREATE TABLE wallet_stocks (
    wallet_id  TEXT   NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    stock_name TEXT   NOT NULL,
    quantity   BIGINT NOT NULL CHECK (quantity >= 0),
    PRIMARY KEY (wallet_id, stock_name)
);

CREATE TABLE audit_log (
    id         BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation  TEXT        NOT NULL CHECK (operation IN ('BUY', 'SELL')),
    wallet_id  TEXT        NOT NULL,
    stock_name TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE wallet_stocks;
DROP TABLE wallets;
DROP TABLE bank_stocks;
