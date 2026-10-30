-- catalog: how tax is added to a service's price. Like Reddog's menu items it is a type, not a stored rate: 'none' (the price is
-- all the customer pays) or 'ppn' (value-added tax, whose rate the checkout reads from configuration). Every service that
-- existed before this column is sold without tax, so they are 'none' explicitly rather than unknown.

-- +goose Up
ALTER TABLE services
    ADD COLUMN tax_type text NOT NULL DEFAULT 'none',
    ADD CONSTRAINT services_tax_type_known CHECK (tax_type IN ('none', 'ppn'));

-- +goose Down
ALTER TABLE services
    DROP CONSTRAINT IF EXISTS services_tax_type_known,
    DROP COLUMN IF EXISTS tax_type;
