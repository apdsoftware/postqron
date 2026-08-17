package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// tokenMargin è quanto prima della scadenza un token in cache viene considerato
// esaurito.
//
// I token di installazione durano un'ora. Usarne uno che scade fra due secondi
// significa che la richiesta parte valida e arriva scaduta, e l'errore che ne
// esce (401) è indistinguibile da una App revocata. Un minuto di margine copre
// il viaggio e la deriva fra il nostro orologio e quello di GitHub.
const tokenMargin = time.Minute

// installationToken è un token in cache, con la sua scadenza.
type installationToken struct {
	value     string
	expiresAt time.Time
}

// LogValue impedisce che il token finisca in un log per distrazione: un token
// di installazione legge tutti i repository di un cliente.
func (t installationToken) LogValue() slog.Value { return slog.StringValue("[redatto]") }

// installationAccessToken restituisce un token valido per l'installazione,
// riusando quello in cache finché regge.
//
// La cache non è un'ottimizzazione cosmetica. Senza, ogni push costa una
// chiamata in più a GitHub, e le chiamate a `/app/installations/.../access_tokens`
// hanno un tetto proprio: un utente che spinge spesso brucerebbe quel tetto per
// tutti gli altri.
func (c *Client) installationAccessToken(ctx context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", fmt.Errorf("%w: identificativo di installazione assente", ErrForbidden)
	}

	if token, ok := c.cachedToken(installationID); ok {
		return token, nil
	}

	appJWT, err := c.appJWT()
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("githubapp: costruzione della richiesta di token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("githubapp: richiesta del token di installazione: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// L'App non è più installata, o non lo è mai stata su quel numero. Non
		// è un guasto: ripetere non cambierà la risposta.
		return "", fmt.Errorf("%w: installazione %d, GitHub ha risposto %d",
			ErrForbidden, installationID, resp.StatusCode)
	default:
		return "", fmt.Errorf("githubapp: token di installazione, GitHub ha risposto %d", resp.StatusCode)
	}

	// Il corpo è limitato come tutti gli altri: è una risposta di GitHub, ma il
	// tetto costa niente e toglie un caso in cui un errore altrui diventa
	// memoria nostra.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("githubapp: lettura del token di installazione: %w", err)
	}

	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// L'errore di json cita il testo che non ha saputo leggere, e quel testo
		// contiene il token quando il problema è altrove nella struttura.
		return "", fmt.Errorf("githubapp: risposta del token di installazione non decodificabile")
	}
	if payload.Token == "" {
		return "", fmt.Errorf("githubapp: risposta del token di installazione senza token")
	}
	if payload.ExpiresAt.IsZero() {
		// GitHub la manda sempre; senza, il token vale finché non fallisce.
		payload.ExpiresAt = c.now().Add(time.Hour)
	}

	c.storeToken(installationID, installationToken{value: payload.Token, expiresAt: payload.ExpiresAt})
	return payload.Token, nil
}

func (c *Client) cachedToken(installationID int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	token, ok := c.tokens[installationID]
	if !ok || !c.now().Add(tokenMargin).Before(token.expiresAt) {
		return "", false
	}
	return token.value, true
}

func (c *Client) storeToken(installationID int64, token installationToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[installationID] = token
}

// forgetToken scarta il token in cache di un'installazione. Serve a un caso
// solo: GitHub ha risposto 401 a una richiesta fatta con quel token, e tenerlo
// significherebbe ripetere lo stesso 401 fino alla scadenza.
func (c *Client) forgetToken(installationID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, installationID)
}
