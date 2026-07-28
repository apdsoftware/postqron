package email

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

type Renderer struct {
	brand Brand
	html  *template.Template
}

type localizedCopy struct {
	Subject   string
	Preheader string
	Heading   string
	Intro     string
	Action    string
}

type renderView struct {
	Lang      Locale
	Brand     renderBrand
	Copy      localizedCopy
	Recipient Recipient
	Detail    string
	Facts     []renderFact
	ActionURL string
	Footer    string
}

type renderFact struct {
	Label string
	Value string
}

type renderBrand struct {
	Name          string
	LogoURL       template.URL
	Canvas        template.CSS
	Surface       template.CSS
	Text          template.CSS
	TextMuted     template.CSS
	Brand         template.CSS
	TextInverse   template.CSS
	Border        template.CSS
	Danger        template.CSS
	DangerSurface template.CSS
	FontFamily    template.CSS
}

func NewRenderer(brand Brand) (*Renderer, error) {
	if strings.TrimSpace(brand.Name) == "" ||
		strings.TrimSpace(brand.LogoURL) == "" ||
		strings.TrimSpace(brand.FontFamily) == "" {
		return nil, errors.New("resolved F1 brand is required")
	}
	if err := validateHTTPSURL(brand.LogoURL); err != nil {
		return nil, errors.New("resolved F1 logo URL must use HTTPS")
	}
	if strings.ContainsAny(brand.FontFamily, "{};<>") ||
		strings.Contains(strings.ToLower(brand.FontFamily), "url(") {
		return nil, errors.New("resolved F1 font family is invalid")
	}
	for _, value := range []string{
		brand.Canvas, brand.Surface, brand.Text, brand.TextMuted, brand.Brand,
		brand.TextInverse, brand.Border, brand.Danger, brand.DangerSurface,
	} {
		if !validHexColor(value) {
			return nil, errors.New("resolved F1 brand contains an invalid color")
		}
	}
	parsed, err := template.New("email").Parse(baseHTML)
	if err != nil {
		return nil, fmt.Errorf("parse HTML template: %w", err)
	}
	return &Renderer{brand: brand, html: parsed}, nil
}

func (renderer *Renderer) Render(message Message) (RenderedMessage, error) {
	if err := validateMessage(message); err != nil {
		return RenderedMessage{}, err
	}
	locale := ResolveLocale(message.Recipient.Locale)
	copy := templateCatalog[message.Template][locale]
	view := renderView{
		Lang:      locale,
		Brand:     safeRenderBrand(renderer.brand),
		Copy:      copy,
		Recipient: message.Recipient,
		Detail:    message.Data.Detail,
		Facts:     localizedFacts(locale, message.Data),
		ActionURL: message.Data.ActionURL,
		Footer:    localizedFooter(locale, renderer.brand.Name),
	}
	var htmlOutput bytes.Buffer
	if err := renderer.html.Execute(&htmlOutput, view); err != nil {
		return RenderedMessage{}, fmt.Errorf("render HTML template: %w", err)
	}
	textOutput := renderPlainText(view)
	return RenderedMessage{
		MessageID:       message.ID,
		IdempotencyKey:  message.IdempotencyKey,
		Channel:         message.Channel,
		Template:        message.Template,
		TemplateVersion: message.TemplateVersion,
		Locale:          locale,
		Recipient:       message.Recipient,
		Subject:         fmt.Sprintf(copy.Subject, renderer.brand.Name),
		Preheader:       copy.Preheader,
		HTML:            htmlOutput.String(),
		Text:            textOutput,
	}, nil
}

func safeRenderBrand(brand Brand) renderBrand {
	return renderBrand{
		Name: brand.Name, LogoURL: template.URL(brand.LogoURL),
		Canvas: template.CSS(brand.Canvas), Surface: template.CSS(brand.Surface),
		Text: template.CSS(brand.Text), TextMuted: template.CSS(brand.TextMuted),
		Brand: template.CSS(brand.Brand), TextInverse: template.CSS(brand.TextInverse),
		Border: template.CSS(brand.Border), Danger: template.CSS(brand.Danger),
		DangerSurface: template.CSS(brand.DangerSurface),
		FontFamily:    template.CSS(brand.FontFamily),
	}
}

func renderPlainText(view renderView) string {
	var output strings.Builder
	output.WriteString(view.Copy.Heading + "\n\n" + view.Copy.Intro)
	if view.Detail != "" {
		output.WriteString("\n\n" + view.Detail)
	}
	for _, fact := range view.Facts {
		output.WriteString("\n\n" + fact.Label + ": " + fact.Value)
	}
	if view.ActionURL != "" {
		output.WriteString("\n\n" + view.Copy.Action + ": " + view.ActionURL)
	}
	output.WriteString("\n\n" + view.Footer + "\n")
	return output.String()
}

func localizedFacts(locale Locale, data TemplateData) []renderFact {
	labels := factLabels[locale]
	facts := make([]renderFact, 0, 3)
	if !data.OccurredAt.IsZero() {
		facts = append(facts, renderFact{labels[0], formatDateTime(data.OccurredAt, data.TimeZone, locale)})
	}
	if data.Count != nil {
		facts = append(facts, renderFact{labels[1], formatInteger(*data.Count, locale)})
	}
	if data.AmountMinor != nil {
		facts = append(facts, renderFact{labels[2], formatCurrency(*data.AmountMinor, data.Currency, locale)})
	}
	return facts
}

var factLabels = map[Locale][3]string{
	LocaleEnglish: {"Date and time", "Quantity", "Amount"},
	LocaleItalian: {"Data e ora", "Quantità", "Importo"},
	LocaleSpanish: {"Fecha y hora", "Cantidad", "Importe"},
	LocaleFrench:  {"Date et heure", "Quantité", "Montant"},
	LocaleGerman:  {"Datum und Uhrzeit", "Anzahl", "Betrag"},
}

func formatDateTime(value time.Time, zone string, locale Locale) string {
	location := time.UTC
	if zone != "" {
		if loaded, err := time.LoadLocation(zone); err == nil {
			location = loaded
		}
	}
	local := value.In(location)
	switch locale {
	case LocaleEnglish:
		return local.Format("Jan 2, 2006, 3:04 PM MST")
	case LocaleGerman:
		return local.Format("02.01.2006, 15:04 MST")
	default:
		return local.Format("02/01/2006, 15:04 MST")
	}
}

func formatInteger(value int64, locale Locale) string {
	raw := strconv.FormatInt(value, 10)
	separator := ","
	if locale != LocaleEnglish {
		separator = "."
	}
	for index := len(raw) - 3; index > 0; index -= 3 {
		raw = raw[:index] + separator + raw[index:]
	}
	return raw
}

func formatCurrency(minor int64, currency string, locale Locale) string {
	absolute := minor
	sign := ""
	if absolute < 0 {
		sign, absolute = "-", -absolute
	}
	decimal := "."
	if locale != LocaleEnglish {
		decimal = ","
	}
	number := sign + formatInteger(absolute/100, locale) + decimal + fmt.Sprintf("%02d", absolute%100)
	code := strings.ToUpper(strings.TrimSpace(currency))
	if code == "EUR" && locale != LocaleEnglish {
		return number + " €"
	}
	if code == "USD" && locale == LocaleEnglish {
		return "$" + number
	}
	return number + " " + code
}

func localizedFooter(locale Locale, product string) string {
	switch locale {
	case LocaleItalian:
		return "Messaggio transazionale inviato da " + product + "."
	case LocaleSpanish:
		return "Mensaje transaccional enviado por " + product + "."
	case LocaleFrench:
		return "Message transactionnel envoyé par " + product + "."
	case LocaleGerman:
		return "Transaktionsnachricht von " + product + "."
	default:
		return "Transactional message sent by " + product + "."
	}
}

const baseHTML = `<!doctype html>
<html lang="{{.Lang}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>{{.Copy.Heading}}</title>
  <style>
    @media only screen and (max-width:620px){.email-shell{width:100%!important}.email-content{padding:24px 20px!important}.email-action{display:block!important;width:100%!important;box-sizing:border-box!important}}
    @media (prefers-reduced-motion:reduce){*{scroll-behavior:auto!important}}
  </style>
</head>
<body style="margin:0;padding:0;background:{{.Brand.Canvas}};color:{{.Brand.Text}};font-family:{{.Brand.FontFamily}};">
  <div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Copy.Preheader}}</div>
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background:{{.Brand.Canvas}};"><tr><td align="center" style="padding:24px 12px;">
    <table role="presentation" class="email-shell" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px;max-width:100%;background:{{.Brand.Surface}};border:1px solid {{.Brand.Border}};border-radius:16px;"><tr><td class="email-content" style="padding:32px 36px;">
      <img src="{{.Brand.LogoURL}}" width="152" alt="{{.Brand.Name}}" style="display:block;width:152px;max-width:100%;height:auto;border:0;margin:0 0 32px;">
      <main>
        <h1 style="margin:0 0 16px;font-size:28px;line-height:1.2;color:{{.Brand.Text}};">{{.Copy.Heading}}</h1>
        <p style="margin:0 0 16px;font-size:16px;line-height:1.6;color:{{.Brand.Text}};">{{.Copy.Intro}}</p>
        {{if .Detail}}<div role="note" style="margin:0 0 20px;padding:16px;border-left:4px solid {{.Brand.Danger}};background:{{.Brand.DangerSurface}};font-size:15px;line-height:1.6;color:{{.Brand.Text}};">{{.Detail}}</div>{{end}}
        {{range .Facts}}<p style="margin:0 0 10px;font-size:15px;line-height:1.6;color:{{$.Brand.Text}};"><strong>{{.Label}}:</strong> {{.Value}}</p>{{end}}
        {{if .ActionURL}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0;"><tr><td><a class="email-action" href="{{.ActionURL}}" style="display:inline-block;min-height:44px;padding:12px 20px;background:{{.Brand.Brand}};color:{{.Brand.TextInverse}};font-size:16px;font-weight:600;line-height:20px;text-align:center;text-decoration:none;border-radius:10px;">{{.Copy.Action}}</a></td></tr></table>{{end}}
      </main>
      <footer style="margin-top:32px;padding-top:20px;border-top:1px solid {{.Brand.Border}};"><p style="margin:0;font-size:13px;line-height:1.6;color:{{.Brand.TextMuted}};">{{.Footer}}</p></footer>
    </td></tr></table>
  </td></tr></table>
</body>
</html>`

func c(subject, preheader, heading, intro, action string) localizedCopy {
	return localizedCopy{subject, preheader, heading, intro, action}
}

var templateCatalog = map[TemplateID]map[Locale]localizedCopy{
	TemplateWelcome: translations(
		c("Welcome to %s", "Your account is ready.", "Welcome", "Your Postqron account is ready. Complete onboarding to start publishing.", "Continue"),
		c("Benvenuto su %s", "Il tuo account è pronto.", "Benvenuto", "Il tuo account Postqron è pronto. Completa l’onboarding per iniziare a pubblicare.", "Continua"),
		c("Te damos la bienvenida a %s", "Tu cuenta está lista.", "Te damos la bienvenida", "Tu cuenta de Postqron está lista. Completa la configuración para empezar a publicar.", "Continuar"),
		c("Bienvenue sur %s", "Votre compte est prêt.", "Bienvenue", "Votre compte Postqron est prêt. Terminez la configuration pour commencer à publier.", "Continuer"),
		c("Willkommen bei %s", "Dein Konto ist bereit.", "Willkommen", "Dein Postqron-Konto ist bereit. Schließe die Einrichtung ab, um mit der Veröffentlichung zu beginnen.", "Weiter"),
	),
	TemplateAccountVerification: translations(
		c("Verify your %s account", "Confirm your email address to activate password sign-in.", "Verify your email address", "Confirm this address to finish setting up password sign-in for your Postqron account. The verification token is delivered only through the secure link below.", "Verify email address"),
		c("Verifica il tuo account %s", "Conferma il tuo indirizzo email per attivare l’accesso con password.", "Verifica il tuo indirizzo email", "Conferma questo indirizzo per completare l’attivazione dell’accesso con password al tuo account Postqron. Il token di verifica viene consegnato solo tramite il link sicuro qui sotto.", "Verifica indirizzo email"),
		c("Verifica tu cuenta de %s", "Confirma tu correo para activar el acceso con contraseña.", "Verifica tu dirección de correo", "Confirma esta dirección para terminar de activar el acceso con contraseña a tu cuenta de Postqron. El token de verificación solo se entrega a través del enlace seguro que aparece abajo.", "Verificar correo"),
		c("Vérifiez votre compte %s", "Confirmez votre adresse e-mail pour activer la connexion par mot de passe.", "Vérifiez votre adresse e-mail", "Confirmez cette adresse pour terminer l’activation de la connexion par mot de passe à votre compte Postqron. Le jeton de vérification n’est transmis que par le lien sécurisé ci-dessous.", "Vérifier l’adresse e-mail"),
		c("Bestätige dein %s-Konto", "Bestätige deine E-Mail-Adresse, um die Passwortanmeldung zu aktivieren.", "Bestätige deine E-Mail-Adresse", "Bestätige diese Adresse, um die Passwortanmeldung für dein Postqron-Konto fertig einzurichten. Das Verifizierungs-Token wird nur über den sicheren Link unten zugestellt.", "E-Mail-Adresse bestätigen"),
	),
	TemplateWorkspaceInvitation: translations(
		c("You are invited to a workspace", "A workspace invitation is waiting.", "Workspace invitation", "You have been invited to collaborate in a Postqron workspace.", "Review invitation"),
		c("Sei stato invitato in un workspace", "Ti aspetta un invito al workspace.", "Invito al workspace", "Sei stato invitato a collaborare in un workspace Postqron.", "Controlla l’invito"),
		c("Te han invitado a un espacio de trabajo", "Tienes una invitación pendiente.", "Invitación al espacio de trabajo", "Te han invitado a colaborar en un espacio de trabajo de Postqron.", "Revisar invitación"),
		c("Vous êtes invité dans un espace de travail", "Une invitation vous attend.", "Invitation à l’espace de travail", "Vous avez été invité à collaborer dans un espace de travail Postqron.", "Voir l’invitation"),
		c("Du wurdest in einen Arbeitsbereich eingeladen", "Eine Einladung wartet auf dich.", "Einladung zum Arbeitsbereich", "Du wurdest zur Zusammenarbeit in einem Postqron-Arbeitsbereich eingeladen.", "Einladung prüfen"),
	),
	TemplateAccountSecurity: translations(
		c("Security notice for your %s account", "Review recent account activity.", "Security notice", "A security-sensitive action was recorded for your account.", "Review activity"),
		c("Avviso di sicurezza per il tuo account %s", "Controlla l’attività recente.", "Avviso di sicurezza", "È stata registrata un’azione sensibile per il tuo account.", "Controlla l’attività"),
		c("Aviso de seguridad de tu cuenta de %s", "Revisa la actividad reciente.", "Aviso de seguridad", "Se ha registrado una acción sensible en tu cuenta.", "Revisar actividad"),
		c("Avis de sécurité pour votre compte %s", "Vérifiez l’activité récente.", "Avis de sécurité", "Une action sensible a été enregistrée sur votre compte.", "Vérifier l’activité"),
		c("Sicherheitshinweis für dein %s-Konto", "Prüfe die letzten Kontoaktivitäten.", "Sicherheitshinweis", "Für dein Konto wurde eine sicherheitsrelevante Aktion registriert.", "Aktivität prüfen"),
	),
	TemplateAccountLinked: translations(
		c("Account connection updated", "A sign-in provider connection changed.", "Account connection updated", "A provider was linked to or removed from your account.", "Review connections"),
		c("Collegamento account aggiornato", "È cambiato un provider di accesso.", "Collegamento account aggiornato", "Un provider è stato collegato o rimosso dal tuo account.", "Controlla i collegamenti"),
		c("Conexión de cuenta actualizada", "Ha cambiado un proveedor de acceso.", "Conexión de cuenta actualizada", "Se ha vinculado o eliminado un proveedor de tu cuenta.", "Revisar conexiones"),
		c("Connexion du compte mise à jour", "Un fournisseur de connexion a changé.", "Connexion du compte mise à jour", "Un fournisseur a été lié à votre compte ou supprimé.", "Vérifier les connexions"),
		c("Kontoverknüpfung aktualisiert", "Eine Anmeldeverknüpfung wurde geändert.", "Kontoverknüpfung aktualisiert", "Ein Anbieter wurde mit deinem Konto verknüpft oder entfernt.", "Verknüpfungen prüfen"),
	),
	TemplateSocialReconnect: translations(
		c("Reconnect a social channel", "Publishing needs your attention.", "Reconnect your social channel", "A social authorization expired or was revoked. Reconnect it before the next publication.", "Reconnect channel"),
		c("Riconnetti un canale social", "La pubblicazione richiede attenzione.", "Riconnetti il canale social", "Un’autorizzazione social è scaduta o è stata revocata. Riconnettila prima della prossima pubblicazione.", "Riconnetti il canale"),
		c("Vuelve a conectar un canal social", "La publicación necesita tu atención.", "Vuelve a conectar tu canal social", "Una autorización social ha caducado o se ha revocado. Vuelve a conectarla antes de la próxima publicación.", "Volver a conectar"),
		c("Reconnectez un canal social", "La publication nécessite votre attention.", "Reconnectez votre canal social", "Une autorisation sociale a expiré ou a été révoquée. Reconnectez-la avant la prochaine publication.", "Reconnecter le canal"),
		c("Social-Media-Kanal erneut verbinden", "Die Veröffentlichung benötigt deine Aufmerksamkeit.", "Social-Media-Kanal erneut verbinden", "Eine Autorisierung ist abgelaufen oder wurde widerrufen. Verbinde den Kanal vor der nächsten Veröffentlichung erneut.", "Kanal verbinden"),
	),
	TemplateCollaboration: translations(
		c("Collaboration update", "A review or collaboration item changed.", "Collaboration update", "An approval, comment, or editorial collaboration item needs your attention.", "Open workspace"),
		c("Aggiornamento collaborazione", "È cambiata un’attività editoriale.", "Aggiornamento collaborazione", "Un’approvazione, un commento o un’attività editoriale richiede attenzione.", "Apri il workspace"),
		c("Actualización de colaboración", "Ha cambiado una tarea editorial.", "Actualización de colaboración", "Una aprobación, comentario o tarea editorial necesita tu atención.", "Abrir espacio de trabajo"),
		c("Mise à jour de collaboration", "Un élément éditorial a changé.", "Mise à jour de collaboration", "Une approbation, un commentaire ou une tâche éditoriale requiert votre attention.", "Ouvrir l’espace"),
		c("Aktualisierung zur Zusammenarbeit", "Eine redaktionelle Aufgabe wurde geändert.", "Aktualisierung zur Zusammenarbeit", "Eine Freigabe, ein Kommentar oder eine redaktionelle Aufgabe benötigt deine Aufmerksamkeit.", "Arbeitsbereich öffnen"),
	),
	TemplatePublicationSuccess: translations(
		c("Your post was published", "Publication completed successfully.", "Post published", "Your scheduled content was published successfully.", "View post"),
		c("Il tuo post è stato pubblicato", "Pubblicazione completata.", "Post pubblicato", "Il contenuto programmato è stato pubblicato correttamente.", "Visualizza il post"),
		c("Tu publicación se ha publicado", "Publicación completada correctamente.", "Publicación realizada", "Tu contenido programado se ha publicado correctamente.", "Ver publicación"),
		c("Votre publication a été publiée", "Publication terminée avec succès.", "Publication réussie", "Votre contenu programmé a été publié avec succès.", "Voir la publication"),
		c("Dein Beitrag wurde veröffentlicht", "Veröffentlichung erfolgreich abgeschlossen.", "Beitrag veröffentlicht", "Dein geplanter Inhalt wurde erfolgreich veröffentlicht.", "Beitrag ansehen"),
	),
	TemplatePublicationFailed: translations(
		c("A post could not be published", "Publication failed and may require action.", "Publication failed", "Your scheduled content could not be published. Review the reason before retrying.", "Review publication"),
		c("Un post non è stato pubblicato", "La pubblicazione richiede un intervento.", "Pubblicazione fallita", "Il contenuto programmato non è stato pubblicato. Controlla il motivo prima di riprovare.", "Controlla la pubblicazione"),
		c("No se pudo publicar una publicación", "La publicación necesita una acción.", "Error de publicación", "No se pudo publicar tu contenido programado. Revisa el motivo antes de volver a intentarlo.", "Revisar publicación"),
		c("Une publication n’a pas pu être publiée", "La publication nécessite une action.", "Échec de la publication", "Votre contenu programmé n’a pas pu être publié. Vérifiez la raison avant de réessayer.", "Vérifier la publication"),
		c("Ein Beitrag konnte nicht veröffentlicht werden", "Die Veröffentlichung erfordert eine Aktion.", "Veröffentlichung fehlgeschlagen", "Dein geplanter Inhalt konnte nicht veröffentlicht werden. Prüfe den Grund vor einem erneuten Versuch.", "Veröffentlichung prüfen"),
	),
	TemplateBilling: translations(
		c("Billing or plan update", "Important information about your plan.", "Billing update", "Your payment, plan, cancellation, or grace-period status changed. Paddle receipts are sent separately by Paddle.", "Review plan"),
		c("Aggiornamento di fatturazione o piano", "Informazioni importanti sul tuo piano.", "Aggiornamento di fatturazione", "È cambiato lo stato di pagamento, piano, cancellazione o periodo di tolleranza. Le ricevute Paddle sono inviate separatamente da Paddle.", "Controlla il piano"),
		c("Actualización de facturación o plan", "Información importante sobre tu plan.", "Actualización de facturación", "Ha cambiado el estado de pago, plan, cancelación o período de gracia. Paddle envía sus recibos por separado.", "Revisar plan"),
		c("Mise à jour de facturation ou d’abonnement", "Information importante sur votre abonnement.", "Mise à jour de facturation", "Le statut du paiement, de l’abonnement, de l’annulation ou du délai de grâce a changé. Paddle envoie ses reçus séparément.", "Vérifier l’abonnement"),
		c("Abrechnungs- oder Tarifaktualisierung", "Wichtige Informationen zu deinem Tarif.", "Abrechnungsaktualisierung", "Der Status von Zahlung, Tarif, Kündigung oder Kulanzfrist hat sich geändert. Paddle sendet Belege separat.", "Tarif prüfen"),
	),
	TemplateDataExportReady: translations(
		c("Your data export is ready", "The secure download is available for a limited time.", "Data export ready", "Your requested data package is ready. Use the authenticated expiring link to download it.", "Download export"),
		c("La tua esportazione dati è pronta", "Il download sicuro è disponibile per un periodo limitato.", "Esportazione dati pronta", "Il pacchetto dati richiesto è pronto. Usa il link autenticato e a scadenza per scaricarlo.", "Scarica l’esportazione"),
		c("Tu exportación de datos está lista", "La descarga segura está disponible por tiempo limitado.", "Exportación de datos lista", "El paquete de datos solicitado está listo. Usa el enlace autenticado y temporal para descargarlo.", "Descargar exportación"),
		c("Votre export de données est prêt", "Le téléchargement sécurisé est disponible pour une durée limitée.", "Export de données prêt", "Le paquet de données demandé est prêt. Utilisez le lien authentifié et temporaire pour le télécharger.", "Télécharger l’export"),
		c("Dein Datenexport ist bereit", "Der sichere Download ist nur begrenzte Zeit verfügbar.", "Datenexport bereit", "Das angeforderte Datenpaket ist bereit. Verwende den authentifizierten, zeitlich begrenzten Link zum Herunterladen.", "Export herunterladen"),
	),
	TemplateDeletion: translations(
		c("Account deletion update", "Your deletion request status changed.", "Deletion update", "Your account or workspace deletion was scheduled or completed according to the retention policy.", "Review account"),
		c("Aggiornamento cancellazione account", "È cambiato lo stato della richiesta.", "Aggiornamento cancellazione", "La cancellazione dell’account o del workspace è stata pianificata o completata secondo la policy di conservazione.", "Controlla l’account"),
		c("Actualización de eliminación de cuenta", "Ha cambiado el estado de tu solicitud.", "Actualización de eliminación", "La eliminación de tu cuenta o espacio de trabajo se ha programado o completado según la política de conservación.", "Revisar cuenta"),
		c("Mise à jour de suppression du compte", "Le statut de votre demande a changé.", "Mise à jour de suppression", "La suppression de votre compte ou espace a été planifiée ou effectuée conformément à la politique de conservation.", "Vérifier le compte"),
		c("Aktualisierung zur Kontolöschung", "Der Status deiner Anfrage wurde geändert.", "Aktualisierung zur Löschung", "Die Löschung deines Kontos oder Arbeitsbereichs wurde gemäß der Aufbewahrungsrichtlinie geplant oder abgeschlossen.", "Konto prüfen"),
	),
	TemplatePrivacyRequest: translations(
		c("Privacy request confirmation", "We recorded your privacy request.", "Privacy request received", "Your privacy request was recorded and will be handled according to the applicable policy.", "Review request"),
		c("Conferma richiesta privacy", "Abbiamo registrato la tua richiesta.", "Richiesta privacy ricevuta", "La richiesta privacy è stata registrata e verrà gestita secondo la policy applicabile.", "Controlla la richiesta"),
		c("Confirmación de solicitud de privacidad", "Hemos registrado tu solicitud.", "Solicitud de privacidad recibida", "Tu solicitud de privacidad se ha registrado y se gestionará según la política aplicable.", "Revisar solicitud"),
		c("Confirmation de demande de confidentialité", "Nous avons enregistré votre demande.", "Demande de confidentialité reçue", "Votre demande a été enregistrée et sera traitée conformément à la politique applicable.", "Vérifier la demande"),
		c("Bestätigung der Datenschutzanfrage", "Wir haben deine Anfrage erfasst.", "Datenschutzanfrage eingegangen", "Deine Datenschutzanfrage wurde erfasst und gemäß der geltenden Richtlinie bearbeitet.", "Anfrage prüfen"),
	),
	TemplatePrelaunchAccess: translations(
		c("Access request confirmed", "Your pre-launch request was received.", "Request confirmed", "We received your access or pre-launch request and will notify you about the next step.", "View status"),
		c("Richiesta di accesso confermata", "Abbiamo ricevuto la richiesta pre-lancio.", "Richiesta confermata", "Abbiamo ricevuto la richiesta di accesso o pre-lancio e ti avviseremo del prossimo passo.", "Visualizza lo stato"),
		c("Solicitud de acceso confirmada", "Hemos recibido tu solicitud previa al lanzamiento.", "Solicitud confirmada", "Hemos recibido tu solicitud de acceso o prelanzamiento y te avisaremos del siguiente paso.", "Ver estado"),
		c("Demande d’accès confirmée", "Votre demande de pré-lancement a été reçue.", "Demande confirmée", "Nous avons reçu votre demande d’accès ou de pré-lancement et vous informerons de la prochaine étape.", "Voir le statut"),
		c("Zugangsanfrage bestätigt", "Deine Vorabzugangsanfrage ist eingegangen.", "Anfrage bestätigt", "Wir haben deine Zugangs- oder Vorabzugangsanfrage erhalten und informieren dich über den nächsten Schritt.", "Status ansehen"),
	),
	TemplateOperationalAlert: translations(
		c("Operational alert", "A user-facing operational event needs attention.", "Operational alert", "An administrative or operational event affecting your account requires attention.", "Review alert"),
		c("Avviso operativo", "Un evento operativo richiede attenzione.", "Avviso operativo", "Un evento amministrativo o operativo relativo al tuo account richiede attenzione.", "Controlla l’avviso"),
		c("Alerta operativa", "Un evento operativo necesita atención.", "Alerta operativa", "Un evento administrativo u operativo que afecta a tu cuenta necesita atención.", "Revisar alerta"),
		c("Alerte opérationnelle", "Un événement opérationnel nécessite une attention.", "Alerte opérationnelle", "Un événement administratif ou opérationnel affectant votre compte nécessite votre attention.", "Vérifier l’alerte"),
		c("Betriebswarnung", "Ein betrieblicher Vorgang benötigt Aufmerksamkeit.", "Betriebswarnung", "Ein administrativer oder betrieblicher Vorgang zu deinem Konto benötigt Aufmerksamkeit.", "Warnung prüfen"),
	),
}

func translations(en, it, es, fr, de localizedCopy) map[Locale]localizedCopy {
	return map[Locale]localizedCopy{
		LocaleEnglish: en, LocaleItalian: it, LocaleSpanish: es,
		LocaleFrench: fr, LocaleGerman: de,
	}
}
