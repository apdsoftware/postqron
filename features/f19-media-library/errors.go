package medialibrary

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("media asset not found")
	ErrConflict        = errors.New("media asset revision conflict")
	ErrQuotaExceeded   = errors.New("media storage quota exceeded")
	ErrUploadExpired   = errors.New("media upload expired")
	ErrUploadMismatch  = errors.New("uploaded object does not match the reservation")
	ErrAssetArchived   = errors.New("media asset is archived")
	ErrAssetInUse      = errors.New("media asset is referenced by a draft")
)
