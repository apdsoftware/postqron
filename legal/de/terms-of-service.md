---
document: terms-of-service
version: 1.2.0
effective_date: 2026-08-18
language: de
status: approved
---

# Nutzungsbedingungen

Diese Bedingungen regeln Ihre Nutzung von Postqron. Mit der Erstellung eines Kontos
akzeptieren Sie sie zusammen mit der
[Richtlinie zur zulässigen Nutzung](acceptable-use-policy.md) und der
[Datenschutzerklärung](privacy-policy.md).

## 1. Mit wem Sie den Vertrag schließen

Postqron wird betrieben von
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
(„wir“, „uns“).

**Käufe erfolgen über Paddle.** Paddle handelt als Merchant of Record: Wenn Sie einen
kostenpflichtigen Tarif kaufen, kommt der Kaufvertrag über diesen Kauf zwischen Ihnen und
Paddle zustande, und die Käuferbedingungen von Paddle gelten dafür zusätzlich zu diesen
Bedingungen. Paddle übernimmt Zahlung, Rechnungsstellung und Steuern. Wir übernehmen den
Dienst.

## 2. Was der Dienst tut

Postqron führt HTTP-Anfragen an Adressen aus, die Sie festlegen, zu Zeiten, die Sie
festlegen, protokolliert das Ergebnis und benachrichtigt Sie über Fehlschläge. Zeitpläne
lassen sich in der Anwendung oder in einer Datei `cron.yaml` in einem von Ihnen
verbundenen Repository definieren.

**Postqron führt Ihren Code nicht aus.** Es stellt HTTP-Anfragen. Löst eine Anfrage eine
Verarbeitung auf Ihren Systemen aus, ist diese Verarbeitung Ihre.

## 3. Ihr Konto

Sie sind verantwortlich für das, was unter Ihrem Konto geschieht, für die sichere
Verwahrung Ihrer Zugangsdaten und für die Personen, die Sie in Ihren Workspace einladen.
Teilen Sie uns unverzüglich mit, wenn Sie annehmen, dass Ihr Konto kompromittiert wurde.

Sie müssen mindestens 16 Jahre alt und, wenn Sie für eine Organisation handeln, zu deren
Vertretung befugt sein.

**Der kostenlose Tarif steht allen offen.** Nutzen Sie ihn für ein Nebenprojekt, um den
Dienst auszuprobieren, oder weil er für Ihre Zwecke ausreicht. Nichts hier verlangt von
Ihnen, ein Unternehmen zu sein, um ein Konto anzulegen.

**Kostenpflichtige Tarife werden für die berufliche Nutzung angeboten.** Mit dem Kauf
bestätigen Sie, dass Sie im Rahmen einer gewerblichen, geschäftlichen, handwerklichen oder
beruflichen Tätigkeit handeln. Deshalb weisen wir unsere Preise ohne Steuern aus: Wer ein
Unternehmen führt, für den ist der Nettobetrag der maßgebliche, denn er ist der
abziehbare. Wir bitten Sie, dies beim Bezahlvorgang zu bestätigen, und wir erheben Ihre
Umsatzsteuer-Identifikationsnummer, sofern Sie eine haben — manche völlig legitimen
Kleinunternehmerregelungen in Europa sehen keine vor, deshalb fragen wir danach, ohne sie
zu verlangen.

Soweit das Gesetz Ihnen trotz dieser Bestätigung Verbraucherschutzrechte einräumt, geht
das Gesetz vor — einschließlich des Widerrufsrechts nach §4.3.

## 4. Tarife, Grenzen und Zahlung

Tarife, Preise und Grenzen sind die auf unserer Preisseite veröffentlichten und vom Dienst
angewandten. **Die Grenzen werden von der Engine durchgesetzt**, nicht bloß behauptet: die
Anzahl der Jobs eines Tarifs, das Mindestintervall und die Aufbewahrung der Protokolle
sind tatsächliche Obergrenzen.

Die Preise werden **ohne Steuern** angezeigt. Paddle berechnet die anwendbare Steuer nach
Ihrem Standort und schlägt sie auf.

Kostenpflichtige Tarife verlängern sich automatisch um denselben Zeitraum, bis sie
gekündigt werden. Sie können jederzeit kündigen; die Kündigung wird zum Ende des bereits
bezahlten Zeitraums wirksam, und bis dahin läuft der Dienst weiter.

### 4.1 Tarifwechsel

Höherstufungen werden sofort wirksam. **Herabstufungen werden zum Ende des laufenden
Zeitraums wirksam**, und wir sagen Ihnen vor Ihrer Bestätigung, was geschehen wird.

**Wenn Sie mehr aktive Jobs haben, als der niedrigere Tarif erlaubt, pausieren wir sie
alle, und Sie wählen, welche wieder eingeschaltet werden**, bis zur neuen Grenze. Wir
wählen nicht für Sie, denn wir können es nicht: Zwei Jobs, die für uns identisch aussehen,
können für Sie einer sein, der Rechnungen stellt, und einer, der eine Erinnerung
verschickt. Jede automatische Regel, die wir uns ausgedacht hätten, wäre geraten — und
hätte genau dort falsch geraten, wo es am meisten zählt.

Passen Ihre aktiven Jobs bereits in die neue Grenze, wird nichts pausiert.

**Wir löschen Ihre Arbeit nicht.** Pausierte Jobs bleiben sichtbar, bearbeitbar und
exportierbar, samt ihrer Ausführungshistorie. Eines sollten Sie wissen: Ein Job, der
häufiger geplant ist, als der neue Tarif erlaubt, lässt sich erst wieder einschalten, wenn
Sie seinen Zeitplan ändern — auch wenn Platz für ihn wäre.

Dasselbe gilt, wenn eine Zahlung endgültig fehlschlägt oder ein Abonnement ausläuft: Beide
führen das Konto auf den kostenlosen Tarif zurück.

### 4.2 Fehlgeschlagene Zahlung

Schlägt eine Zahlung fehl, wiederholt Paddle sie nach seinem eigenen Zeitplan. In diesem
Zeitraum läuft Ihr Dienst weiter. Schlägt die Zahlung endgültig fehl, wechselt das Konto
auf den kostenlosen Tarif, und §4.1 gilt unverändert: Wenn Sie mehr aktive Jobs haben, als
der kostenlose Tarif erlaubt, werden alle pausiert, und Sie wählen, welche wieder
eingeschaltet werden. Es wird nichts gelöscht.

### 4.3 Erstattungen und Widerruf

Die Regel ist einfach: **Sie können jederzeit aufhören, und der bereits bezahlte Monat
läuft zu Ende.** Es wird nichts anteilig erstattet, und es gibt nichts einzufordern oder
auszuhandeln.

Sind Sie Verbraucher in der Europäischen Union, steht Ihnen zusätzlich das gesetzliche
Widerrufsrecht binnen 14 Tagen nach dem Kauf zu. Da der Dienst sofort beginnt, werden Sie
gebeten, der sofortigen Leistungserbringung zuzustimmen; diese Zustimmung lässt das
Widerrufsrecht erlöschen, sobald die Leistung vollständig erbracht ist. Soweit uns das
Gesetz dennoch zur Erstattung verpflichtet, erstatten wir, und Paddle wickelt es ab.

## 5. Verfügbarkeit

Wir wollen den Dienst durchgehend am Laufen halten, und wir sagen Ihnen, wenn er es nicht
ist (wie wir Sie bei Störungen kontaktieren, steht in der Richtlinie zur zulässigen
Nutzung).

**Wir geben keine Verfügbarkeitsgarantie, und wir wollen offen sagen, warum.** Der
Scheduler und die Datenbank laufen auf einem einzigen Server, bewusst so gewählt, damit
die Auslösung nicht durch Netzwerklatenz verzögert wird. Diese Wahl tauscht
Ausfallsicherheit gegen Präzision. Wir erstellen Sicherungen und testen deren
Wiederherstellung, aber ein Ausfall dieser Maschine unterbricht den Dienst. Jede Zusage
über das hinaus, was eine einzelne Maschine leisten kann, wäre eine Zusage, die wir nicht
halten könnten.

Sollten wir je eine Vereinbarung über das Dienstniveau mit messbaren Zusagen anbieten,
erscheint sie hier — und die Architektur wird sich vorher geändert haben, nicht nachher.

## 6. Ihre Inhalte und unsere

**Ihres bleibt Ihres.** Ihre Zeitpläne, Ihre Konfiguration, Ihre Protokolle und die Daten,
die Sie durch den Dienst leiten, bleiben Ihr Eigentum. Sie räumen uns nur die Erlaubnis
ein, die wir brauchen, um den Dienst für Sie zu betreiben: diese Daten zu speichern, die
von Ihnen konfigurierten Anfragen auszuführen und Ihnen die Ergebnisse zu zeigen.

Postqron selbst — die Software, die Oberfläche, der Name und die Marke — bleibt unser.
Diese Bedingungen geben Ihnen das Recht, den Dienst zu nutzen, nicht ihn zu kopieren oder
weiterzuverkaufen.

## 7. Sperrung und Beendigung

Wir können Ihr Konto bei einem wesentlichen Verstoß gegen diese Bedingungen oder gegen die
Richtlinie zur zulässigen Nutzung sperren oder beenden, in der dort beschriebenen Weise
und mit der dort beschriebenen Ankündigung.

Sie können Ihr Konto jederzeit schließen. Bei der Schließung stellen wir die Ausführung
ein, widerrufen Schlüssel und löschen Ihre Daten nach der in der Datenschutzerklärung
genannten Schonfrist.

**Die Schließung Ihres Kontos kündigt kein kostenpflichtiges Abonnement.** Die Zahlung
wickelt Paddle als Merchant of Record ab (§1), ein Abonnement wird daher bei Paddle
gekündigt, nicht bei uns. Schließen Sie Ihr Konto, während ein kostenpflichtiger Tarif
läuft, läuft der bereits bezahlte Zeitraum zu Ende, wie in §4.3 beschrieben. Wir sagen
Ihnen das vor Ihrer Bestätigung und bitten Sie, es zur Kenntnis zu nehmen.

## 8. Haftung

Nichts hier beschränkt eine Haftung, die gesetzlich nicht beschränkt werden kann,
einschließlich der Haftung für Tod oder Körperverletzung infolge Fahrlässigkeit, für
Arglist, oder der Rechte, die Verbrauchern nach zwingendem Recht zustehen.

Vorbehaltlich dessen: Wir erbringen den Dienst mit angemessener Sorgfalt und Sachkunde,
haften aber nicht für mittelbare Schäden oder Folgeschäden, für entgangenen Gewinn oder
entgangene Geschäfte, noch für die Folgen der Verarbeitung, die Ihre Jobs auf Ihren
eigenen Systemen auslösen. **Eine geplante Anfrage ist keine Garantie dafür, dass die
dahinterliegende Verarbeitung erfolgreich war**, und Sie sollten Ihre Systeme unter dieser
Annahme entwerfen.

Über diese Ausnahmen hinaus ist **unsere Haftung im größtmöglichen nach anwendbarem Recht
zulässigen Umfang ausgeschlossen**.

Wir sagen es lieber deutlich, als es zu vergraben: Postqron ist ein Scheduler zum Preis
von null bis wenigen Dutzend Euro im Monat, und er kann das Risiko dessen nicht tragen,
was von den Jobs abhängt, die er ausführt. Würde Ihnen eine ausgefallene oder doppelte
Ausführung einen erheblichen Schaden verursachen, ist der Dienst nicht der richtige Ort
für diese Abhängigkeit, und keine Formulierung hier ändert diese technische Realität.

## 9. Änderungen dieser Bedingungen

Wir können diese Bedingungen ändern. Betrifft eine Änderung Ihre Rechte wesentlich, teilen
wir sie Ihnen
30 Tage
im Voraus mit. Wenn Sie die Änderung nicht akzeptieren, können Sie Ihr Konto schließen,
bevor sie wirksam wird.

## 10. Anwendbares Recht und Gerichtsstand

Diese Bedingungen unterliegen
italienischem Recht.
Für Streitigkeiten sind ausschließlich
die Gerichte von Bergamo, Italien,
zuständig, **außer** dass Sie als Verbraucher den Schutz der zwingenden Vorschriften des
Landes Ihres Wohnsitzes behalten und vor Ihren örtlichen Gerichten klagen können.

---

**Kontakt:** hello@postqron.com
**Betrieben von:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
