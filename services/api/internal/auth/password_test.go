package auth_test

import (
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// cheapParams sono parametri validi ma economici, per i test che non misurano il
// costo. Restano sopra i minimi che NewHasher impone.
var cheapParams = auth.Argon2idParams{
	Memory:      8 * 1024,
	Time:        1,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

func newTestHasher(t testing.TB, params auth.Argon2idParams) *auth.Hasher {
	t.Helper()
	hasher, err := auth.NewHasher(params)
	if err != nil {
		t.Fatalf("costruzione del Hasher: %v", err)
	}
	return hasher
}

// I parametri di default non sono un dettaglio implementativo: sono la difesa
// contro un attaccante con hardware dedicato, e un abbassamento accidentale non
// deve poter passare la CI in silenzio. I valori sono il minimo raccomandato da
// OWASP per Argon2id.
func TestDefaultParamsRispettanoIlMinimoRaccomandato(t *testing.T) {
	params := auth.DefaultParams
	if params.Memory < 19*1024 {
		t.Errorf("Memory = %d KiB, il minimo raccomandato è 19456", params.Memory)
	}
	if params.Time < 2 {
		t.Errorf("Time = %d, il minimo raccomandato è 2", params.Time)
	}
	if params.SaltLength < 16 {
		t.Errorf("SaltLength = %d, minimo 16", params.SaltLength)
	}
	if params.KeyLength < 32 {
		t.Errorf("KeyLength = %d, minimo 32", params.KeyLength)
	}
	if auth.MinPasswordLength < 12 {
		t.Errorf("MinPasswordLength = %d, minimo 12", auth.MinPasswordLength)
	}
}

func TestNewHasherRifiutaParametriTroppoDeboli(t *testing.T) {
	tests := map[string]auth.Argon2idParams{
		"memoria insufficiente": {Memory: 1024, Time: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		"nessuna passata":       {Memory: 19 * 1024, Time: 0, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		"parallelismo nullo":    {Memory: 19 * 1024, Time: 2, Parallelism: 0, SaltLength: 16, KeyLength: 32},
		"salt corto":            {Memory: 19 * 1024, Time: 2, Parallelism: 1, SaltLength: 8, KeyLength: 32},
		"chiave corta":          {Memory: 19 * 1024, Time: 2, Parallelism: 1, SaltLength: 16, KeyLength: 16},
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := auth.NewHasher(params); err == nil {
				t.Fatal("atteso un errore su parametri troppo deboli")
			}
		})
	}
}

func TestHashEVerify(t *testing.T) {
	hasher := newTestHasher(t, cheapParams)
	const password = "una-password-lunga-abbastanza"

	encoded, err := hasher.Hash(t.Context(), password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	// L'hash è nel formato PHC e dichiara i parametri con cui è stato prodotto:
	// è ciò che permette di alzare il costo in futuro senza migrare la tabella.
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Errorf("formato inatteso: %q", encoded)
	}
	// La password in chiaro non compare nell'hash, per nessuna via.
	if strings.Contains(encoded, password) {
		t.Error("la password compare nell'hash")
	}

	ok, stale, err := hasher.Verify(t.Context(), encoded, password)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("la password corretta non è stata riconosciuta")
	}
	if stale {
		t.Error("l'hash appena prodotto è segnalato come obsoleto")
	}

	ok, _, err = hasher.Verify(t.Context(), encoded, password+"x")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("una password sbagliata è stata accettata")
	}
}

// Due hash della stessa password devono differire: il salt è casuale, e senza
// questa proprietà due utenti con la stessa password sarebbero riconoscibili
// guardando la tabella.
func TestHashUsaUnSaltDiversoOgniVolta(t *testing.T) {
	hasher := newTestHasher(t, cheapParams)
	const password = "la-stessa-password-di-sempre"

	first, err := hasher.Hash(t.Context(), password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := hasher.Hash(t.Context(), password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if first == second {
		t.Fatal("due hash della stessa password sono identici: il salt non è casuale")
	}

	// Entrambi restano verificabili.
	for i, encoded := range []string{first, second} {
		ok, _, err := hasher.Verify(t.Context(), encoded, password)
		if err != nil || !ok {
			t.Errorf("hash %d non verificabile: ok=%v err=%v", i, ok, err)
		}
	}
}

// Un hash prodotto con parametri più deboli va segnalato come da rigenerare: è
// il meccanismo che permette di alzare il costo nel 2028 senza chiedere a nessuno
// di cambiare password.
func TestVerifySegnalaUnHashConParametriObsoleti(t *testing.T) {
	const password = "password-da-rigenerare"

	weak := newTestHasher(t, cheapParams)
	encoded, err := weak.Hash(t.Context(), password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	stronger := cheapParams
	stronger.Memory *= 2
	strong := newTestHasher(t, stronger)

	ok, stale, err := strong.Verify(t.Context(), encoded, password)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("l'hash prodotto con parametri diversi non è più verificabile")
	}
	if !stale {
		t.Error("l'hash con parametri obsoleti non è stato segnalato")
	}

	// Una password sbagliata non deve far scattare la rigenerazione: non c'è
	// nulla da rigenerare, e la password in chiaro non è quella giusta.
	_, stale, err = strong.Verify(t.Context(), encoded, "password-sbagliata-lunga")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if stale {
		t.Error("una verifica fallita ha segnalato l'hash come da rigenerare")
	}
}

// Un hash illeggibile non è un errore del server: è una password sbagliata. Il
// caso esiste perché `users.password_hash` può contenere un valore residuo di
// un'importazione o di un provider esterno, e un 500 su quella riga sarebbe
// anche un modo per distinguerla dalle altre.
func TestVerifyTrattaUnHashIllegibileComePasswordSbagliata(t *testing.T) {
	hasher := newTestHasher(t, cheapParams)

	tests := map[string]string{
		"vuoto":                "",
		"non PHC":              "hash",
		"algoritmo diverso":    "$bcrypt$v=19$m=8192,t=1,p=1$c2FsdHNhbHQ$a2V5",
		"versione diversa":     "$argon2id$v=16$m=8192,t=1,p=1$c2FsdHNhbHQ$a2V5",
		"parametri illegibili": "$argon2id$v=19$m=molti,t=1,p=1$c2FsdHNhbHQ$a2V5",
		"base64 rotto":         "$argon2id$v=19$m=8192,t=1,p=1$!!!$a2V5",
		"campi mancanti":       "$argon2id$v=19$m=8192,t=1,p=1$c2FsdHNhbHQ",
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			ok, stale, err := hasher.Verify(t.Context(), encoded, "una-password-qualunque")
			if err != nil {
				t.Fatalf("atteso nessun errore, ottenuto %v", err)
			}
			if ok || stale {
				t.Errorf("ok=%v stale=%v, attesi entrambi falsi", ok, stale)
			}
		})
	}
}

// La normalizzazione NFKC serve al caso concreto della stessa password digitata
// da tastiere diverse: `é` composto da un code point e `é` composto da due sono
// lo stesso carattere per l'utente, e senza normalizzazione il login
// funzionerebbe da un dispositivo e non dall'altro.
func TestVerifyNormalizzaLeFormeUnicodeEquivalenti(t *testing.T) {
	hasher := newTestHasher(t, cheapParams)

	// Le due stringhe sono scritte con gli escape espliciti: se fossero letterali,
	// l'editor o un `gofmt` potrebbero normalizzarle, e il test non proverebbe
	// niente.
	const composed = "caff\u00e9-macchiato-1234"    // é in un solo code point
	const decomposed = "caffe\u0301-macchiato-1234" // e + accento combinante

	if composed == decomposed {
		t.Fatal("le due forme sono la stessa stringa: il test non proverebbe niente")
	}

	encoded, err := hasher.Hash(t.Context(), composed)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, _, err := hasher.Verify(t.Context(), encoded, decomposed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("la forma decomposta della stessa password non è stata riconosciuta")
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"lunghezza minima", strings.Repeat("a", auth.MinPasswordLength), nil},
		{"un carattere in meno", strings.Repeat("a", auth.MinPasswordLength-1), auth.ErrPasswordTooShort},
		{"vuota", "", auth.ErrPasswordTooShort},
		{"soli spazi", strings.Repeat(" ", auth.MinPasswordLength+2), auth.ErrPasswordBlank},
		{"lunghissima", strings.Repeat("a", 129), auth.ErrPasswordTooLong},
		// Nessuna regola di composizione: una passphrase di parole comuni è
		// migliore di `P@ssw0rd!` e non deve essere rifiutata.
		{"passphrase", "cavallo batteria graffetta", nil},
		// I caratteri multibyte contano come caratteri, non come byte: dodici
		// ideogrammi sono una password di dodici caratteri.
		{"multibyte", strings.Repeat("直", 12), nil},
		{"multibyte troppo corta", strings.Repeat("直", 11), auth.ErrPasswordTooShort},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidatePassword(tc.password)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("atteso nessun errore, ottenuto %v", err)
			}
			if tc.wantErr != nil && err != tc.wantErr {
				t.Fatalf("errore = %v, atteso %v", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"normale", "mario@example.com", "mario@example.com", false},
		// Le maiuscole si conservano: la 0002 memorizza l'indirizzo come
		// l'utente lo ha scritto e verifica l'unicità su lower(email).
		{"maiuscole conservate", "Mario@Example.COM", "Mario@Example.COM", false},
		{"spazi ai bordi", "  mario@example.com \t", "mario@example.com", false},
		{"vuoto", "", "", true},
		{"senza chiocciola", "mario.example.com", "", true},
		{"senza dominio", "mario@", "", true},
		{"senza punto nel dominio", "mario@example", "", true},
		{"due chiocciole", "mario@@example.com", "", true},
		{"con spazio interno", "mar io@example.com", "", true},
		{"troppo lungo", strings.Repeat("a", 250) + "@example.com", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := auth.NormalizeEmail(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("atteso un errore per %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, atteso %q", got, tc.want)
			}
		})
	}
}

// BenchmarkHash misura il costo reale dei parametri di default.
//
// È il numero da guardare quando si rivedono: la regola pratica è fra 100 e
// 500 ms sull'hardware di produzione. Sotto i 100 ms si alza `Memory`.
//
//	go test ./internal/auth -bench BenchmarkHash -benchtime 5x -run '^$'
func BenchmarkHash(b *testing.B) {
	hasher := newTestHasher(b, auth.DefaultParams)
	ctx := b.Context()
	for b.Loop() {
		if _, err := hasher.Hash(ctx, "password-di-riferimento-per-il-benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}
