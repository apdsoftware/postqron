// Package migrate applica e annulla le migrazioni versionate di PostQron.
//
// Le regole che il package fa rispettare sono quelle di db/migrations/README.md
// e di AGENTS.md §8:
//
//   - numerazione progressiva, con `up` e `down` sempre in coppia;
//   - una migrazione già applicata non si modifica più — il checksum registrato
//     nel database lo rende una condizione verificabile invece che una buona
//     intenzione;
//   - ogni migrazione è applicata una sola volta, dentro la propria transazione.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Migration è una migrazione con entrambe le direzioni già lette da disco.
type Migration struct {
	// Version è il numero progressivo del prefisso del nome file.
	Version int
	// Name è la parte descrittiva del nome file, senza numero né estensione.
	Name string
	// Up è lo SQL che porta lo schema avanti di un passo.
	Up string
	// Down è lo SQL che lo riporta esattamente allo stato precedente.
	Down string
	// Checksum copre entrambe le direzioni: anche un `down` modificato dopo il
	// merge è una migrazione diversa da quella applicata.
	Checksum string
}

// String rende la migrazione come compare nei messaggi: `0003_plans`.
func (m Migration) String() string { return fmt.Sprintf("%04d_%s", m.Version, m.Name) }

// DirName è la directory delle migrazioni, relativa alla radice del repository.
const DirName = "db/migrations"

var fileNamePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)

// Load legge le migrazioni da una directory e le restituisce ordinate per
// versione.
//
// Un file `.sql` che non rispetta la convenzione di nome è un errore, non
// qualcosa da ignorare: un `0007_typo.up.sql.bak` saltato in silenzio è una
// migrazione che non verrà mai applicata e di cui nessuno si accorgerà.
func Load(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("lettura di %s: %w", dir, err)
	}

	type pair struct {
		name       string
		up, down   string
		hasUp      bool
		hasDown    bool
		upFile     string
		downFile   string
		versionInt int
	}
	pairs := make(map[int]*pair)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := fileNamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf(
				"%s: nome non conforme, atteso NNNN_descrizione.up.sql oppure NNNN_descrizione.down.sql",
				filepath.Join(dir, entry.Name()))
		}

		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("%s: numero di versione non leggibile: %w", entry.Name(), err)
		}
		if version < 1 {
			return nil, fmt.Errorf("%s: la numerazione parte da 0001", entry.Name())
		}

		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("lettura di %s: %w", entry.Name(), err)
		}

		p := pairs[version]
		if p == nil {
			p = &pair{name: match[2], versionInt: version}
			pairs[version] = p
		}
		if p.name != match[2] {
			return nil, fmt.Errorf(
				"versione %04d usata da due migrazioni diverse: %q e %q",
				version, p.name, match[2])
		}

		if match[3] == "up" {
			p.up, p.hasUp, p.upFile = string(content), true, entry.Name()
		} else {
			p.down, p.hasDown, p.downFile = string(content), true, entry.Name()
		}
	}

	migrations := make([]Migration, 0, len(pairs))
	for version, p := range pairs {
		switch {
		case !p.hasUp:
			return nil, fmt.Errorf("%s: manca il file .up.sql corrispondente", p.downFile)
		case !p.hasDown:
			// Una migrazione senza `down` non è accettabile: lo schema
			// resterebbe senza via di ritorno (db/migrations/README.md).
			return nil, fmt.Errorf("%s: manca il file .down.sql corrispondente", p.upFile)
		case strings.TrimSpace(p.up) == "":
			return nil, fmt.Errorf("%s: il file è vuoto", p.upFile)
		case strings.TrimSpace(p.down) == "":
			return nil, fmt.Errorf("%s: il file è vuoto", p.downFile)
		}
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     p.name,
			Up:       p.up,
			Down:     p.down,
			Checksum: checksum(p.up, p.down),
		})
	}

	slices.SortFunc(migrations, func(a, b Migration) int { return a.Version - b.Version })
	return migrations, nil
}

// checksum riassume le due direzioni della migrazione.
func checksum(up, down string) string {
	sum := sha256.New()
	// Il separatore impedisce che spostare testo da un file all'altro passi
	// inosservato: senza, `up` + `down` sarebbe la stessa sequenza di byte.
	sum.Write([]byte(up))
	sum.Write([]byte("\x00"))
	sum.Write([]byte(down))
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// FindDir individua la directory delle migrazioni.
//
// Nell'ordine: la variabile POSTQRON_MIGRATIONS_DIR, se impostata; altrimenti
// `db/migrations` cercato risalendo da start. La risalita serve perché il tool
// viene lanciato sia dalla radice del monorepo sia da services/api, dove lo
// porta `make migrate`.
func FindDir(start string) (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv("POSTQRON_MIGRATIONS_DIR")); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err != nil || !info.IsDir() {
			return "", fmt.Errorf("POSTQRON_MIGRATIONS_DIR non è una directory leggibile: %q", fromEnv)
		}
		return fromEnv, nil
	}

	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, filepath.FromSlash(DirName))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"%s non trovata risalendo da %s; indicala con -dir o POSTQRON_MIGRATIONS_DIR: %w",
				DirName, start, fs.ErrNotExist)
		}
		dir = parent
	}
}

// ErrNotFound segnala che una migrazione registrata nel database non ha più un
// file corrispondente.
var ErrNotFound = errors.New("migrazione non trovata fra i file")
