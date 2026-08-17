package mailronix

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChiaveOscurata verifica che la chiave non si stampi mai in chiaro,
// qualunque sia il verbo di formattazione.
func TestChiaveOscurata(t *testing.T) {
	key := APIKey(testKey)

	forme := map[string]string{
		"String":    key.String(),
		"%v":        fmt.Sprintf("%v", key),
		"%s":        fmt.Sprintf("%s", key),
		"%q":        fmt.Sprintf("%q", key),
		"%#v":       fmt.Sprintf("%#v", key),
		"in Config": fmt.Sprintf("%+v", Config{APIKey: key, From: "noreply@postqron.com"}),
	}
	for name, got := range forme {
		if strings.Contains(got, testSecret) {
			t.Errorf("%s espone il segreto: %s", name, got)
		}
		if !strings.Contains(got, KeyPrefix) {
			t.Errorf("%s = %s, atteso il prefisso %q per riconoscere la chiave", name, got, KeyPrefix)
		}
	}
}

func TestChiaveOscurataCasiLimite(t *testing.T) {
	cases := map[APIKey]string{
		"":                 "(nessuna chiave)",
		"   ":              "(nessuna chiave)",
		"mrx_live_abc":     "mrx_live_***",
		"mrx_test_abc":     "mrx_test_***",
		"mrx_live_a_b_c":   "mrx_live_***",
		"senzatrattibassi": "***",
		"solo_uno":         "***",
	}
	for key, want := range cases {
		if got := key.String(); got != want {
			t.Errorf("APIKey(%q).String() = %q, atteso %q", string(key), got, want)
		}
	}
}

// TestChiaveNonFinisceNeiLog è la verifica che conta davvero: si esercitano
// tutti i percorsi d'errore del client e si controlla che il segreto non
// compaia né nei log né nei messaggi d'errore.
func TestChiaveNonFinisceNeiLog(t *testing.T) {
	scenari := map[string]http.HandlerFunc{
		"blocco cloudflare": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "error code: 1010")
		},
		"dominio non verificato": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":"domain_not_verified","message":"non verificato"}}`)
		},
		"rate limit ripetuto": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"troppe"}}`)
		},
		"gateway": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>502</html>")
		},
		// Il caso cattivo: un server che rimanda indietro quello che ha
		// ricevuto, intestazione Authorization compresa. Il corpo di una
		// risposta non è mai fidato, e il frammento che finisce nell'errore
		// nemmeno.
		"eco delle intestazioni": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "richiesta rifiutata: "+r.Header.Get("Authorization"))
		},
		"accodata": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"status":"queued","email_log_id":"id"}`)
		},
	}

	for name, handler := range scenari {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()

			var logged bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

			client := newTestClient(t, server.URL, WithLogger(logger),
				WithSleep(func(context.Context, time.Duration) error { return nil }))
			_, err := client.Send(t.Context(), sampleEmail())

			if strings.Contains(logged.String(), testSecret) {
				t.Errorf("il segreto è finito nei log: %s", logged.String())
			}
			if err != nil && strings.Contains(err.Error(), testSecret) {
				t.Errorf("il segreto è finito nel messaggio d'errore: %s", err.Error())
			}
			// Nemmeno il destinatario, che è un dato personale e nel log non
			// serve (SPEC §5).
			if strings.Contains(logged.String(), "utente@example.com") {
				t.Errorf("il destinatario è finito nei log: %s", logged.String())
			}
		})
	}
}

// TestErroreDiTrasportoRipulisceIlCorpo verifica che il frammento non porti nei
// log caratteri di controllo o righe intere.
func TestErroreDiTrasportoRipulisceIlCorpo(t *testing.T) {
	err := newTransportError(http.StatusBadGateway, []byte("prima riga\nseconda\triga\x00\x07"), nil)
	if want := "prima riga seconda riga"; err.Snippet != want {
		t.Errorf("Snippet = %q, atteso %q", err.Snippet, want)
	}
	if strings.ContainsAny(err.Snippet, "\n\r\t\x00") {
		t.Errorf("il frammento contiene caratteri di controllo: %q", err.Snippet)
	}
}
