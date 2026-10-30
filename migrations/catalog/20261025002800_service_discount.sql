-- catalog: a discount on a service, either a fixed amount or a percentage of the price. The service's price (what staff key in)
-- is the LIST price; the discount comes off it first, and tax is added to what is left (list - discount, then tax).
--
-- `discount_kind` is 'none', 'value' or 'percent', and `discount_value` is whole rupiah for 'value' and basis points for
-- 'percent' (1250 = 12.5%, at most 10000 = 100%). The customer app already had `discount` (a float) and `discount_type`
-- ('percentage' | 'fixed' | ''), shown as a label and never used in a price. They are left as they are for the version that is
-- still running during a deploy, are no longer read, and are dropped in a later migration once nothing uses them; what they
-- held is carried over below. The app's JSON keeps `discount` and `discount_type`, now derived from these two columns.

-- +goose Up
ALTER TABLE services
    ADD COLUMN discount_kind  text   NOT NULL DEFAULT 'none',
    ADD COLUMN discount_value bigint NOT NULL DEFAULT 0;

UPDATE services SET
    discount_kind = CASE discount_type WHEN 'percentage' THEN 'percent' WHEN 'fixed' THEN 'value' ELSE 'none' END,
    discount_value = CASE discount_type WHEN 'percentage' THEN round(discount * 100)::bigint WHEN 'fixed' THEN round(discount)::bigint ELSE 0 END;

ALTER TABLE services
    ADD CONSTRAINT services_discount_kind_known CHECK (discount_kind IN ('none', 'value', 'percent')),
    ADD CONSTRAINT services_discount_value_valid CHECK (
        (discount_kind = 'none' AND discount_value = 0)
        OR (discount_kind = 'value' AND discount_value >= 0 AND discount_value <= price)
        OR (discount_kind = 'percent' AND discount_value BETWEEN 0 AND 10000)
    );

-- +goose Down
ALTER TABLE services
    DROP CONSTRAINT IF EXISTS services_discount_value_valid,
    DROP CONSTRAINT IF EXISTS services_discount_kind_known,
    DROP COLUMN IF EXISTS discount_value,
    DROP COLUMN IF EXISTS discount_kind;
