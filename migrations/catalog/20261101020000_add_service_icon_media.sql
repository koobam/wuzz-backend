-- catalog: a service's icon as an uploaded picture. icon_media_id is the picture's id in the media service; icon_url is its address,
-- kept here too so the menu is read without a call to the media service (an address is never reused, so it never goes stale). Both
-- empty means the app draws the service's named `icon` from its own set, as before.

-- +goose Up
ALTER TABLE services
    ADD COLUMN icon_media_id text NOT NULL DEFAULT '',
    ADD COLUMN icon_url      text NOT NULL DEFAULT '';
ALTER TABLE services ADD CONSTRAINT services_icon_media_pair CHECK ((icon_media_id = '') = (icon_url = ''));

-- +goose Down
ALTER TABLE services DROP CONSTRAINT services_icon_media_pair;
ALTER TABLE services
    DROP COLUMN icon_url,
    DROP COLUMN icon_media_id;
