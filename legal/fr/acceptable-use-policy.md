---
document: acceptable-use-policy
version: 1.0.0
effective_date: 2026-08-17
language: fr
status: pending-review
---

# Politique d’utilisation acceptable

Cette politique fait partie des [Conditions d’utilisation](terms-of-service.md). Elle
décrit ce que vous ne pouvez pas faire avec Postqron, et ce qui se passe si vous le faites.

Postqron envoie des requêtes HTTP vers les adresses que vous choisissez, selon la
planification que vous choisissez, depuis notre infrastructure et nos adresses IP. Cette
capacité est utile, et c’est aussi celle que recherche un attaquant. Cette politique existe
pour que la différence entre les deux soit écrite plutôt que laissée à l’appréciation.

## 1. À qui elle s’applique

À toute personne qui utilise Postqron, quelle que soit la formule, y compris la formule
gratuite. Elle s’applique aussi à toute personne que vous invitez dans votre espace de
travail : vous êtes responsable de son utilisation du service.

## 2. Ce que vous ne devez pas faire

### 2.1 Attaquer, surcharger ou sonder des systèmes

Vous ne devez pas utiliser Postqron pour :

- envoyer des requêtes vers un système qui ne vous appartient pas ou que vous n’êtes pas
  expressément autorisé à tester ;
- générer une charge destinée à dégrader, épuiser ou refuser le service de quelque système
  que ce soit, y compris par des planifications à haute fréquence, de nombreuses tâches
  contre une même cible, ou l’usage coordonné de plusieurs comptes ;
- scanner, énumérer ou sonder des hôtes, des ports, des chemins ou des identifiants ;
- atteindre des systèmes qui ne sont pas destinés à être accessibles publiquement, y
  compris les réseaux privés, les adresses de bouclage, les points de métadonnées cloud et
  les services internes — les nôtres comme ceux d’autrui.

L’autorisation compte davantage que l’intention. Planifier une requête vers le point
d’accès d’un tiers ne devient pas acceptable parce qu’on l’appelle contrôle de santé.

### 2.2 Contourner nos contrôles

Vous ne devez pas tenter de contourner les mesures techniques qui font appliquer cette
politique, y compris le filtrage d’adresses, les limites de débit, les limites de formule
ou les plafonds d’exécution. Cela comprend l’usage de redirections, d’entrées DNS sous
votre contrôle ou de mandataires pour atteindre une destination que nous refuserions
autrement.

### 2.3 Utiliser le service de manière illicite ou abusive

Vous ne devez pas utiliser Postqron pour enfreindre la loi, porter atteinte aux droits
d’autrui, distribuer des logiciels malveillants, envoyer des messages non sollicités, ou
traiter des contenus illicites dans les juridictions où vous ou vos destinataires vous
trouvez.

### 2.4 Travestir l’origine

Vous ne devez pas présenter des requêtes provenant de Postqron comme émanant de quelqu’un
d’autre, ni utiliser le service pour dissimuler l’origine d’une activité.

### 2.5 Revendre le service ou l’exposer comme le vôtre

Vous ne devez pas proposer à des tiers la capacité d’exécution de Postqron comme un service
qui serait le vôtre sans accord écrit. Exécuter des tâches pour le compte de vos propres
clients au sein d’un espace de travail Agency est prévu et autorisé ; bâtir un produit
par-dessus notre ordonnanceur et le vendre ne l’est pas.

## 3. Ressources partagées

Les requêtes sortantes partent d’adresses IP partagées par tous les clients, sauf lorsqu’une
formule comprend une adresse dédiée. La réputation de ces adresses est un bien commun :
l’abus d’un client dégrade le service pour tous. Nous faisons appliquer cette politique
pour protéger les autres clients, pas pour vous surveiller.

Nous pouvons appliquer des limites agrégées par hôte de destination, et nous pouvons
refuser ou ralentir les requêtes vers une destination qui montre les signes d’être prise
pour cible plutôt que servie.

## 4. Ce que nous faisons en cas de manquement

Lorsque la situation le permet, nous vous contactons d’abord et vous donnons l’occasion de
corriger. Lorsqu’elle ne le permet pas — parce que le dommage est en cours, parce qu’un
tiers est attaqué, ou parce que la loi nous oblige à agir — nous pouvons agir
immédiatement et vous le dire ensuite.

Selon la gravité, nous pouvons :

1. **limiter ou bloquer** des tâches ou des destinations précises ;
2. **suspendre** les tâches concernées en laissant votre compte utilisable par ailleurs ;
3. **suspendre le compte**, arrêtant toute exécution ;
4. **résilier** le compte.

Nous suspendons la chose la plus étroite qui arrête le dommage. Une suspension n’est pas un
cas de remboursement : voir les Conditions.

Lorsque nous suspendons ou résilions, vous conservez le droit d’exporter vos données
pendant 30 jours,
sauf si cela est illicite.

## 5. Signaler un abus

Si vous pensez que quelqu’un utilise Postqron pour attaquer ou abuser d’un système dont
vous êtes responsable, écrivez à
abuse@postqron.com.
Indiquez l’adresse de destination, les horodatages en UTC et, s’ils sont disponibles, l’IP
source. Nous enquêtons sur les signalements et en confirmerons la réception
sous deux jours ouvrés.

## 6. Modifications

Nous pouvons mettre cette politique à jour. Lorsqu’une modification restreint
substantiellement ce qui est permis, nous vous en informons
30 jours
avant qu’elle prenne effet, sauf lorsqu’un délai plus court est nécessaire pour arrêter un
dommage en cours ou pour respecter la loi.

---

**Contact :** hello@postqron.com
**Exploité par :** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
