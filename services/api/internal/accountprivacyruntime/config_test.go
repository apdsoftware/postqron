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
