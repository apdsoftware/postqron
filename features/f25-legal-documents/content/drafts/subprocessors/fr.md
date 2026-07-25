---
document: subprocessors
locale: fr
version: "0.1"
title: "Registre des sous-traitants de Postqron"
controllerName: "APDSoftware — exploitant de Postqron"
contactEmail: help@postqron.com
status: draft_pending_legal_review
changeType: material
revisionSummary: "Brouillon initial rédigé de zéro, en attente de validation juridique."
---

## Identité du fournisseur

Le présent registre est publié par APDSoftware, exploitant de Postqron, joignable à l'adresse help@postqron.com et via https://apdsoftware.it. La dénomination sociale complète, le siège social et le numéro de TVA de l'entité contractante enregistrée sont enregistrés comme métadonnées en attente de validation juridique et seront indiqués ici dès leur confirmation.

## Objet du présent registre

Il s'agit du registre public, mis à jour régulièrement, des sous-traitants et autres tiers auxquels Postqron fait appel pour fournir son service, auquel renvoient les Conditions Générales de Service, la Politique de Confidentialité et l'Accord de Traitement des Données, plutôt que d'y être dupliqué. Il distingue les fournisseurs agissant en tant que nos sous-traitants au titre de l'article 28 du RGPD (traitement de données personnelles sur instruction de Postqron) des tiers indépendants (tels que les fournisseurs d'identité OAuth) qui agissent en tant que responsables de traitement autonomes pour l'étape du service qu'ils exécutent. Chaque entrée ci-dessous est établie uniquement à partir de sources primaires et officielles citées par URL, avec la date à laquelle chaque source a été consultée. Lorsqu'un fait n'a pas pu être vérifié auprès d'une source officielle, cette lacune est indiquée explicitement plutôt que comblée.

L'ajout ou le remplacement d'un sous-traitant qui traitera des données de contenu client suit la procédure de notification et d'opposition décrite dans l'Accord de Traitement des Données : un préavis d'au moins 30 jours aux propriétaires (Owners) de l'espace de travail, un canal permettant de formuler une objection motivée, et la suspension de l'activation pour le client qui s'y oppose jusqu'à la résolution de l'objection. Un historique des fournisseurs retirés est conservé sous le tableau actif dès qu'un fournisseur est retiré.

## Sous-traitants et tiers actifs

| Dénomination sociale | Rôle | Service | Catégories de données | Établissement | Lieu de traitement | Mécanisme de transfert | Référence de l'Accord de Traitement des Données | Source (consultée le 2026-07-25) |
|---|---|---|---|---|---|---|---|---|
| Paddle.com Market Limited (entité contractante) ; Paddle.com Inc. (sous-traitant au titre de l'Accord de Traitement des Données) ; Paddle Payments Limited ; Paddle.com Canada Ltd | Sous-traitant | Traitement des paiements et facturation en tant que Merchant of Record | Données de contact de facturation ; métadonnées d'abonnement/transaction | Royaume-Uni ; Irlande ; États-Unis ; Canada | Non communiqué par Paddle ; peut être traité par toute entité du groupe Paddle | Clauses contractuelles types | [Avenant sur le traitement des données de Paddle](https://www.paddle.com/legal/data-processing-addendum) | [Accord de Traitement des Données de Paddle](https://www.paddle.com/legal/data-processing-addendum) |
| Hetzner Online GmbH | Sous-traitant | Infrastructure d'hébergement cloud (calcul, stockage, sauvegardes) | Données de compte ; données d'espace de travail et de contenu ; sauvegardes chiffrées | Allemagne | Union européenne/EEE lorsqu'un emplacement de serveur dans l'UE est sélectionné, conformément à la préférence d'hébergement UE/EEE en priorité de Postqron | Traitement dans l'UE/EEE (aucun transfert vers un pays tiers lorsqu'un emplacement de l'UE est utilisé) | [Auftragsverarbeitungsvertrag (DPA) de Hetzner](https://www.hetzner.com/AV/DPA_en.pdf) | [Accord de Traitement des Données de Hetzner](https://www.hetzner.com/AV/DPA_en.pdf) |
| Cloudflare, Inc. | Sous-traitant | DNS, CDN, réseau en périphérie (edge) et terminaison TLS | Métadonnées de réseau et de trafic ; adresses IP | États-Unis | Réseau en périphérie mondial ; peut traiter des données en dehors de l'EEE, de la Suisse et du Royaume-Uni selon les services configurés | Clauses contractuelles types (également certifié EU-US Data Privacy Framework et Global CBPR) | [Accord de Traitement des Données clients de Cloudflare](https://www.cloudflare.com/cloudflare-customer-dpa/) | [Accord de Traitement des Données de Cloudflare](https://www.cloudflare.com/cloudflare-customer-dpa/) |
| Non vérifié (« Mailronix ») | Sous-traitant | Envoi d'e-mails transactionnels (notifications de compte, de sécurité et de service) | Adresse e-mail du destinataire ; nom du destinataire ; contenu du message transactionnel | Non vérifié | Non vérifié | Sans objet — aucune source vérifiée | Non disponible | Contrat d'API interne uniquement (`features/f14-email/contracts/mailronix-api-1.0.0.md`) ; ce n'est pas une source juridique publique |
| Google LLC ; Google Ireland Limited | Tiers indépendant | Connexion OAuth (« Se connecter avec Google ») | Adresse e-mail ; nom affiché ; photo de profil ; identifiant du compte Google | États-Unis ; Irlande | Mondial | EU-US et Swiss-US Data Privacy Framework ; clauses contractuelles types lorsque le Framework ne s'applique pas | Sans objet — aucun accord de traitement des données dédié publié pour cette fonctionnalité | [Conditions de Service des API Google](https://developers.google.com/terms) |
| Apple Inc. | Tiers indépendant | Connexion OAuth (« Se connecter avec Apple ») | Adresse e-mail (ou adresse de relais privé Apple) ; nom (à la première connexion uniquement) ; identifiant du compte Apple | États-Unis | Non vérifié | Non vérifié | Sans objet — aucun accord de traitement des données dédié publié pour cette fonctionnalité | [Se connecter avec Apple et confidentialité](https://www.apple.com/legal/privacy/data/en/sign-in-with-apple/) |
| Meta Platforms, Inc. ; Meta Platforms Ireland Limited | Tiers indépendant | Connexion OAuth (« Facebook Login ») et la connexion propre du client à des Pages Facebook / Instagram Professionnel comme destination de publication | Adresse e-mail ; nom ; photo de profil ; identifiant du compte Facebook/Instagram ; contenu que le client choisit de publier sur son compte connecté | Irlande ; États-Unis | Non vérifié | Clauses contractuelles types ; Meta Platforms, Inc. également certifiée Data Privacy Framework | Sans objet — aucun accord de traitement des données dédié publié pour cette fonctionnalité | [Conditions de la Plateforme Meta](https://developers.facebook.com/terms/dfc_platform_terms/) |
| LinkedIn Corporation ; LinkedIn Ireland Unlimited Company | Tiers indépendant | Connexion OAuth (« Se connecter avec LinkedIn ») | Adresse e-mail ; nom ; photo de profil ; identifiant du compte LinkedIn | États-Unis ; Irlande | États-Unis | Clauses contractuelles types ; LinkedIn Corporation également certifiée Data Privacy Framework | Référence croisée uniquement — l'accord de traitement des données pour le développement commercial de LinkedIn est lié depuis ses Conditions d'utilisation de l'API mais ne mentionne pas expressément cette fonctionnalité | [Conditions d'utilisation de l'API LinkedIn](https://www.linkedin.com/legal/l/api-terms-of-use) |

## Lacunes connues à résoudre avant publication

- **« Mailronix » n'a pas pu être vérifié.** Aucun site web officiel, page juridique, accord de traitement des données ou liste de sous-traitants n'a pu être localisé pour une entreprise réelle opérant sous ce nom, malgré une recherche de sources primaires. Cette entrée ne peut pas être publiée comme approuvée tant que la fonction de gestion des fournisseurs de Postqron n'a pas confirmé l'entité juridique contractante exacte et fourni sa documentation officielle.
- **Les déclarations relatives au lieu de traitement d'Apple et de Meta** n'ont pas été trouvées sur les pages officielles consultées et nécessitent une confirmation directe avant publication.
- **L'applicabilité de l'accord de traitement des données pour le développement commercial de LinkedIn à la connexion OAuth en particulier** est déduite uniquement par référence croisée et devrait être confirmée directement auprès de LinkedIn ou du conseil juridique.
- **La liste des sous-traitants du Trust Center de Paddle** (une page générée en JavaScript) n'a pas pu être lue par la recherche automatisée et son contenu n'est pas vérifié de manière indépendante ; le texte de l'accord de traitement des données lui-même renvoie par ailleurs vers un lien obsolète vers une ancienne liste de sous-traitants, qui devrait être clarifié auprès de Paddle.

## Sous-traitants retirés

Aucun enregistré à la date de cette révision du brouillon.

## Contact

Toute question relative au présent registre, ou toute objection à un sous-traitant répertorié au titre de l'Accord de Traitement des Données, doit être adressée à help@postqron.com.
