---
document: privacy-policy
version: 1.1.0
effective_date: 2026-08-18
language: fr
status: approved
---

# Politique de confidentialité

Cette politique explique quelles données à caractère personnel Postqron traite, pourquoi,
et ce que vous pouvez y faire. Elle est écrite pour être lue, pas pour être endurée.

## 1. Qui est responsable

Le responsable du traitement de vos données à caractère personnel est
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224.

Vous pouvez nous joindre à
privacy@postqron.com.

Nous n’avons pas désigné de délégué à la protection des données : notre traitement ne
remplit pas les conditions de l’art. 37 du RGPD — nous ne sommes pas une autorité
publique, notre activité de base n’est pas un suivi systématique à grande échelle, et nous
ne traitons pas de catégories particulières de données à grande échelle. Les demandes
relatives à la vie privée s’adressent à l’adresse ci-dessus et nous les traitons
directement.

## 2. Ce que nous traitons, et pourquoi

### 2.1 Compte et authentification

Adresse e-mail, mot de passe (conservé uniquement sous forme d’empreinte Argon2id — nous
ne détenons jamais le mot de passe lui-même), langue préférée, sessions et leur expiration,
et les jetons servant à vérifier votre adresse ou à réinitialiser votre mot de passe.

**Pourquoi :** pour fournir le service que vous avez demandé. **Base légale :** exécution
d’un contrat (art. 6, par. 1, point b, du RGPD).

### 2.2 Tâches et exécutions

Les planifications que vous définissez, les adresses de destination, les méthodes HTTP,
les en-têtes et les corps que vous configurez, et pour chaque exécution : l’heure de début
et de fin, la durée, le résultat, le statut HTTP, un extrait tronqué de la réponse et le
numéro de tentative.

Deux choses méritent d’être dites franchement. Premièrement, **c’est vous qui décidez de
ce qui entre dans une tâche** : si vous mettez des données personnelles dans une URL, un
en-tête ou un corps, nous les traiterons parce que c’est vous qui les y avez mises.
Deuxièmement, **les extraits de réponses sont conservés** : si le système que vous appelez
renvoie des données personnelles, ces données arrivent dans nos journaux.

**Pourquoi :** pour faire fonctionner le service et vous laisser voir ce qui s’est passé.
**Base légale :** exécution d’un contrat.

**Conservation :** les journaux d’exécution sont conservés pendant la durée prévue par
votre formule — 3, 15, 30 ou 90 jours — puis supprimés.

### 2.3 Synchronisation des dépôts

Si vous connectez un dépôt GitHub, nous traitons l’identifiant du dépôt, les événements que
GitHub nous envoie lors de vos push et le contenu du fichier `cron.yaml`. Nous demandons un
accès en lecture seule au contenu et aux métadonnées du dépôt, et rien d’autre.

**Base légale :** exécution d’un contrat.

### 2.4 Secrets et identifiants

Les secrets de l’espace de travail, les clés d’API et les clés de fournisseurs d’IA sont
chiffrés au repos, ne sont jamais restitués en clair après leur enregistrement et ne sont
jamais écrits dans les journaux.

### 2.5 Facturation

Les paiements sont gérés par Paddle en qualité de Merchant of Record (§4). Nous recevons
l’état de l’abonnement, la formule et les identifiants nécessaires à son rapprochement.
**Nous ne voyons jamais votre carte de paiement.**

**Base légale :** exécution d’un contrat et obligation légale pour les documents fiscaux.

### 2.6 Sécurité et audit

Enregistrements des événements sensibles : connexions, changements de formule, révocation
de clés, usurpation d’identité par un administrateur. Les journaux techniques sont
structurés de façon à exclure les secrets et les données personnelles non nécessaires.

**Base légale :** intérêt légitime à exploiter un service sécurisé (art. 6, par. 1,
point f), et obligation légale le cas échéant.

### 2.7 E-mails transactionnels

Nous vous envoyons les e-mails dont vous avez besoin pour utiliser le service : bienvenue,
alertes de tâches en échec, changements de formule, événements de sécurité. Ce n’est pas
du marketing et vous ne pouvez pas vous en désabonner sans fermer votre compte, car c’est
la manière dont le service vous dit les choses.

### 2.8 E-mails marketing

Si vous l’acceptez, nous vous envoyons des e-mails à propos du produit : nouvelles
fonctionnalités, changements qui valent la peine d’être connus, à l’occasion quelque chose
que nous avons écrit.

**Ils sont distincts des e-mails ci-dessus à tous les égards.** La base légale est votre
**consentement** (art. 6, par. 1, point a), demandé pour lui-même et jamais couplé à
l’acceptation des conditions ou à la création d’un compte. Refuser ne vous coûte rien : le
service fonctionne à l’identique.

Chaque message marketing comporte un lien de désabonnement qui fonctionne en un clic et
sans connexion. Le désabonnement n’arrête que les e-mails marketing — vous continuez à
recevoir les e-mails transactionnels que le service doit vous envoyer, car ce n’est pas du
marketing.

Nous gardons trace du moment où vous avez consenti et de celui où vous avez retiré votre
consentement : c’est ainsi que nous pouvons démontrer que nous avions le droit de vous
écrire.

## 3. Fonctions d’IA : un transfert qu’il vaut mieux comprendre

Si vous activez le débogage assisté par IA, vous fournissez **votre propre** clé d’API
d’un fournisseur d’IA (OpenAI, Anthropic ou un autre). Lorsque vous utilisez la fonction,
le contenu du journal d’exécution que vous analysez est envoyé à ce fournisseur sous votre
clé et selon ses conditions.

Cela signifie que vos données quittent notre infrastructure et parviennent à un tiers **que
vous avez choisi**, en vertu d’un contrat **entre vous et lui**. Nous n’y sommes pas
partie, nous ne contrôlons pas ce qu’il fait du contenu, et ce sont ses règles de
conservation qui s’appliquent, pas les nôtres.

La fonction est désactivée tant que vous ne l’activez pas, et chaque analyse est un acte
délibéré. Nous demandons votre consentement explicite avant le premier transfert.

**Base légale :** consentement (art. 6, par. 1, point a), que vous pouvez retirer à tout
moment en supprimant votre clé. Le retrait est sans effet sur les transferts déjà effectués.

## 4. Qui d’autre traite vos données

Nous faisons appel à ces prestataires. Chacun traite les données sur nos instructions, en
vertu d’un accord de sous-traitance.

| Prestataire | Rôle | Où |
|---|---|---|
| Hetzner | Serveurs et base de données | Allemagne |
| Cloudflare | DNS, TLS, CDN, hébergement statique, protection en périphérie | Réseau edge mondial |
| Paddle | Merchant of Record : paiements, facturation, fiscalité | Royaume-Uni |
| Mailronix | Acheminement des e-mails transactionnels | Union européenne — exploité par Apdsoftware, la même entité qui exploite Postqron |
| GitHub | Synchronisation des dépôts, seulement si vous en connectez un | États-Unis |

Nous tenons cette liste à jour. Si nous ajoutons ou changeons un prestataire d’une manière
qui vous concerne, nous mettons cette politique à jour et, lorsque le changement est
substantiel, nous vous le disons avant qu’il prenne effet.

**Transferts hors de l’EEE.** Certains prestataires traitent des données hors de l’Espace
économique européen. Lorsque c’est le cas, nous nous appuyons sur les garanties de
l’art. 46 du RGPD, principalement les clauses contractuelles types de la Commission
européenne, avec les mesures techniques propres au prestataire.

## 5. Combien de temps nous conservons les choses

| Donnée | Conservée |
|---|---|
| Compte et profil | Tant que le compte existe |
| Journaux d’exécution | 3, 15, 30 ou 90 jours, selon la formule |
| Enregistrements d’audit | 24 mois |
| Documents comptables et fiscaux | Selon la loi, en général 10 ans |
| Sauvegardes | 30 jours |

Lorsque vous supprimez votre compte, nous arrêtons l’exécution et révoquons les clés
immédiatement, puis nous supprimons les données après un délai de grâce de
30 jours,
pendant lequel vous pouvez changer d’avis. Les données déjà écrites dans les sauvegardes
disparaissent au fur et à mesure de leur rotation. Seuls survivent à la suppression les
enregistrements que nous devons conserver pour des raisons fiscales ou légales.

Une chose survit à la suppression sans plus vous concerner. Lorsqu’un administrateur est
intervenu sur votre compte, notre journal de sécurité conserve la trace de ce que **lui** a
fait, toute référence à vous étant retirée. Ce qui reste dit qu’une action a eu lieu et qui
l’a effectuée ; cela ne dit plus à l’égard de qui. Nous le conservons parce que, sinon,
fermer un compte effacerait la preuve de l’accès d’une autre personne à celui-ci. Ce n’est
pas un enregistrement que nous gardons pour des raisons fiscales ou légales — c’est un
enregistrement de sécurité sur les actes d’une autre personne.

## 6. Vos droits

Vous pouvez nous demander une copie de vos données, leur rectification, leur effacement,
la limitation du traitement ou vous y opposer, ou encore leur fourniture dans un format
portable. Vous pouvez retirer votre consentement lorsque le traitement repose sur le
consentement.

L’export et la suppression sont disponibles dans l’application sans avoir à nous le
demander. Pour tout le reste, écrivez-nous et nous répondrons dans un délai d’un mois.

Si vous estimez que nous traitons mal vos données, vous pouvez saisir l’autorité de
contrôle de votre pays. En Italie, il s’agit du *Garante per la protezione dei dati
personali*.

## 7. Sécurité

Nous chiffrons les secrets au repos, hachons les mots de passe avec Argon2id, gardons les
journaux exempts d’identifiants, vérifions la signature des webhooks entrants, limitons le
débit de l’authentification et consignons les événements sensibles dans un journal
d’audit.

Nous devrions aussi vous dire ce que nous n’avons pas : Postqron tourne sur un seul
serveur, choisi délibérément pour que l’ordonnanceur et la base de données soient côte à
côte. Ce choix échange la résilience contre la latence. Nous faisons des sauvegardes et
nous en avons testé la restauration, mais une panne de cette machine interrompt le
service.

## 8. Décisions automatisées

Nous ne prenons pas à votre égard de décision produisant des effets juridiques ou vous
affectant de manière significative de façon automatisée, et nous ne vous profilons pas.

## 9. Mineurs

Postqron n’est pas destiné aux personnes de moins de
16 ans.
Nous ne collectons pas sciemment leurs données.

## 10. Modifications

Nous pouvons mettre cette politique à jour. La version et la date d’entrée en vigueur
figurent en tête. Lorsqu’une modification est substantielle, nous vous en informons avant
qu’elle prenne effet et, lorsque la loi l’exige, nous vous redemandons votre consentement.

---

**Contact :** privacy@postqron.com
**Exploité par :** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
