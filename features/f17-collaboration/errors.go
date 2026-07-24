package collaboration

import "errors"

func publicErrorCode(err error) (string, int) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return "unauthenticated", 401
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrSelfApproval):
		return "forbidden", 403
	case errors.Is(err, ErrNotFound):
		return "not_found", 404
	case errors.Is(err, ErrConflict),
		errors.Is(err, ErrReviewPending),
		errors.Is(err, ErrReviewNotPending),
		errors.Is(err, ErrUnresolvedComment):
		return "conflict", 409
	case errors.Is(err, ErrDraftInvalid):
		return "draft_invalid", 422
	case errors.Is(err, ErrApprovalRequired):
		return "approval_required", 422
	case errors.Is(err, ErrInvalidArgument):
		return "invalid_request", 400
	default:
		return "internal_error", 500
	}
}
