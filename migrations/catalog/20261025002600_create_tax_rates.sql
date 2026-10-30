-- catalog: the rate of each tax type a service can take, in basis points (1100 = 11%). A service stores its price BEFORE tax and
-- its tax type, never the tax itself: the price after tax is derived from them, so a rate change reprices every service of
-- that type and the two numbers can never drift. 'none' is a type like any other, with a rate of 0.

-- +goose Up
CREATE TABLE tax_rates (
    tax_type  text PRIMARY KEY,
    rate_bps  integer NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tax_rates_type_known CHECK (tax_type IN ('none', 'ppn')),
    CONSTRAINT tax_rates_rate_range CHECK (rate_bps BETWEEN 0 AND 10000),
    CONSTRAINT tax_rates_none_is_zero CHECK (tax_type <> 'none' OR rate_bps = 0)
);
INSERT INTO tax_rates (tax_type, rate_bps) VALUES ('none', 0), ('ppn', 1100);

-- +goose Down
DROP TABLE IF EXISTS tax_rates;
