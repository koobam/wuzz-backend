-- catalog: where a banner is drawn. The home carousel and the login screen's each read their own list, so a banner for one is never
-- on the other. Existing banners were all made for the home carousel.

-- +goose Up
ALTER TABLE banners ADD COLUMN placement text NOT NULL DEFAULT 'home';
ALTER TABLE banners ADD CONSTRAINT banners_placement CHECK (placement IN ('home', 'login'));

-- +goose Down
ALTER TABLE banners DROP CONSTRAINT banners_placement;
ALTER TABLE banners DROP COLUMN placement;
