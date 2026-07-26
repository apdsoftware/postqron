package appshell

import "testing"

func TestNormalizeOriginsRestrictsCredentialedAppRequests(t *testing.T) {
	origins, err := normalizeOrigins(
		"https://postqron.com, https://postqron.com/,http://localhost:3000",
	)
	if err != nil {
		t.Fatalf("normalizeOrigins() error = %v", err)
	}
	if len(origins) != 2 ||
		origins[0] != "http://localhost:3000" ||
		origins[1] != "https://postqron.com" {
		t.Fatalf("origins = %#v", origins)
	}
	for _, invalid := range []string{
		"*",
		"https://user:password@postqron.com",
		"https://postqron.com/path",
		"javascript:alert(1)",
	} {
		if _, err := normalizeOrigin(invalid); err == nil {
			t.Fatalf("normalizeOrigin(%q) succeeded", invalid)
		}
	}
}
