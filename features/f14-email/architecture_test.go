package email

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoEmailProviderClientExistsOutsideF14(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	slice, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	prohibited := []string{
		"api.mailronix.com", "mailronixclient", `"net/smtp"`,
		"smtp.sendmail", "mail" + "rox",
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".context", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(path, slice+string(filepath.Separator)) {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".ts", ".tsx", ".js", ".mjs", ".vue":
		default:
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(content))
		for _, pattern := range prohibited {
			if strings.Contains(lower, pattern) {
				t.Errorf("provider client marker %q found outside F14: %s", pattern, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
