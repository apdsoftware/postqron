---
document: acceptable-use-policy
version: 1.0.0
effective_date: 2026-08-17
language: de
status: approved
---

# Richtlinie zur zulässigen Nutzung

Diese Richtlinie ist Teil der [Nutzungsbedingungen](terms-of-service.md). Sie beschreibt,
was Sie mit Postqron nicht tun dürfen, und was geschieht, wenn Sie es doch tun.

Postqron sendet HTTP-Anfragen an Adressen Ihrer Wahl, nach einem Zeitplan Ihrer Wahl, aus
unserer Infrastruktur und von unseren IP-Adressen. Diese Fähigkeit ist nützlich, und sie
ist zugleich die Fähigkeit, die ein Angreifer haben will. Diese Richtlinie gibt es, damit
der Unterschied zwischen beidem aufgeschrieben ist, statt dem Ermessen überlassen zu
bleiben.

## 1. Für wen sie gilt

Für alle, die Postqron nutzen, in jedem Tarif, den kostenlosen eingeschlossen. Sie gilt
auch für alle, die Sie in Ihren Workspace einladen: Sie sind für deren Nutzung des
Dienstes verantwortlich.

## 2. Was Sie nicht tun dürfen

### 2.1 Systeme angreifen, überlasten oder ausspähen

Sie dürfen Postqron nicht nutzen, um:

- Anfragen an ein System zu senden, das Ihnen nicht gehört oder zu dessen Prüfung Sie
  nicht ausdrücklich befugt sind;
- Last zu erzeugen, die den Dienst irgendeines Systems beeinträchtigen, erschöpfen oder
  verweigern soll, auch durch hochfrequente Zeitpläne, viele Jobs gegen ein einzelnes Ziel
  oder abgestimmte Nutzung mehrerer Konten;
- Hosts, Ports, Pfade oder Zugangsdaten zu scannen, aufzuzählen oder abzutasten;
- Systeme zu erreichen, die nicht öffentlich erreichbar sein sollen, einschließlich
  privater Netze, Loopback-Adressen, Cloud-Metadaten-Endpunkte und interner Dienste —
  unserer wie fremder.

Die Befugnis zählt mehr als die Absicht. Eine geplante Anfrage gegen den Endpunkt eines
Dritten wird nicht dadurch zulässig, dass man sie Health Check nennt.

### 2.2 Unsere Kontrollen umgehen

Sie dürfen nicht versuchen, die technischen Maßnahmen zu umgehen, die diese Richtlinie
durchsetzen, einschließlich Adressfilterung, Ratenbegrenzungen, Tarifgrenzen oder
Ausführungsobergrenzen. Dazu gehört, Weiterleitungen, von Ihnen kontrollierte DNS-Einträge
oder Proxys zu nutzen, um ein Ziel zu erreichen, das wir sonst ablehnen würden.

### 2.3 Den Dienst rechtswidrig oder missbräuchlich nutzen

Sie dürfen Postqron nicht nutzen, um gegen das Gesetz zu verstoßen, Rechte anderer zu
verletzen, Schadsoftware zu verbreiten, unerwünschte Nachrichten zu versenden oder
Inhalte zu verarbeiten, die in den Rechtsordnungen rechtswidrig sind, in denen Sie oder
Ihre Empfänger sich befinden.

### 2.4 Die Herkunft verschleiern

Sie dürfen Anfragen, die von Postqron ausgehen, nicht als von jemand anderem stammend
ausgeben und den Dienst nicht nutzen, um die Herkunft einer Aktivität zu verbergen.

### 2.5 Den Dienst weiterverkaufen oder als eigenen ausgeben

Sie dürfen Dritten die Ausführungsfähigkeit von Postqron nicht ohne schriftliche
Vereinbarung als eigenen Dienst anbieten. Jobs für Ihre eigenen Kunden innerhalb eines
Agency-Workspace auszuführen, ist vorgesehen und erlaubt; ein Produkt auf unserem
Scheduler aufzubauen und es zu verkaufen, ist es nicht.

## 3. Geteilte Ressourcen

Ausgehende Anfragen verlassen uns über IP-Adressen, die alle Kunden teilen, außer wo ein
Tarif eine dedizierte Adresse enthält. Der Ruf dieser Adressen ist ein gemeinsames Gut:
Der Missbrauch eines Kunden verschlechtert den Dienst für alle. Wir setzen diese Richtlinie
durch, um die anderen Kunden zu schützen, nicht um Sie zu überwachen.

Wir können aggregierte Grenzen je Zielhost anwenden und Anfragen an ein Ziel ablehnen oder
verlangsamen, das Anzeichen zeigt, eher ins Visier genommen als bedient zu werden.

## 4. Was wir bei Verstößen tun

Wo die Lage es erlaubt, sprechen wir Sie zuerst an und geben Ihnen Gelegenheit, es in
Ordnung zu bringen. Wo sie es nicht erlaubt — weil der Schaden andauert, weil ein Dritter
angegriffen wird, oder weil wir rechtlich zum Handeln verpflichtet sind — können wir
sofort handeln und es Ihnen danach sagen.

Je nach Schwere können wir:

1. bestimmte Jobs oder Ziele **drosseln oder blockieren**;
2. die betroffenen Jobs **sperren** und Ihr Konto im Übrigen nutzbar lassen;
3. **das Konto sperren** und damit jede Ausführung anhalten;
4. das Konto **beenden**.

Wir sperren das Engste, was den Schaden beendet. Eine Sperrung ist kein Erstattungsfall:
siehe die Bedingungen.

Wo wir sperren oder beenden, behalten Sie das Recht, Ihre Daten
30 Tage lang
zu exportieren, sofern das nicht rechtswidrig ist.

## 5. Missbrauch melden

Wenn Sie glauben, dass jemand Postqron nutzt, um ein System anzugreifen oder zu
missbrauchen, für das Sie verantwortlich sind, schreiben Sie an
abuse@postqron.com.
Nennen Sie die Zieladresse, Zeitstempel in UTC und, sofern verfügbar, die Quell-IP. Wir
gehen Meldungen nach und bestätigen den Eingang
innerhalb von zwei Werktagen.

## 6. Änderungen

Wir können diese Richtlinie aktualisieren. Schränkt eine Änderung wesentlich ein, was
erlaubt ist, teilen wir sie Ihnen
30 Tage
vor dem Wirksamwerden mit, außer wo eine kürzere Frist nötig ist, um andauernden Schaden
zu beenden oder das Gesetz einzuhalten.

---

**Kontakt:** hello@postqron.com
**Betrieben von:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
