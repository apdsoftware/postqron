package githubhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
)

var segreto = []byte("segreto-del-webhook-di-prova")

// TestFirmaValida verifica il caso legittimo, e lo fa contro un HMAC calcolato
// qui a mano invece che con githubhook.Sign: se la verifica e la firma
// usassero la stessa funzione sbagliata, il test passerebbe comunque.
func TestFirmaValida(t *testing.T) {
	corpo := []byte(`{"ref":"refs/heads/main"}`)

	mac := hmac.New(sha256.New, segreto)
	mac.Write(corpo)
	firma := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := githubhook.VerifySignature(segreto, corpo, firma); err != nil {
		t.Fatalf("firma legittima rifiutata: %v", err)
	}
	if got := githubhook.Sign(segreto, corpo); got != firma {
		t.Errorf("Sign = %q, atteso %q", got, firma)
	}
}

// TestFirmaRifiutata copre tutti i modi in cui una richiesta può non essere
// firmata correttamente. È il cuore di R11: ognuno di questi casi dev'essere un
// rifiuto, e ognuno dev'essere lo *stesso* rifiuto.
func TestFirmaRifiutata(t *testing.T) {
	corpo := []byte(`{"ref":"refs/heads/main","after":"a1b2c3"}`)
	valida := githubhook.Sign(segreto, corpo)

	casi := []struct {
		nome  string
		corpo []byte
		firma string
	}{
		{"firma assente", corpo, ""},
		{"solo spazi", corpo, "   "},
		{"prefisso assente", corpo, strings.TrimPrefix(valida, "sha256=")},
		{"prefisso sbagliato", corpo, "sha1=" + strings.TrimPrefix(valida, "sha256=")},
		{"prefisso in maiuscolo", corpo, "SHA256=" + strings.TrimPrefix(valida, "sha256=")},
		{"esadecimale illeggibile", corpo, "sha256=non-esadecimale"},
		{"firma troncata", corpo, valida[:len(valida)-2]},
		{"firma allungata", corpo, valida + "00"},
		{"un bit diverso", corpo, flip(valida)},
		{"firma di un altro segreto", corpo, githubhook.Sign([]byte("un-altro-segreto-lungo"), corpo)},
		{"corpo alterato dopo la firma", append(corpo, ' '), valida},
		{"corpo troncato dopo la firma", corpo[:len(corpo)-1], valida},
		{"corpo sostituito", []byte(`{"ref":"refs/heads/altro"}`), valida},
		{"corpo vuoto", nil, valida},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			err := githubhook.VerifySignature(segreto, caso.corpo, caso.firma)
			if !errors.Is(err, githubhook.ErrInvalidSignature) {
				t.Fatalf("err = %v, atteso ErrInvalidSignature", err)
			}
		})
	}
}

// TestFirmaSenzaSegretoNonVerificaNiente: senza segreto non esiste una firma
// valida, nemmeno quella che l'HMAC di una chiave vuota produrrebbe. Chi
// riuscisse a leggere il codice non deve poter costruire una richiesta valida
// per un'installazione che il segreto non l'ha configurato.
func TestFirmaSenzaSegretoNonVerificaNiente(t *testing.T) {
	corpo := []byte(`{}`)
	firma := githubhook.Sign(nil, corpo)

	if err := githubhook.VerifySignature(nil, corpo, firma); !errors.Is(err, githubhook.ErrInvalidSignature) {
		t.Fatalf("err = %v, atteso ErrInvalidSignature", err)
	}
}

// TestFirmaAccettaEsadecimaleMaiuscolo: la codifica esadecimale non distingue
// le maiuscole, e rifiutarle sarebbe una fragilità gratuita rispetto a un
// mittente che cambiasse formato.
func TestFirmaAccettaEsadecimaleMaiuscolo(t *testing.T) {
	corpo := []byte(`{"zen":"Non-blocking is better than blocking."}`)
	firma := githubhook.Sign(segreto, corpo)
	maiuscola := "sha256=" + strings.ToUpper(strings.TrimPrefix(firma, "sha256="))

	if err := githubhook.VerifySignature(segreto, corpo, maiuscola); err != nil {
		t.Fatalf("firma legittima in maiuscolo rifiutata: %v", err)
	}
}

// flip cambia un carattere della parte esadecimale della firma.
func flip(firma string) string {
	byteArray := []byte(firma)
	ultimo := len(byteArray) - 1
	if byteArray[ultimo] == '0' {
		byteArray[ultimo] = '1'
	} else {
		byteArray[ultimo] = '0'
	}
	return string(byteArray)
}
