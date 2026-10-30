-- Stock on hand, per outlet and material: the quantity, what it is worth, and the reorder level. A row is only ever changed in
-- the same transaction that writes the movement that changed it (see stock_movements), with the row locked, so concurrent
-- movements never interleave. The average cost is value / qty, an exact fraction that is never stored.

-- +goose Up
CREATE TABLE stock_balances (
    outlet_id   bigint        NOT NULL REFERENCES outlets (id),
    material_id bigint        NOT NULL REFERENCES materials (id) ON DELETE RESTRICT,
    -- In the material's base unit. May be negative: a sale is never blocked by an empty shelf.
    qty         numeric(18,4) NOT NULL DEFAULT 0,
    -- Whole rupiah. Zero whenever qty is zero (see the valuation rules in the domain).
    value       bigint        NOT NULL DEFAULT 0,
    -- Low stock when min_qty > 0 and qty <= min_qty.
    min_qty     numeric(18,4) NOT NULL DEFAULT 0,
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (outlet_id, material_id),
    CONSTRAINT stock_balances_min_not_negative CHECK (min_qty >= 0)
);

CREATE INDEX stock_balances_material_idx ON stock_balances (material_id);

-- +goose Down
DROP TABLE IF EXISTS stock_balances;
