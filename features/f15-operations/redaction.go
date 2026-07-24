package operations

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var (
	emailPattern     = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	bearerPattern    = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	connectionString = regexp.MustCompile(`(?i)\b(postgres(?:ql)?|redis|https?)://([^/@\s:]+):([^/@\s]+)@`)
	querySecret      = regexp.MustCompile(`(?i)(access_token|refresh_token|token|secret|password|code)=([^&\s]+)`)
)

var sensitiveKeyFragments = []string{
	"authorization",
	"cookie",
	"password",
	"passwd",
	"secret",
	"token",
	"apikey",
	"privatekey",
	"credential",
	"email",
	"phone",
	"address",
	"caption",
	"content",
	"messagebody",
	"mediaurl",
}

// RedactingHandler applies data minimisation before a slog record reaches its
// destination. It redacts known sensitive fields and common secret/PII shapes.
type RedactingHandler struct {
	next   slog.Handler
	groups []string
}

func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	return &RedactingHandler{next: next}
}

func (handler *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, sanitizeString(record.Message), record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		redacted.AddAttrs(redactAttr(attribute))
		return true
	})
	return handler.next.Handle(ctx, redacted)
}

func (handler *RedactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		redacted = append(redacted, redactAttr(attribute))
	}
	return &RedactingHandler{
		next:   handler.next.WithAttrs(redacted),
		groups: append([]string(nil), handler.groups...),
	}
}

func (handler *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{
		next:   handler.next.WithGroup(name),
		groups: append(append([]string(nil), handler.groups...), name),
	}
}

func redactAttr(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if sensitiveKey(attribute.Key) {
		return slog.String(attribute.Key, RedactedValue)
	}

	switch attribute.Value.Kind() {
	case slog.KindGroup:
		group := attribute.Value.Group()
		redacted := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			redacted = append(redacted, redactAttr(child))
		}
		return slog.Group(attribute.Key, attrsToAny(redacted)...)
	case slog.KindString:
		return slog.String(attribute.Key, sanitizeString(attribute.Value.String()))
	case slog.KindAny:
		return slog.String(attribute.Key, sanitizeString(fmt.Sprint(attribute.Value.Any())))
	default:
		return attribute
	}
}

func attrsToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index, attribute := range attributes {
		values[index] = attribute
	}
	return values
}

func sensitiveKey(key string) bool {
	normalised := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(normalised, fragment) {
			return true
		}
	}
	return false
}

func sanitizeString(value string) string {
	value = emailPattern.ReplaceAllString(value, RedactedValue)
	value = bearerPattern.ReplaceAllString(value, RedactedValue)
	value = connectionString.ReplaceAllString(value, `${1}://`+RedactedValue+`@`)
	value = querySecret.ReplaceAllString(value, `${1}=`+url.QueryEscape(RedactedValue))
	return value
}
