package paddle

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// envelope è l'involucro comune di ogni notifica.
//
// Non usa DisallowUnknownFields, e non è una dimenticanza: Paddle aggiunge campi
// ai propri payload senza preavviso, e rifiutarne uno sconosciuto trasformerebbe
// un'aggiunta innocua in un webhook di fatturazione che smette di funzionare —
// cioè in utenti che pagano e non ricevono il piano.
type envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// subscriptionData è il sottoinsieme dell'oggetto sottoscrizione che ci serve.
//
// Manca tutto ciò che riguarda il denaro — `currency_code`, `totals`, le
// aliquote — e manca di proposito: il titolare di quei dati è Paddle (R61,
// R61-bis). Vedi la nota sul Merchant of Record nella documentazione del
// package.
type subscriptionData struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	CustomerID string `json:"customer_id"`
	CanceledAt string `json:"canceled_at"`

	CurrentBillingPeriod struct {
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	} `json:"current_billing_period"`

	ScheduledChange *struct {
		Action      string `json:"action"`
		EffectiveAt string `json:"effective_at"`
	} `json:"scheduled_change"`

	Items []struct {
		Status string `json:"status"`
		Price  struct {
			ID string `json:"id"`
		} `json:"price"`
	} `json:"items"`

	CustomData customData `json:"custom_data"`
}

// customData è ciò che abbiamo messo noi nel checkout.
//
// È l'unico campo del payload che non arriva da Paddle ma da noi, ed è il legame
// fra una sottoscrizione e un account: vedi [Subscription.UserID].
type customData struct {
	UserID string `json:"user_id"`
}

// transactionData serve solo a etichettare la riga di registro di un evento che
// non trattiamo. Non produce entitlement: vedi [EventPrefixSubscription].
type transactionData struct {
	SubscriptionID string `json:"subscription_id"`
	CustomerID     string `json:"customer_id"`
}

// parseEvent legge l'involucro di una consegna già verificata.
func parseEvent(body []byte) (Event, json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Event{}, nil, fmt.Errorf("%w: payload non decodificabile: %w", ErrInvalidRequest, err)
	}

	id := strings.TrimSpace(env.EventID)
	eventType := strings.TrimSpace(env.EventType)
	switch {
	case id == "":
		return Event{}, nil, fmt.Errorf("%w: payload senza `event_id`", ErrInvalidRequest)
	case len(id) > maxEventIDLen:
		return Event{}, nil, fmt.Errorf("%w: `event_id` troppo lungo", ErrInvalidRequest)
	case eventType == "":
		return Event{}, nil, fmt.Errorf("%w: payload senza `event_type`", ErrInvalidRequest)
	case len(eventType) > maxEventTypeLen:
		return Event{}, nil, fmt.Errorf("%w: `event_type` troppo lungo", ErrInvalidRequest)
	}

	// `occurred_at` è obbligatorio e non ha un ripiego ragionevole. Sostituirlo
	// con l'ora di arrivo sembrerebbe innocuo e sarebbe il difetto peggiore di
	// tutta l'integrazione: renderebbe *ogni* evento più recente di quelli già
	// applicati, cioè spegnerebbe in silenzio la difesa dal fuori ordine proprio
	// sul payload malformato che avrebbe più bisogno di essere fermato.
	occurredAt, err := parseTime(env.OccurredAt)
	if err != nil || occurredAt.IsZero() {
		return Event{}, nil, fmt.Errorf("%w: `occurred_at` assente o illeggibile", ErrInvalidRequest)
	}

	return Event{ID: id, Type: eventType, OccurredAt: occurredAt}, env.Data, nil
}

// parseSubscription legge l'oggetto sottoscrizione di un evento `subscription.*`.
func parseSubscription(event Event, data json.RawMessage) (Subscription, error) {
	var payload subscriptionData
	if err := json.Unmarshal(data, &payload); err != nil {
		return Subscription{}, fmt.Errorf("%w: sottoscrizione non decodificabile: %w", ErrInvalidRequest, err)
	}

	id := strings.TrimSpace(payload.ID)
	if id == "" {
		return Subscription{}, fmt.Errorf("%w: sottoscrizione senza identificativo", ErrInvalidRequest)
	}
	status := SubscriptionStatus(strings.TrimSpace(payload.Status))
	if status == "" {
		return Subscription{}, fmt.Errorf("%w: sottoscrizione senza stato", ErrInvalidRequest)
	}

	sub := Subscription{
		Event:      event,
		ID:         id,
		CustomerID: strings.TrimSpace(payload.CustomerID),
		Status:     status,
		UserID:     strings.TrimSpace(payload.CustomData.UserID),
	}

	// Gli errori di lettura delle date **non fanno fallire** la consegna, al
	// contrario di `occurred_at`. Sono descrittive del periodo di fatturazione e
	// una di esse illeggibile lascia comunque uno stato applicabile; rifiutare
	// tutto significherebbe far ripetere a Paddle un evento che sappiamo leggere
	// nella parte che conta.
	sub.CurrentPeriodStart, _ = parseTime(payload.CurrentBillingPeriod.StartsAt)
	sub.CurrentPeriodEnd, _ = parseTime(payload.CurrentBillingPeriod.EndsAt)

	if canceled, err := parseTime(payload.CanceledAt); err == nil && !canceled.IsZero() {
		sub.CanceledAt = &canceled
	}
	if change := payload.ScheduledChange; change != nil && strings.TrimSpace(change.Action) == scheduledActionCancel {
		if effective, err := parseTime(change.EffectiveAt); err == nil && !effective.IsZero() {
			sub.ScheduledCancelAt = &effective
		}
	}

	for _, item := range payload.Items {
		// Una voce disattivata è una riga di storico dentro la sottoscrizione:
		// far risalire il piano anche a quella significherebbe dare a un utente
		// che ha cambiato prezzo il piano vecchio insieme a quello nuovo. Lo stato
		// vuoto conta come attivo — è la lettura prudente su un campo che Paddle
		// potrebbe non valorizzare in ogni variante del payload.
		if strings.TrimSpace(item.Status) == itemStatusInactive {
			continue
		}
		if priceID := strings.TrimSpace(item.Price.ID); priceID != "" {
			sub.PriceIDs = append(sub.PriceIDs, priceID)
		}
	}

	return sub, nil
}

// parseIdentity ricava le identità Paddle da un payload che non trattiamo, per
// la sola riga di registro. Non fallisce mai: un evento che non applichiamo non
// deve poter far rispondere 500, o Paddle lo ripeterebbe finché non si arrende.
func parseIdentity(eventType string, data json.RawMessage) (subscriptionID, customerID string) {
	if strings.HasPrefix(eventType, EventPrefixSubscription) {
		var payload subscriptionData
		if err := json.Unmarshal(data, &payload); err == nil {
			return strings.TrimSpace(payload.ID), strings.TrimSpace(payload.CustomerID)
		}
		return "", ""
	}
	var payload transactionData
	if err := json.Unmarshal(data, &payload); err == nil {
		return strings.TrimSpace(payload.SubscriptionID), strings.TrimSpace(payload.CustomerID)
	}
	return "", ""
}

const (
	// scheduledActionCancel è l'unica azione programmata che ci riguarda: la
	// disdetta che avrà effetto a fine periodo (Termini §4.1).
	scheduledActionCancel = "cancel"
	// itemStatusInactive marca una voce non più in forza dentro la
	// sottoscrizione.
	itemStatusInactive = "inactive"
)

// parseTime legge un istante nel formato di Paddle.
//
// Il formato è RFC 3339 con i microsecondi (`2026-08-17T10:00:00.000000Z`), che
// [time.RFC3339] accetta: la parte frazionaria è facoltativa nel layout di Go.
// Una stringa vuota è un istante assente, non un errore — molti campi di data
// del payload sono nulli finché il fatto che descrivono non avviene.
func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
