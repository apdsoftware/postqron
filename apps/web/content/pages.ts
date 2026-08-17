import type { PublicPages } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'

const sharedFeatures = {
  images: ['screenshots/jobs', 'screenshots/metrics'] as const,
}

export const publicPages: Record<LocaleCode, PublicPages> = {
  en: {
    features: {
      meta: { title: 'Features', lead: 'Repository-defined HTTP scheduling, run history, alerts and controls for every stage.' },
      intro: { title: 'Scheduling you can inspect', lead: 'Define HTTP jobs in the application or in cron.yaml, then follow every run from one place.' },
      features: [
        { icon: 'github', title: 'Schedules as code', text: 'Sync cron.yaml from one repository on Free or from unlimited repositories on paid plans.', highlighted: true },
        { icon: 'checkSquare', title: 'From minutes to seconds', text: 'Minimum resolution is one minute on Free, ten seconds on Pro, and one second on Team and Agency.' },
        { icon: 'barChart', title: 'Useful run history', text: 'Keep logs for 3 days on Free, 15 on Pro, 30 on Team, or 90 on Agency. Team and Agency can export them.' },
        { icon: 'bell', title: 'Alerts where work happens', text: 'Every plan includes email alerts. Paid plans add webhooks, with advanced member and environment controls on Team and Agency.' },
      ],
      showcases: [
        { title: 'Review schedules with the code', text: 'A cron.yaml file keeps schedules close to the application they call. Every push is read again so changes can follow the same review process as code.', bullets: ['Cron expressions with explicit time zones', 'Intervals for sub-minute schedules', 'Separate staging and production environments on paid plans', 'One repository on Free, unlimited on paid plans'], image: sharedFeatures.images[0], imageAlt: 'Postqron job list synced from a repository', imageSide: 'left' },
        { title: 'See each HTTP request', text: 'Postqron records the outcome of every scheduled HTTP request. It does not run shell commands, scripts or containers.', bullets: ['Duration, outcome and response in the run history', 'CSV and JSON export on Team and Agency', 'Metrics and charts on Team and Agency', 'Dedicated static outbound IP on Agency'], image: sharedFeatures.images[1], imageAlt: 'Postqron chart showing job duration', imageSide: 'right' },
      ],
    },
    faq: {
      meta: { title: 'Frequently asked questions', lead: 'Answers based on the current Postqron specification and published plans.' },
      intro: { title: 'Questions, answered plainly', lead: 'If a product decision has not been made, we say so instead of making a promise.' },
      items: [
        { question: 'What can a Postqron job run?', answer: 'A job makes an HTTP request to an address you configure. Postqron does not execute shell commands, scripts, containers or your code.' },
        { question: 'How frequently can jobs run?', answer: 'The minimum resolution is one minute on Free, ten seconds on Pro, and one second on Team and Agency. Sub-minute schedules use interval mode alongside cron expressions.' },
        { question: 'How long are run logs retained?', answer: 'Free retains logs for 3 days, Pro for 15 days, Team for 30 days, and Agency for 90 days. Team includes CSV and JSON export; Agency includes export too.' },
        { question: 'What happens when I downgrade?', answer: 'If you have more active jobs than the new plan allows, we pause all of them and you choose which to switch back on, up to the new limit. We do not pick for you: two jobs that look identical to us may be, to you, one that issues invoices and one that sends a reminder. Nothing is deleted.' },
        { question: 'Is there a free trial?', answer: 'No. Postqron has no trial period. The Free plan is the entry point and includes up to 20 cron jobs.' },
        { question: 'How do repositories and environments vary by plan?', answer: 'Free connects one cron.yaml repository and one environment. Paid plans allow unlimited repositories and separate staging and production environments.' },
      ],
    },
    contact: {
      meta: { title: 'Contact', lead: 'Get in touch with the team that operates Postqron.' },
      intro: { title: 'Talk to Postqron', lead: 'Questions about the product, plans or your account are welcome by email.' },
      details: [
        { label: 'Email', value: 'hello@postqron.com', href: 'mailto:hello@postqron.com' },
        { label: 'Operated by', value: 'Apdsoftware di Carlo Zuffetti' },
        { label: 'Registered address', value: 'Via C. Colombo 15, 24047 Treviglio (BG), Italy' },
        { label: 'Company details', value: 'VAT 03835250162 · REA BG 431224' },
      ],
      responseNote: 'We do not publish a response-time commitment that has not been approved.',
    },
  },
  it: {
    features: {
      meta: { title: 'Funzionalità', lead: 'Schedulazione HTTP definita nel repository, cronologia, avvisi e controlli per ogni fase.' },
      intro: { title: 'Schedulazioni che puoi ispezionare', lead: 'Definisci i job HTTP nell’applicazione o in cron.yaml e segui ogni esecuzione da un solo posto.' },
      features: [
        { icon: 'github', title: 'Schedulazioni come codice', text: 'Sincronizza cron.yaml da un repository su Free o da repository illimitati sui piani a pagamento.', highlighted: true },
        { icon: 'checkSquare', title: 'Dai minuti ai secondi', text: 'Risoluzione minima di un minuto su Free, dieci secondi su Pro e un secondo su Team e Agency.' },
        { icon: 'barChart', title: 'Cronologia utile', text: 'Conserva i log 3 giorni su Free, 15 su Pro, 30 su Team o 90 su Agency. Team e Agency possono esportarli.' },
        { icon: 'bell', title: 'Avvisi dove lavori', text: 'Tutti i piani includono avvisi email. I piani a pagamento aggiungono webhook, con controlli avanzati su Team e Agency.' },
      ],
      showcases: [
        { title: 'Rivedi le schedulazioni insieme al codice', text: 'cron.yaml mantiene le schedulazioni vicino all’applicazione chiamata. Ogni push viene riletto e le modifiche seguono la revisione del codice.', bullets: ['Espressioni cron con fuso orario esplicito', 'Intervalli per le schedulazioni sotto il minuto', 'Ambienti staging e production separati sui piani a pagamento', 'Un repository su Free, illimitati sui piani a pagamento'], image: sharedFeatures.images[0], imageAlt: 'Elenco dei job Postqron sincronizzato da un repository', imageSide: 'left' },
        { title: 'Vedi ogni richiesta HTTP', text: 'Postqron registra l’esito di ogni richiesta HTTP schedulata. Non esegue comandi shell, script o container.', bullets: ['Durata, esito e risposta nella cronologia', 'Esportazione CSV e JSON su Team e Agency', 'Metriche e grafici su Team e Agency', 'IP statico dedicato in uscita su Agency'], image: sharedFeatures.images[1], imageAlt: 'Grafico Postqron della durata dei job', imageSide: 'right' },
      ],
    },
    faq: {
      meta: { title: 'Domande frequenti', lead: 'Risposte basate sulla specifica corrente e sui piani pubblicati di Postqron.' },
      intro: { title: 'Risposte chiare', lead: 'Se una decisione di prodotto non è stata presa, lo diciamo invece di fare una promessa.' },
      items: [
        { question: 'Cosa può eseguire un job Postqron?', answer: 'Un job invia una richiesta HTTP a un indirizzo configurato. Postqron non esegue comandi shell, script, container o il tuo codice.' },
        { question: 'Ogni quanto possono essere eseguiti i job?', answer: 'La risoluzione minima è un minuto su Free, dieci secondi su Pro e un secondo su Team e Agency. Le schedulazioni sotto il minuto usano la modalità intervallo accanto alle espressioni cron.' },
        { question: 'Per quanto tempo vengono conservati i log?', answer: 'Free conserva i log per 3 giorni, Pro per 15, Team per 30 e Agency per 90. Team e Agency includono l’esportazione CSV e JSON.' },
        { question: 'Cosa succede quando faccio un downgrade?', answer: 'Se hai più job attivi di quanti il nuovo piano ne consenta, li sospendiamo tutti e scegli tu quali riattivare, fino al nuovo limite. Non scegliamo noi: due job identici ai nostri occhi possono essere, per te, uno che emette fatture e uno che manda un promemoria. Non cancelliamo niente.' },
        { question: 'Esiste una prova gratuita?', answer: 'No. Postqron non prevede un periodo di prova. Il piano Free è il punto di ingresso e include fino a 20 cronjob.' },
        { question: 'Come cambiano repository e ambienti tra i piani?', answer: 'Free collega un repository cron.yaml e un solo ambiente. I piani a pagamento consentono repository illimitati e ambienti staging e production separati.' },
      ],
    },
    contact: {
      meta: { title: 'Contatti', lead: 'Contatta il team che gestisce Postqron.' },
      intro: { title: 'Parla con Postqron', lead: 'Scrivici per domande sul prodotto, sui piani o sul tuo account.' },
      details: [
        { label: 'Email', value: 'hello@postqron.com', href: 'mailto:hello@postqron.com' },
        { label: 'Gestito da', value: 'Apdsoftware di Carlo Zuffetti' },
        { label: 'Sede legale', value: 'Via C. Colombo 15, 24047 Treviglio (BG), Italia' },
        { label: 'Dati societari', value: 'P. IVA 03835250162 · REA BG 431224' },
      ],
      responseNote: 'Non pubblichiamo tempi di risposta che non siano stati approvati.',
    },
  },
  es: {
    features: {
      meta: { title: 'Funcionalidades', lead: 'Programación HTTP definida en el repositorio, historial, alertas y controles para cada etapa.' },
      intro: { title: 'Programaciones que puedes inspeccionar', lead: 'Define trabajos HTTP en la aplicación o en cron.yaml y sigue cada ejecución desde un solo lugar.' },
      features: [
        { icon: 'github', title: 'Programaciones como código', text: 'Sincroniza cron.yaml desde un repositorio en Free o desde repositorios ilimitados en los planes de pago.', highlighted: true },
        { icon: 'checkSquare', title: 'De minutos a segundos', text: 'Resolución mínima de un minuto en Free, diez segundos en Pro y un segundo en Team y Agency.' },
        { icon: 'barChart', title: 'Un historial útil', text: 'Conserva los logs 3 días en Free, 15 en Pro, 30 en Team o 90 en Agency. Team y Agency permiten exportarlos.' },
        { icon: 'bell', title: 'Alertas donde trabajas', text: 'Todos los planes incluyen alertas por email. Los planes de pago añaden webhooks y Team y Agency ofrecen controles avanzados.' },
      ],
      showcases: [
        { title: 'Revisa las programaciones con el código', text: 'cron.yaml mantiene las programaciones cerca de la aplicación a la que llaman. Cada push se vuelve a leer para que los cambios sigan la revisión del código.', bullets: ['Expresiones cron con zona horaria explícita', 'Intervalos para programaciones de menos de un minuto', 'Entornos staging y production separados en los planes de pago', 'Un repositorio en Free, ilimitados en los planes de pago'], image: sharedFeatures.images[0], imageAlt: 'Lista de trabajos de Postqron sincronizada desde un repositorio', imageSide: 'left' },
        { title: 'Observa cada petición HTTP', text: 'Postqron registra el resultado de cada petición HTTP programada. No ejecuta comandos shell, scripts ni contenedores.', bullets: ['Duración, resultado y respuesta en el historial', 'Exportación CSV y JSON en Team y Agency', 'Métricas y gráficos en Team y Agency', 'IP de salida estática dedicada en Agency'], image: sharedFeatures.images[1], imageAlt: 'Gráfico de Postqron con la duración de los trabajos', imageSide: 'right' },
      ],
    },
    faq: {
      meta: { title: 'Preguntas frecuentes', lead: 'Respuestas basadas en la especificación actual y los planes publicados de Postqron.' },
      intro: { title: 'Respuestas claras', lead: 'Si una decisión de producto aún no se ha tomado, lo decimos en lugar de prometerla.' },
      items: [
        { question: '¿Qué puede ejecutar un trabajo de Postqron?', answer: 'Un trabajo realiza una petición HTTP a una dirección configurada. Postqron no ejecuta comandos shell, scripts, contenedores ni tu código.' },
        { question: '¿Con qué frecuencia pueden ejecutarse los trabajos?', answer: 'La resolución mínima es un minuto en Free, diez segundos en Pro y un segundo en Team y Agency. Las programaciones de menos de un minuto usan el modo intervalo junto a las expresiones cron.' },
        { question: '¿Cuánto tiempo se conservan los logs?', answer: 'Free conserva los logs 3 días, Pro 15, Team 30 y Agency 90. Team y Agency incluyen exportación CSV y JSON.' },
        { question: '¿Qué ocurre al bajar de plan?', answer: 'Si tienes más trabajos activos de los que permite el nuevo plan, los pausamos todos y tú eliges cuáles reactivar, hasta el nuevo límite. No elegimos nosotros: dos trabajos idénticos a nuestros ojos pueden ser, para ti, uno que emite facturas y otro que envía un recordatorio. No borramos nada.' },
        { question: '¿Hay una prueba gratuita?', answer: 'No. Postqron no ofrece un período de prueba. El plan Free es la entrada e incluye hasta 20 trabajos cron.' },
        { question: '¿Cómo cambian los repositorios y entornos entre planes?', answer: 'Free conecta un repositorio cron.yaml y un entorno. Los planes de pago permiten repositorios ilimitados y entornos staging y production separados.' },
      ],
    },
    contact: {
      meta: { title: 'Contacto', lead: 'Ponte en contacto con el equipo que opera Postqron.' },
      intro: { title: 'Habla con Postqron', lead: 'Escríbenos si tienes preguntas sobre el producto, los planes o tu cuenta.' },
      details: [
        { label: 'Email', value: 'hello@postqron.com', href: 'mailto:hello@postqron.com' },
        { label: 'Operado por', value: 'Apdsoftware di Carlo Zuffetti' },
        { label: 'Domicilio social', value: 'Via C. Colombo 15, 24047 Treviglio (BG), Italia' },
        { label: 'Datos de la empresa', value: 'NIF-IVA 03835250162 · REA BG 431224' },
      ],
      responseNote: 'No publicamos compromisos de tiempo de respuesta que no hayan sido aprobados.',
    },
  },
  de: {
    features: {
      meta: { title: 'Funktionen', lead: 'HTTP-Zeitpläne aus dem Repository, Ausführungsverlauf, Benachrichtigungen und Kontrollen für jede Phase.' },
      intro: { title: 'Nachvollziehbare Zeitpläne', lead: 'Definiere HTTP-Jobs in der Anwendung oder in cron.yaml und verfolge jede Ausführung an einem Ort.' },
      features: [
        { icon: 'github', title: 'Zeitpläne als Code', text: 'Synchronisiere cron.yaml im Free-Tarif aus einem Repository und in bezahlten Tarifen aus unbegrenzt vielen.', highlighted: true },
        { icon: 'checkSquare', title: 'Von Minuten zu Sekunden', text: 'Die Mindestauflösung beträgt eine Minute bei Free, zehn Sekunden bei Pro und eine Sekunde bei Team und Agency.' },
        { icon: 'barChart', title: 'Nützlicher Verlauf', text: 'Logs bleiben bei Free 3, bei Pro 15, bei Team 30 und bei Agency 90 Tage erhalten. Team und Agency können sie exportieren.' },
        { icon: 'bell', title: 'Benachrichtigungen am richtigen Ort', text: 'Alle Tarife enthalten E-Mail-Benachrichtigungen. Bezahlte Tarife ergänzen Webhooks; Team und Agency bieten erweiterte Kontrollen.' },
      ],
      showcases: [
        { title: 'Zeitpläne gemeinsam mit Code prüfen', text: 'cron.yaml hält Zeitpläne nahe bei der aufgerufenen Anwendung. Jeder Push wird neu eingelesen, sodass Änderungen denselben Prüfprozess wie Code durchlaufen.', bullets: ['Cron-Ausdrücke mit expliziter Zeitzone', 'Intervalle für Zeitpläne unter einer Minute', 'Getrennte Staging- und Production-Umgebungen in bezahlten Tarifen', 'Ein Repository bei Free, unbegrenzt viele in bezahlten Tarifen'], image: sharedFeatures.images[0], imageAlt: 'Aus einem Repository synchronisierte Jobliste in Postqron', imageSide: 'left' },
        { title: 'Jede HTTP-Anfrage sehen', text: 'Postqron zeichnet das Ergebnis jeder geplanten HTTP-Anfrage auf. Shell-Befehle, Skripte oder Container werden nicht ausgeführt.', bullets: ['Dauer, Ergebnis und Antwort im Verlauf', 'CSV- und JSON-Export bei Team und Agency', 'Metriken und Diagramme bei Team und Agency', 'Dedizierte statische Ausgangs-IP bei Agency'], image: sharedFeatures.images[1], imageAlt: 'Postqron-Diagramm zur Jobdauer', imageSide: 'right' },
      ],
    },
    faq: {
      meta: { title: 'Häufig gestellte Fragen', lead: 'Antworten auf Grundlage der aktuellen Postqron-Spezifikation und der veröffentlichten Tarife.' },
      intro: { title: 'Klare Antworten', lead: 'Wenn eine Produktentscheidung noch offen ist, sagen wir das, statt ein Versprechen zu erfinden.' },
      items: [
        { question: 'Was kann ein Postqron-Job ausführen?', answer: 'Ein Job sendet eine HTTP-Anfrage an eine konfigurierte Adresse. Postqron führt keine Shell-Befehle, Skripte, Container oder deinen Code aus.' },
        { question: 'Wie häufig können Jobs laufen?', answer: 'Die Mindestauflösung beträgt eine Minute bei Free, zehn Sekunden bei Pro und eine Sekunde bei Team und Agency. Zeitpläne unter einer Minute verwenden neben Cron-Ausdrücken den Intervallmodus.' },
        { question: 'Wie lange werden Logs aufbewahrt?', answer: 'Free bewahrt Logs 3 Tage auf, Pro 15, Team 30 und Agency 90. Team und Agency enthalten CSV- und JSON-Export.' },
        { question: 'Was passiert bei einem Downgrade?', answer: 'Wenn Sie mehr aktive Jobs haben, als der neue Tarif erlaubt, pausieren wir alle und Sie wählen, welche wieder laufen sollen — bis zum neuen Limit. Wir wählen nicht für Sie: zwei für uns identische Jobs können für Sie einer sein, der Rechnungen stellt, und einer, der eine Erinnerung schickt. Nichts wird gelöscht.' },
        { question: 'Gibt es eine kostenlose Testphase?', answer: 'Nein. Postqron bietet keine Testphase. Der Free-Tarif ist der Einstieg und umfasst bis zu 20 Cronjobs.' },
        { question: 'Wie unterscheiden sich Repositories und Umgebungen?', answer: 'Free verbindet ein cron.yaml-Repository und eine Umgebung. Bezahlte Tarife erlauben unbegrenzt viele Repositories sowie getrennte Staging- und Production-Umgebungen.' },
      ],
    },
    contact: {
      meta: { title: 'Kontakt', lead: 'Nimm Kontakt mit dem Team auf, das Postqron betreibt.' },
      intro: { title: 'Sprich mit Postqron', lead: 'Fragen zum Produkt, zu Tarifen oder zu deinem Konto beantworten wir per E-Mail.' },
      details: [
        { label: 'E-Mail', value: 'hello@postqron.com', href: 'mailto:hello@postqron.com' },
        { label: 'Betreiber', value: 'Apdsoftware di Carlo Zuffetti' },
        { label: 'Geschäftsanschrift', value: 'Via C. Colombo 15, 24047 Treviglio (BG), Italien' },
        { label: 'Unternehmensdaten', value: 'USt-IdNr. 03835250162 · REA BG 431224' },
      ],
      responseNote: 'Wir veröffentlichen keine nicht genehmigte Zusage zur Antwortzeit.',
    },
  },
  fr: {
    features: {
      meta: { title: 'Fonctionnalités', lead: 'Planification HTTP définie dans le dépôt, historique, alertes et contrôles pour chaque étape.' },
      intro: { title: 'Des planifications vérifiables', lead: 'Définissez les tâches HTTP dans l’application ou dans cron.yaml, puis suivez chaque exécution au même endroit.' },
      features: [
        { icon: 'github', title: 'La planification comme code', text: 'Synchronisez cron.yaml depuis un dépôt avec Free ou depuis un nombre illimité de dépôts avec les offres payantes.', highlighted: true },
        { icon: 'checkSquare', title: 'Des minutes aux secondes', text: 'La résolution minimale est d’une minute avec Free, dix secondes avec Pro et une seconde avec Team et Agency.' },
        { icon: 'barChart', title: 'Un historique utile', text: 'Conservez les logs 3 jours avec Free, 15 avec Pro, 30 avec Team ou 90 avec Agency. Team et Agency permettent leur export.' },
        { icon: 'bell', title: 'Des alertes là où vous travaillez', text: 'Toutes les offres incluent les alertes par e-mail. Les offres payantes ajoutent les webhooks, avec des contrôles avancés pour Team et Agency.' },
      ],
      showcases: [
        { title: 'Révisez les planifications avec le code', text: 'cron.yaml garde les planifications près de l’application appelée. Chaque push est relu afin que les modifications suivent la même revue que le code.', bullets: ['Expressions cron avec fuseau horaire explicite', 'Intervalles pour les planifications inférieures à une minute', 'Environnements staging et production séparés avec les offres payantes', 'Un dépôt avec Free, un nombre illimité avec les offres payantes'], image: sharedFeatures.images[0], imageAlt: 'Liste des tâches Postqron synchronisée depuis un dépôt', imageSide: 'left' },
        { title: 'Voyez chaque requête HTTP', text: 'Postqron enregistre le résultat de chaque requête HTTP planifiée. Il n’exécute ni commandes shell, ni scripts, ni conteneurs.', bullets: ['Durée, résultat et réponse dans l’historique', 'Export CSV et JSON avec Team et Agency', 'Métriques et graphiques avec Team et Agency', 'Adresse IP sortante statique dédiée avec Agency'], image: sharedFeatures.images[1], imageAlt: 'Graphique Postqron montrant la durée des tâches', imageSide: 'right' },
      ],
    },
    faq: {
      meta: { title: 'Questions fréquentes', lead: 'Des réponses fondées sur la spécification actuelle et les offres publiées de Postqron.' },
      intro: { title: 'Des réponses claires', lead: 'Si une décision produit n’est pas prise, nous le disons au lieu d’inventer une promesse.' },
      items: [
        { question: 'Que peut exécuter une tâche Postqron ?', answer: 'Une tâche envoie une requête HTTP à une adresse configurée. Postqron n’exécute ni commandes shell, ni scripts, ni conteneurs, ni votre code.' },
        { question: 'À quelle fréquence les tâches peuvent-elles s’exécuter ?', answer: 'La résolution minimale est d’une minute avec Free, dix secondes avec Pro et une seconde avec Team et Agency. Les planifications inférieures à une minute utilisent le mode intervalle avec les expressions cron.' },
        { question: 'Combien de temps les logs sont-ils conservés ?', answer: 'Free conserve les logs 3 jours, Pro 15, Team 30 et Agency 90. Team et Agency incluent l’export CSV et JSON.' },
        { question: 'Que se passe-t-il lors d’un passage à une offre inférieure ?', answer: 'Si vous avez plus de tâches actives que le nouveau forfait n\'en permet, nous les mettons toutes en pause et vous choisissez lesquelles réactiver, dans la limite du nouveau forfait. Nous ne choisissons pas à votre place : deux tâches identiques à nos yeux peuvent être, pour vous, l\'une qui émet des factures et l\'autre qui envoie un rappel. Rien n\'est supprimé.' },
        { question: 'Existe-t-il une période d’essai gratuite ?', answer: 'Non. Postqron ne propose pas de période d’essai. L’offre Free est le point d’entrée et inclut jusqu’à 20 tâches cron.' },
        { question: 'Comment les dépôts et environnements varient-ils selon l’offre ?', answer: 'Free connecte un dépôt cron.yaml et un environnement. Les offres payantes permettent un nombre illimité de dépôts et des environnements staging et production séparés.' },
      ],
    },
    contact: {
      meta: { title: 'Contact', lead: 'Contactez l’équipe qui exploite Postqron.' },
      intro: { title: 'Parlez à Postqron', lead: 'Écrivez-nous pour toute question sur le produit, les offres ou votre compte.' },
      details: [
        { label: 'E-mail', value: 'hello@postqron.com', href: 'mailto:hello@postqron.com' },
        { label: 'Exploité par', value: 'Apdsoftware di Carlo Zuffetti' },
        { label: 'Siège social', value: 'Via C. Colombo 15, 24047 Treviglio (BG), Italie' },
        { label: 'Informations légales', value: 'TVA 03835250162 · REA BG 431224' },
      ],
      responseNote: 'Nous ne publions aucun engagement de délai de réponse qui n’a pas été approuvé.',
    },
  },
}
