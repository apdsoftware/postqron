package emailrender

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DirName è la directory dei template, relativa alla radice del repository.
//
// I template stanno lì e non dentro il modulo Go perché la spec li vuole lì
// (R19), e da lì non si possono includere nel binario con go:embed: la
// direttiva non attraversa il confine del modulo, e services/api non contiene
// emails/. È lo stesso vincolo di db/migrations, risolto allo stesso modo —
// vedi migrate.FindDir. Chi distribuisce il servizio porta con sé questa
// directory; chi preferisce incorporarla passa a New un fs.FS qualunque, che è
// il motivo per cui New accetta un filesystem e non un percorso.
const DirName = "emails/templates"

// DirEnvVar sovrascrive la ricerca automatica.
const DirEnvVar = "POSTQRON_EMAIL_TEMPLATES_DIR"

// FindDir individua la directory dei template.
//
// Nell'ordine: la variabile POSTQRON_EMAIL_TEMPLATES_DIR, se impostata;
// altrimenti `emails/templates` cercata risalendo da start. La risalita serve
// perché il processo viene lanciato tanto dalla radice del monorepo quanto da
// services/api, e i test girano nella directory del proprio package.
func FindDir(start string) (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(DirEnvVar)); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err != nil || !info.IsDir() {
			return "", fmt.Errorf("%s non è una directory leggibile: %q", DirEnvVar, fromEnv)
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
				"%s non trovata risalendo da %s; indicala con %s: %w",
				DirName, start, DirEnvVar, fs.ErrNotExist)
		}
		dir = parent
	}
}

func openDir(dir string) (fs.FS, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("directory dei template: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q non è una directory", dir)
	}
	return os.DirFS(dir), nil
}
