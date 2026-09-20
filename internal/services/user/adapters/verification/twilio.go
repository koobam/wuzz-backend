// Package verification adapts the user service's Verifications port to an
// SMS provider (Twilio Verify) in production and to a local, no-SMS
// implementation for development.
package verification

import (
	"context"

	"github.com/twilio/twilio-go"
	verify "github.com/twilio/twilio-go/rest/verify/v2"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

type Twilio struct {
	client    *twilio.RestClient
	verifySID string
}

func NewTwilio(cfg config.Twilio) *Twilio {
	client := twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: cfg.AccountSID,
		Password: cfg.AuthToken,
	})
	return &Twilio{client: client, verifySID: cfg.VerifySID}
}

func (t *Twilio) Start(ctx context.Context, phone string) error {
	params := &verify.CreateVerificationParams{}
	params.SetTo("+" + phone)
	params.SetChannel("sms")

	if _, err := t.client.VerifyV2.CreateVerification(t.verifySID, params); err != nil {
		return apperr.Internal("Could not send the verification code").WithCause(err)
	}
	return nil
}

func (t *Twilio) Check(ctx context.Context, phone, code string) (bool, error) {
	params := &verify.CreateVerificationCheckParams{}
	params.SetTo("+" + phone)
	params.SetCode(code)

	resp, err := t.client.VerifyV2.CreateVerificationCheck(t.verifySID, params)
	if err != nil {
		// Twilio answers an unknown/expired verification with an error rather
		// than a "not approved" status — both mean the same thing here.
		return false, nil //nolint:nilerr // deliberate: see comment above
	}
	return resp.Status != nil && *resp.Status == "approved", nil
}
