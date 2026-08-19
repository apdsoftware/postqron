---
document: privacy-policy
version: 1.1.0
effective_date: 2026-08-18
language: de
status: approved
---

# Datenschutzerklärung

Diese Erklärung beschreibt, welche personenbezogenen Daten Postqron verarbeitet, warum,
und was Sie dagegen tun können. Sie ist geschrieben, um gelesen zu werden, nicht um
überstanden zu werden.

## 1. Wer verantwortlich ist

Verantwortlicher für die Verarbeitung Ihrer personenbezogenen Daten ist
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224.

Sie erreichen uns unter
privacy@postqron.com.

Wir haben keinen Datenschutzbeauftragten benannt: Unsere Verarbeitung erfüllt die
Voraussetzungen des Art. 37 DSGVO nicht — wir sind keine Behörde, unsere Kerntätigkeit ist
keine umfangreiche systematische Überwachung, und wir verarbeiten keine besonderen
Kategorien personenbezogener Daten in großem Umfang. Datenschutzanfragen gehen an die
obige Adresse und werden von uns unmittelbar bearbeitet.

## 2. Was wir verarbeiten, und warum

### 2.1 Konto und Authentifizierung

E-Mail-Adresse, Passwort (nur als Argon2id-Hash gespeichert — das Passwort selbst haben
wir nie), bevorzugte Sprache, Sitzungen und deren Ablauf sowie die Token zur Bestätigung
Ihrer Adresse oder zum Zurücksetzen Ihres Passworts.

**Warum:** um den von Ihnen gewünschten Dienst zu erbringen. **Rechtsgrundlage:**
Vertragserfüllung (Art. 6 Abs. 1 lit. b DSGVO).

### 2.2 Jobs und Ausführungen

Die von Ihnen definierten Zeitpläne, die Zieladressen, HTTP-Methoden, Header und Bodies,
die Sie konfigurieren, und zu jeder Ausführung: Beginn und Ende, Dauer, Ergebnis,
HTTP-Status, ein gekürzter Auszug der Antwort und die Versuchsnummer.

Zwei Dinge seien deutlich gesagt. Erstens: **Sie entscheiden, was in einen Job kommt.**
Wenn Sie personenbezogene Daten in eine URL, einen Header oder einen Body schreiben,
verarbeiten wir sie, weil Sie sie dort hineingeschrieben haben. Zweitens: **Auszüge der
Antworten werden gespeichert** — gibt das von Ihnen aufgerufene System also
personenbezogene Daten zurück, gelangen diese Daten in unsere Protokolle.

**Warum:** um den Dienst zu betreiben und Ihnen zu zeigen, was geschehen ist.
**Rechtsgrundlage:** Vertragserfüllung.

**Aufbewahrung:** Ausführungsprotokolle werden für den Zeitraum Ihres Tarifs — 3, 15, 30
oder 90 Tage — aufbewahrt und danach gelöscht.

### 2.3 Repository-Synchronisation

Wenn Sie ein GitHub-Repository verbinden, verarbeiten wir die Kennung des Repositorys, die
Ereignisse, die GitHub uns bei einem Push sendet, und den Inhalt der Datei `cron.yaml`.
Wir fordern nur lesenden Zugriff auf Inhalte und Metadaten des Repositorys an, sonst
nichts.

**Rechtsgrundlage:** Vertragserfüllung.

### 2.4 Geheimnisse und Zugangsdaten

Workspace-Geheimnisse, API-Schlüssel und Schlüssel von KI-Anbietern werden im Ruhezustand
verschlüsselt, nach dem Speichern nie wieder in lesbarer Form ausgegeben und nie in
Protokolle geschrieben.

### 2.5 Abrechnung

Zahlungen wickelt Paddle als Merchant of Record ab (§4). Wir erhalten den
Abonnementstatus, den Tarif und die zur Zuordnung nötigen Kennungen. **Ihre Zahlungskarte
sehen wir nie.**

**Rechtsgrundlage:** Vertragserfüllung und rechtliche Verpflichtung für steuerliche
Aufzeichnungen.

### 2.6 Sicherheit und Audit

Aufzeichnungen sicherheitsrelevanter Ereignisse: Anmeldungen, Tarifwechsel, Widerruf von
Schlüsseln, administrative Identitätsübernahme. Technische Protokolle sind so aufgebaut,
dass Geheimnisse und nicht erforderliche personenbezogene Daten ausgeschlossen sind.

**Rechtsgrundlage:** berechtigtes Interesse am Betrieb eines sicheren Dienstes (Art. 6
Abs. 1 lit. f) und, soweit einschlägig, rechtliche Verpflichtung.

### 2.7 Transaktionale E-Mail

Wir senden Ihnen die E-Mails, die Sie zur Nutzung des Dienstes brauchen: Begrüßung,
Warnungen zu fehlgeschlagenen Jobs, Tarifwechsel, Sicherheitsereignisse. Sie sind kein
Marketing, und Sie können sie nicht abbestellen, ohne Ihr Konto zu schließen, denn sie
sind die Art, wie der Dienst Ihnen Dinge mitteilt.

### 2.8 Marketing-E-Mail

Wenn Sie einwilligen, senden wir Ihnen E-Mails zum Produkt: neue Funktionen, Änderungen,
die zu kennen sich lohnt, gelegentlich etwas, das wir geschrieben haben.

**Sie sind in jeder Hinsicht von den obigen E-Mails getrennt.** Rechtsgrundlage ist Ihre
**Einwilligung** (Art. 6 Abs. 1 lit. a), eigenständig eingeholt und nie mit der Annahme
der Bedingungen oder der Kontoerstellung gebündelt. Eine Ablehnung kostet Sie nichts: Der
Dienst funktioniert genauso.

Jede Marketing-Nachricht enthält einen Abmeldelink, der mit einem Klick und ohne Anmeldung
funktioniert. Die Abmeldung beendet nur Marketing-E-Mails — die transaktionalen E-Mails,
die der Dienst Ihnen senden muss, erhalten Sie weiterhin, denn das ist kein Marketing.

Wir halten fest, wann Sie eingewilligt und wann Sie widerrufen haben; so können wir
belegen, dass wir das Recht hatten, Ihnen zu schreiben.

## 3. KI-Funktionen: eine Übermittlung, die Sie verstehen sollten

Wenn Sie die KI-gestützte Fehlersuche aktivieren, stellen Sie **Ihren eigenen**
API-Schlüssel eines KI-Anbieters (OpenAI, Anthropic oder ein anderer) bereit. Bei Nutzung
der Funktion wird der Inhalt des Ausführungsprotokolls, das Sie analysieren, unter Ihrem
Schlüssel und zu dessen Bedingungen an diesen Anbieter gesendet.

Das bedeutet: Ihre Daten verlassen unsere Infrastruktur und erreichen einen Dritten, **den
Sie ausgewählt haben**, auf Grundlage eines Vertrags **zwischen Ihnen und ihm**. Wir sind
nicht Partei dieses Vertrags, wir kontrollieren nicht, was er mit dem Inhalt tut, und es
gelten seine Aufbewahrungsregeln, nicht unsere.

Die Funktion ist aus, solange Sie sie nicht einschalten, und jede Analyse ist eine
bewusste Handlung. Vor der ersten Übermittlung holen wir Ihre ausdrückliche Einwilligung
ein.

**Rechtsgrundlage:** Einwilligung (Art. 6 Abs. 1 lit. a), die Sie jederzeit durch
Entfernen Ihres Schlüssels widerrufen können. Der Widerruf berührt bereits erfolgte
Übermittlungen nicht.

## 4. Wer sonst Ihre Daten verarbeitet

Wir setzen diese Anbieter ein. Jeder verarbeitet Daten nach unseren Weisungen auf
Grundlage eines Auftragsverarbeitungsvertrags.

| Anbieter | Rolle | Wo |
|---|---|---|
| Hetzner | Server und Datenbank | Deutschland |
| Cloudflare | DNS, TLS, CDN, statisches Hosting, Schutz am Netzrand | Globales Edge-Netz |
| Paddle | Merchant of Record: Zahlungen, Rechnungsstellung, Steuern | Vereinigtes Königreich |
| Mailronix | Zustellung transaktionaler E-Mails | Europäische Union — betrieben von Apdsoftware, derselben Einheit, die Postqron betreibt |
| GitHub | Repository-Synchronisation, nur wenn Sie eines verbinden | Vereinigte Staaten |

Wir halten diese Liste aktuell. Fügen wir einen Anbieter hinzu oder ändern ihn in einer
Weise, die Sie betrifft, aktualisieren wir diese Erklärung und teilen es Ihnen, wenn die
Änderung wesentlich ist, vor dem Wirksamwerden mit.

**Übermittlungen außerhalb des EWR.** Einige Anbieter verarbeiten Daten außerhalb des
Europäischen Wirtschaftsraums. Wo das geschieht, stützen wir uns auf die Garantien des
Art. 46 DSGVO, vor allem die Standardvertragsklauseln der Europäischen Kommission,
zusammen mit den technischen Maßnahmen des Anbieters selbst.

## 5. Wie lange wir Dinge aufbewahren

| Daten | Aufbewahrt |
|---|---|
| Konto und Profil | Solange das Konto besteht |
| Ausführungsprotokolle | 3, 15, 30 oder 90 Tage, je nach Tarif |
| Audit-Aufzeichnungen | 24 Monate |
| Abrechnungs- und Steuerunterlagen | Wie gesetzlich vorgeschrieben, in der Regel 10 Jahre |
| Sicherungen | 30 Tage |

Wenn Sie Ihr Konto löschen, stellen wir die Ausführung sofort ein und widerrufen die
Schlüssel; die Daten entfernen wir danach nach einer Schonfrist von
30 Tagen,
in der Sie es sich anders überlegen können. Bereits in Sicherungen geschriebene Daten
verschwinden, wenn diese Sicherungen ausrotieren. Die Löschung überdauern allein die
Aufzeichnungen, die wir aus steuerlichen oder rechtlichen Gründen aufbewahren müssen.

Eines überdauert die Löschung, ohne noch von Ihnen zu handeln. Wo ein Administrator auf
Ihrem Konto tätig geworden ist, bewahrt unser Sicherheitsprotokoll auf, was **er** getan
hat, mit jedem Bezug auf Sie entfernt. Was bleibt, sagt, dass eine Handlung stattfand und
wer sie vornahm; es sagt nicht mehr, gegenüber wem. Wir bewahren es auf, weil die
Schließung eines Kontos sonst den Nachweis über den Zugriff einer anderen Person darauf
tilgen würde. Das ist keine Aufzeichnung, die wir aus steuerlichen oder rechtlichen
Gründen führen — es ist eine Sicherheitsaufzeichnung über die Handlungen einer anderen
Person.

## 6. Ihre Rechte

Sie können von uns eine Kopie Ihrer Daten verlangen, deren Berichtigung, Löschung,
Einschränkung der Verarbeitung oder Widerspruch dagegen, oder die Bereitstellung in einem
übertragbaren Format. Wo die Verarbeitung auf einer Einwilligung beruht, können Sie diese
widerrufen.

Export und Löschung stehen in der Anwendung zur Verfügung, ohne uns zu fragen. Für alles
Übrige schreiben Sie uns; wir antworten binnen eines Monats.

Wenn Sie meinen, dass wir Ihre Daten unrechtmäßig behandeln, können Sie sich bei Ihrer
nationalen Aufsichtsbehörde beschweren. In Italien ist das der *Garante per la protezione
dei dati personali*.

## 7. Sicherheit

Wir verschlüsseln Geheimnisse im Ruhezustand, hashen Passwörter mit Argon2id, halten
Protokolle frei von Zugangsdaten, prüfen die Signatur eingehender Webhooks, begrenzen die
Rate der Authentifizierung und halten sicherheitsrelevante Ereignisse in einem
Audit-Protokoll fest.

Wir sollten Ihnen auch sagen, was wir nicht haben: Postqron läuft auf einem einzigen
Server, bewusst so gewählt, damit Scheduler und Datenbank nebeneinander liegen. Diese Wahl
tauscht Ausfallsicherheit gegen Latenz. Wir erstellen Sicherungen und haben deren
Wiederherstellung getestet, aber ein Ausfall dieser Maschine unterbricht den Dienst.

## 8. Automatisierte Entscheidungen

Wir treffen über Sie keine Entscheidungen mit rechtlicher oder ähnlich erheblicher Wirkung
auf automatisiertem Weg, und wir erstellen kein Profil von Ihnen.

## 9. Kinder

Postqron ist nicht für Personen unter
16 Jahren
bestimmt. Wir erheben ihre Daten nicht wissentlich.

## 10. Änderungen

Wir können diese Erklärung aktualisieren. Version und Datum des Inkrafttretens stehen
oben. Ist eine Änderung wesentlich, teilen wir sie Ihnen vor dem Wirksamwerden mit und
holen, wo das Gesetz es verlangt, erneut Ihre Einwilligung ein.

---

**Kontakt:** privacy@postqron.com
**Betrieben von:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
