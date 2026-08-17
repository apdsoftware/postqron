package githubapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// apiVersion è la versione dell'API dichiarata a ogni richiesta. GitHub la usa
// per non cambiare il contratto sotto ai client che non l'hanno chiesto.
const apiVersion = "2022-11-28"

// FileAtRef scarica un file del repository al riferimento indicato.
//
// Il secondo valore distingue **«il file non c'è»** da un guasto, ed è una
// distinzione che il chiamante deve poter fare senza leggere il testo di un
// errore: un `cron.yaml` assente è una risposta legittima su cui la
// riconciliazione decide qualcosa, mentre un 500 di GitHub è una consegna da
// ripetere. Confonderle significherebbe o ripetere all'infinito una push su un
// repository che quel file non lo ha mai avuto, o archiviare i job di un utente
// perché GitHub ha avuto trenta secondi difficili.
//
// `ref` è un commit, un ramo o un tag. La riconciliazione passa il **commit**
// della push e non il ramo: fra la ricezione del webhook e questa chiamata il
// ramo può essere avanzato ancora, e sincronizzare uno stato che non è quello
// che ha generato l'evento renderebbe il registro dei sync una cronologia falsa.
func (c *Client) FileAtRef(ctx context.Context, installationID int64, owner, repo, path, ref string) ([]byte, bool, error) {
	switch {
	case owner == "" || repo == "":
		return nil, false, errors.New("githubapp: owner e nome del repository sono obbligatori")
	case path == "":
		return nil, false, errors.New("githubapp: il percorso del file è obbligatorio")
	case ref == "":
		return nil, false, errors.New("githubapp: il riferimento è obbligatorio")
	}

	content, found, err := c.fetchFile(ctx, installationID, owner, repo, path, ref)
	if !errors.Is(err, errStaleToken) {
		return content, found, err
	}

	// Il token in cache non vale più prima della sua scadenza dichiarata: capita
	// se l'utente ha reinstallato l'App o ne ha cambiato i permessi. Un solo
	// nuovo tentativo, con un token nuovo: se fallisce anche quello il problema
	// non è il token.
	c.forgetToken(installationID)
	content, found, err = c.fetchFile(ctx, installationID, owner, repo, path, ref)
	if errors.Is(err, errStaleToken) {
		return nil, false, fmt.Errorf("%w: %s/%s: token di installazione rifiutato due volte", ErrForbidden, owner, repo)
	}
	return content, found, err
}

// errStaleToken è interno: dice a [Client.FileAtRef] di riprovare con un token
// nuovo. Non esce mai da questo package.
var errStaleToken = errors.New("githubapp: token di installazione non più valido")

func (c *Client) fetchFile(ctx context.Context, installationID int64, owner, repo, path, ref string) ([]byte, bool, error) {
	token, err := c.installationAccessToken(ctx, installationID)
	if err != nil {
		return nil, false, err
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		c.baseURL,
		url.PathEscape(owner), url.PathEscape(repo),
		escapePath(path), url.QueryEscape(ref))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, fmt.Errorf("githubapp: costruzione della richiesta del file: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// Il media type `raw` restituisce i byte del file invece della busta JSON
	// con il contenuto in base64. Conta più di quanto sembri: la busta ha un
	// tetto di un megabyte oltre il quale il campo `content` arriva vuoto senza
	// che la risposta sia un errore, e un `cron.yaml` vuoto interpretato come
	// tale archivierebbe tutti i job dell'utente.
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("githubapp: lettura di %s in %s/%s: %w", path, owner, repo, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// GitHub risponde 404 sia per «il file non esiste» sia per «non hai
		// accesso al repository»: è deliberato da parte loro, e non
		// distinguerlo è deliberato da parte nostra. Il token è appena stato
		// emesso *per questa installazione*, quindi l'ipotesi utile è la prima;
		// se fosse la seconda, il repository collegato non sarebbe più
		// leggibile e l'utente lo scoprirebbe dallo stesso messaggio.
		return nil, false, nil
	case http.StatusUnauthorized:
		return nil, false, errStaleToken
	case http.StatusForbidden:
		return nil, false, fmt.Errorf("%w: %s/%s", ErrForbidden, owner, repo)
	default:
		return nil, false, fmt.Errorf("githubapp: lettura di %s in %s/%s: GitHub ha risposto %d",
			path, owner, repo, resp.StatusCode)
	}

	// Un byte oltre il tetto: è così che si distingue «esattamente al limite» da
	// «oltre», senza leggere tutto per poi scoprirlo.
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.max+1))
	if err != nil {
		return nil, false, fmt.Errorf("githubapp: lettura di %s in %s/%s: %w", path, owner, repo, err)
	}
	if int64(len(body)) > c.max {
		return nil, false, fmt.Errorf("githubapp: %s in %s/%s supera %d byte", path, owner, repo, c.max)
	}

	c.log.DebugContext(ctx, "file letto dal repository",
		slog.String("repository", owner+"/"+repo),
		slog.String("path", path),
		slog.String("ref", ref),
		slog.Int("bytes", len(body)))

	return body, true, nil
}

// escapePath codifica un percorso mantenendo gli slash: `.github/cron.yaml` è
// un percorso di due segmenti e `url.PathEscape` ne farebbe un unico segmento
// con uno `%2F` in mezzo, che GitHub non risolve.
func escapePath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
