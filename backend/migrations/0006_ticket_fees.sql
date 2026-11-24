-- Preserve historical order commission and zero fixed fees. New sales snapshot
-- both fee components per ticket so partial refunds reverse the exact charge.
ALTER TABLE tickets
    ADD COLUMN commission_cents BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN ticket_fee_cents BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orders
    ADD COLUMN commission_cents BIGINT NULL DEFAULT NULL,
    ADD COLUMN ticket_fees_cents BIGINT NOT NULL DEFAULT 0;

-- The new standard applies to future sales for existing organisers too.
-- Historical orders retain their original commission_bps and NULL snapshot.
UPDATE organisers SET commission_bps = 350;
