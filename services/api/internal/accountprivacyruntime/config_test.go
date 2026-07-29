package accountprivacyruntime

import "testing"

func TestValidateDownloadBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		value      string
		production bool
		valid      bool
	}{
		{"production https", "https://api.example.test", true, true},
		{"production http rejected", "http://api.example.test", true, false},
		{"development loopback", "http://127.0.0.1:8080", false, true},
		{"development localhost", "http://localhost:8080", false, true},
		{"development remote http rejected", "http://api.example.test", false, false},
		{"credentials rejected", "https://user:pass@api.example.test", false, false},
		{"path rejected", "https://api.example.test/private", false, false},
		{"relative rejected", "/api", false, false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateDownloadBaseURL(test.value, test.production)
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
		})
	}
}

func TestPrivacyAllowedOrigins(t *testing.T) {
	t.Parallel()
	origins, err := privacyAllowedOrigins(
		"https://app.example.test, https://support.example.test",
		"https://api.example.test",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(origins) != 2 {
		t.Fatalf("got %d allowed origins, want two", len(origins))
	}
	if _, ok := origins["https://app.example.test"]; !ok {
		t.Fatal("normalized app origin is missing")
	}
	if _, err := privacyAllowedOrigins(
		"https://app.example.test/path",
		"https://api.example.test",
		true,
	); err == nil {
		t.Fatal("origin with path was accepted")
	}
	if _, err := privacyAllowedOrigins(
		"http://app.example.test",
		"https://api.example.test",
		true,
	); err == nil {
		t.Fatal("HTTP production origin was accepted")
	}
	if _, err := privacyAllowedOrigins(
		"",
		"https://api.example.test",
		true,
	); err == nil {
		t.Fatal("missing production allowlist was accepted")
	}
	developmentOrigins, err := privacyAllowedOrigins(
		"",
		"http://127.0.0.1:8080",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := developmentOrigins["http://127.0.0.1:8080"]; !ok {
		t.Fatal("development loopback fallback is missing")
	}
}
