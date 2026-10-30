package domain

import "github.com/koobam/wuzz-backend/internal/pkg/apperr"

// ValidateOwner checks the kind of thing a recipe belongs to.
func ValidateOwner(ownerType string) error {
	switch ownerType {
	case OwnerService, OwnerVariantOption, OwnerModifierOption:
		return nil
	}
	return apperr.Invalid("The owner type must be service, variant_option or modifier_option")
}
