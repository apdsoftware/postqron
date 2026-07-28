package email

import "testing"

func TestTransactionalEventMatrixIsCompleteAndVersioned(t *testing.T) {
	required := []string{
		"f14.welcome.v1", "f14.account_verification_requested.v1",
		"f14.workspace_invitation.v1",
		"f14.account_security.v1", "f14.account_linked.v1",
		"f14.social_reconnect.v1", "f14.approval_requested.v1",
		"f14.collaboration_update.v1", "f14.publication_succeeded.v1",
		"f14.publication_failed.v1", "f14.publication_retry_required.v1",
		"f14.payment_failed.v1", "f14.plan_changed.v1",
		"f14.plan_cancelled.v1", "f14.grace_period.v1",
		"f14.data_export_ready.v1", "f14.deletion_scheduled.v1",
		"f14.deletion_completed.v1", "f14.privacy_request_received.v1",
		"f14.prelaunch_access.v1", "f14.operational_alert.v1",
	}
	events := make(map[string]EventDefinition, len(TransactionalEventMatrix))
	for _, definition := range TransactionalEventMatrix {
		if definition.Event == "" || definition.Producer == "" ||
			definition.Recipient == "" || definition.Priority == "" ||
			definition.LocaleSource == "" || definition.IdempotencyPattern == "" ||
			definition.Responsibility == "" {
			t.Fatalf("incomplete matrix row: %#v", definition)
		}
		if _, duplicate := events[definition.Event]; duplicate {
			t.Fatalf("duplicate event %s", definition.Event)
		}
		if copies := templateCatalog[definition.Template]; len(copies) != len(SupportedLocales) {
			t.Fatalf("%s template %s has %d locales", definition.Event, definition.Template, len(copies))
		}
		events[definition.Event] = definition
	}
	for _, event := range required {
		if _, ok := events[event]; !ok {
			t.Fatalf("required event %s is missing", event)
		}
	}
}
