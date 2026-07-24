package statusnotifications

import (
	"regexp"
	"strings"
	"time"
)

const maxDiagnosticLength = 320

var (
	emailPattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`,
	)
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	secretPattern = regexp.MustCompile(
		`(?i)\b(token|secret|password|api[-_ ]?key)\b\s*[:=]\s*[^\s,;]+`,
	)
	querySecretPattern = regexp.MustCompile(
		`(?i)([?&](?:token|secret|password|api[-_]?key)=)[^&#\s]+`,
	)
)

func ClientDiagnostic(source SourceDiagnostic, occurredAt time.Time) Diagnostic {
	code := sanitizeCode(source.Code)
	return Diagnostic{
		Code:      code,
		Message:   usefulMessage(code, source.Detail),
		Retryable: source.Retryable,
		At:        occurredAt.UTC(),
	}
}

func sanitizeCode(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			result.WriteRune(character)
		}
		if result.Len() == 64 {
			break
		}
	}
	if result.Len() == 0 {
		return "publication_failed"
	}
	return result.String()
}

func usefulMessage(code, detail string) string {
	switch code {
	case "rate_limited", "too_many_requests":
		return "Il canale ha temporaneamente limitato le pubblicazioni. Riprova più tardi."
	case "authentication_failed", "permission_revoked", "token_expired":
		return "Il canale deve essere riconnesso prima di riprovare."
	case "invalid_content", "media_rejected", "validation_failed":
		return "Il canale ha rifiutato il contenuto. Controlla testo e media."
	case "timeout", "network_error", "provider_unavailable":
		return "Il canale non è al momento raggiungibile. Puoi riprovare."
	case "command_invalidated":
		return "La programmazione è stata sostituita o annullata."
	}
	safe := Redact(detail)
	if safe == "" || safe == "[redacted]" {
		return "La pubblicazione non è riuscita. Controlla il contenuto e riprova."
	}
	return safe
}

func Redact(value string) string {
	value = strings.TrimSpace(value)
	value = bearerPattern.ReplaceAllString(value, "[redacted]")
	value = secretPattern.ReplaceAllString(value, "$1=[redacted]")
	value = querySecretPattern.ReplaceAllString(value, "$1[redacted]")
	value = emailPattern.ReplaceAllString(value, "[redacted]")
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxDiagnosticLength {
		value = string(runes[:maxDiagnosticLength-1]) + "…"
	}
	return value
}
