// Package dotenv carica un file `.env` nell'ambiente del processo.
//
// Serve agli strumenti da riga di comando che girano in sviluppo: Docker
// Compose legge `.env` da sé, un processo Go no, e senza questo il tool di
// migrazione non troverebbe le stesse `POSTGRES_*` con cui il container è stato
// creato (AGENTS.md §7).
//
// Una variabile già presente nell'ambiente non viene mai sovrascritta:
// l'ambiente esplicito vince sempre sul file, altrimenti un `POSTGRES_PORT=5544
// make migrate` non avrebbe alcun effetto e la ragione non sarebbe visibile da
// nessuna parte.
package dotenv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Conflict è una variabile che l'ambiente e il file definiscono entrambi, con
// valori diversi.
//
// Non è di per sé un errore — l'ambiente vince, ed è spesso ciò che si vuole —
// ma per le variabili che decidono *quale* database si sta per toccare è
// un'ambiguità che il chiamante deve poter vedere invece di subire: gli script
// bash del progetto (`scripts/db-env.sh`) risolvono la stessa coppia nel verso
// opposto, dando la precedenza al file.
type Conflict struct {
	// Key è il nome della variabile.
	Key string
	// Environment è il valore già presente nell'ambiente, quello che vince.
	Environment string
	// File è il valore scritto nel `.env`, che resta inapplicato.
	File string
}

func (c Conflict) String() string {
	return fmt.Sprintf("%s: ambiente %q, .env %q", c.Key, c.Environment, c.File)
}

// Load legge il file indicato e imposta le variabili che mancano
// nell'ambiente. Un file assente non è un errore: in produzione la
// configurazione arriva dall'ambiente e il file non esiste.
//
// Restituisce le variabili che il file e l'ambiente definiscono in modo
// diverso, ordinate per nome. Il valore applicato resta quello dell'ambiente.
func Load(path string) ([]Conflict, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	values, err := Parse(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var conflicts []Conflict
	for key, value := range values {
		if current, present := os.LookupEnv(key); present {
			if current != value {
				conflicts = append(conflicts, Conflict{Key: key, Environment: current, File: value})
			}
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, key, err)
		}
	}
	slices.SortFunc(conflicts, func(a, b Conflict) int { return strings.Compare(a.Key, b.Key) })
	return conflicts, nil
}

// LoadNearest cerca un `.env` a partire da start e risalendo le directory
// padre, e carica il primo che trova. Rende `go run ./cmd/migrate` equivalente
// da qualunque punto del monorepo lo si lanci.
func LoadNearest(start string) ([]Conflict, error) {
	path, err := find(start, ".env")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return Load(path)
}

// Parse legge coppie chiave/valore in formato `.env`.
//
// Riconosce: righe vuote, commenti introdotti da `#`, il prefisso `export`, e
// valori racchiusi fra apici singoli o doppi. Dentro gli apici doppi le
// sequenze `\n` e `\t` sono interpretate; dentro quelli singoli il valore è
// letterale, come nella shell.
func Parse(r io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimPrefix(raw, "export ")

		key, value, found := strings.Cut(raw, "=")
		if !found {
			return nil, fmt.Errorf("riga %d: manca il segno `=`", line)
		}
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return nil, fmt.Errorf("riga %d: nome di variabile non valido: %q", line, key)
		}
		values[key] = unquote(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// unquote toglie gli apici e, per quelli doppi, interpreta le sequenze di
// escape. Un valore senza apici perde il commento che lo segue: `PORT=5432 #
// locale` vale 5432.
func unquote(value string) string {
	if len(value) >= 2 {
		switch {
		case value[0] == '"' && value[len(value)-1] == '"':
			inner := value[1 : len(value)-1]
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\t`, "\t")
			return strings.ReplaceAll(inner, `\"`, `"`)
		case value[0] == '\'' && value[len(value)-1] == '\'':
			return value[1 : len(value)-1]
		}
	}
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = value[:comment]
	}
	return strings.TrimSpace(value)
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// find risale da start cercando name in ogni directory attraversata.
func find(start, name string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s non trovato risalendo da %s: %w", name, start, fs.ErrNotExist)
		}
		dir = parent
	}
}
