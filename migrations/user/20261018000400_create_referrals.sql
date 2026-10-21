-- referrals: which customers a customer brought in. The member card's "Teman"
-- counter is the number of rows with that customer as the referrer.
--
-- A customer can be referred at most once (referred_id is unique) and not by
-- themselves. Nothing writes here yet: that needs a referral code at sign-up.

-- +goose Up
CREATE TABLE referrals (
    referrer_id bigint      NOT NULL REFERENCES users (id),
    referred_id bigint      NOT NULL REFERENCES users (id),
    created_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (referrer_id, referred_id),
    CONSTRAINT referrals_not_self CHECK (referrer_id <> referred_id)
);

CREATE UNIQUE INDEX referrals_referred_key ON referrals (referred_id);

-- +goose Down
DROP TABLE IF EXISTS referrals;
