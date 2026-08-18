---
document: terms-of-service
version: 1.2.0
effective_date: 2026-08-18
language: fr
status: pending-review
---

# Conditions d’utilisation

Ces conditions régissent votre utilisation de Postqron. En créant un compte, vous les
acceptez, avec la [Politique d’utilisation acceptable](acceptable-use-policy.md) et la
[Politique de confidentialité](privacy-policy.md).

## 1. Avec qui vous contractez

Postqron est exploité par
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
(« nous »).

**Les achats se font par l’intermédiaire de Paddle.** Paddle agit en qualité de
Merchant of Record : lorsque vous achetez une formule payante, le contrat de vente relatif
à cet achat est conclu entre vous et Paddle, et les conditions acheteur de Paddle s’y
appliquent en plus des présentes conditions. Paddle gère le paiement, la facturation et la
fiscalité. Nous gérons le service.

## 2. Ce que fait le service

Postqron exécute des requêtes HTTP vers les adresses que vous configurez, aux moments que
vous configurez, enregistre le résultat et vous avertit en cas d’échec. Les planifications
peuvent être définies dans l’application ou dans un fichier `cron.yaml` au sein d’un dépôt
que vous connectez.

**Postqron n’exécute pas votre code.** Il effectue des requêtes HTTP. Si une requête
déclenche un traitement sur vos systèmes, ce traitement est le vôtre.

## 3. Votre compte

Vous êtes responsable de ce qui se passe sous votre compte, de la sécurité de vos
identifiants et des personnes que vous invitez dans votre espace de travail.
Prévenez-nous sans délai si vous estimez que votre compte a été compromis.

Vous devez avoir au moins 16 ans et, si vous agissez pour une organisation, être habilité
à l’engager.

**La formule gratuite est ouverte à tous.** Utilisez-la pour un projet personnel, pour
essayer le service, ou parce qu’elle suffit à ce dont vous avez besoin. Rien ici ne vous
demande d’être une entreprise pour créer un compte.

**Les formules payantes sont proposées pour un usage professionnel.** En en achetant une,
vous confirmez agir dans le cadre d’une activité commerciale, industrielle, artisanale ou
libérale. C’est pourquoi nos prix sont affichés hors taxes : pour qui exerce une activité,
le montant net est celui qui compte, parce que c’est celui qu’on déduit. Nous vous
demandons de le confirmer au moment du paiement, et nous recueillons votre numéro de TVA
lorsque vous en avez un — certains régimes de petite entreprise parfaitement légitimes en
Europe n’en délivrent pas, nous le demandons donc sans l’exiger.

Lorsque la loi vous accorde des protections de consommateur malgré cette confirmation, la
loi l’emporte — y compris le droit de rétractation prévu au §4.3.

## 4. Formules, limites et paiement

Les formules, prix et limites sont ceux publiés sur notre page de tarifs et appliqués par
le service. **Les limites sont imposées par le moteur**, et pas seulement énoncées : le
nombre de tâches d’une formule, l’intervalle minimal et la conservation des journaux sont
des plafonds réels.

Les prix sont affichés **hors taxes**. Paddle calcule et ajoute la taxe applicable selon
votre localisation.

Les formules payantes se renouvellent automatiquement pour la même période jusqu’à
résiliation. Vous pouvez résilier à tout moment ; la résiliation prend effet à la fin de
la période déjà payée, et le service se poursuit jusque-là.

### 4.1 Changement de formule

Les montées en gamme prennent effet immédiatement. **Les descentes en gamme prennent effet
à la fin de la période en cours**, et nous vous indiquons ce qui va se passer avant que
vous confirmiez.

**Si vous avez plus de tâches actives que la formule inférieure ne l’autorise, nous les
mettons toutes en pause et c’est vous qui choisissez lesquelles réactiver**, dans la
limite du nouveau plafond. Nous ne choisissons pas à votre place, parce que nous ne le
pouvons pas : deux tâches qui nous semblent identiques peuvent être, pour vous, l’une qui
émet des factures et l’autre qui envoie un rappel. Toute règle automatique que nous
aurions inventée devinerait — et devinerait mal précisément là où cela compte le plus.

Si vos tâches actives tiennent déjà dans le nouveau plafond, rien n’est mis en pause.

**Nous ne supprimons pas votre travail.** Les tâches en pause restent visibles,
modifiables et exportables, avec leur historique d’exécutions. Une chose à savoir : une
tâche planifiée plus fréquemment que la nouvelle formule ne l’autorise ne peut pas être
réactivée tant que vous n’en changez pas la planification, même s’il reste de la place.

Il en va de même si un paiement échoue définitivement ou si un abonnement s’éteint : les
deux ramènent le compte à la formule gratuite.

### 4.2 Paiement échoué

Si un paiement échoue, Paddle le réessaie selon son propre calendrier. Pendant cette
période, votre service se poursuit. Si le paiement échoue définitivement, le compte passe
à la formule gratuite et le §4.1 s’applique sans changement : si vous avez plus de tâches
actives que la formule gratuite ne l’autorise, elles sont toutes mises en pause et c’est
vous qui choisissez lesquelles réactiver. Rien n’est supprimé.

### 4.3 Remboursements et rétractation

La règle est simple : **vous pouvez arrêter quand vous voulez, et le mois que vous avez
déjà payé va jusqu’à son terme.** Rien n’est remboursé au prorata, et il n’y a rien à
réclamer ni à négocier.

Si vous êtes consommateur dans l’Union européenne, vous disposez en outre d’un droit légal
de rétractation dans les 14 jours suivant l’achat. Le service démarrant immédiatement, il
vous est demandé de consentir à son exécution immédiate ; ce consentement met fin au droit
de rétractation une fois le service pleinement exécuté. Lorsque la loi nous impose malgré
tout de vous rembourser, nous le faisons, et Paddle traite le remboursement.

## 5. Disponibilité

Nous visons un service disponible en continu, et nous vous prévenons lorsqu’il ne l’est
pas (voir la Politique d’utilisation acceptable pour la manière dont nous vous contactons
en cas d’incident).

**Nous n’offrons pas de garantie de disponibilité, et nous tenons à dire franchement
pourquoi.** L’ordonnanceur et la base de données tournent sur un seul serveur, choisi
délibérément pour que le déclenchement ne soit pas retardé par la latence réseau. Ce choix
échange la résilience contre la précision. Nous faisons des sauvegardes et nous testons
leur restauration, mais une panne de cette machine interrompt le service. Tout engagement
que nous prendrions au-delà de ce qu’une seule machine peut tenir serait un engagement que
nous ne pourrions pas honorer.

Si nous proposons un jour un accord de niveau de service assorti d’engagements mesurables,
il figurera ici — et l’architecture aura changé avant, pas après.

## 6. Vos contenus et les nôtres

**Ce qui est à vous reste à vous.** Vos planifications, votre configuration, vos journaux
et les données que vous faites transiter par le service demeurent votre propriété. Vous
nous accordez uniquement l’autorisation dont nous avons besoin pour exploiter le service
pour vous : stocker ces données, exécuter les requêtes que vous configurez et vous en
montrer les résultats.

Postqron lui-même — le logiciel, l’interface, le nom et la marque — reste le nôtre. Ces
conditions vous donnent le droit d’utiliser le service, pas de le copier ni de le
revendre.

## 7. Suspension et résiliation

Nous pouvons suspendre ou fermer votre compte en cas de manquement substantiel aux
présentes conditions ou à la Politique d’utilisation acceptable, selon les modalités et le
préavis qui y sont décrits.

Vous pouvez fermer votre compte à tout moment. À la fermeture, nous arrêtons l’exécution,
révoquons les clés et supprimons vos données après le délai de grâce indiqué dans la
Politique de confidentialité.

**Fermer votre compte n’annule pas un abonnement payant.** Le paiement est géré par Paddle
en qualité de Merchant of Record (§1) ; un abonnement se résilie donc auprès de Paddle, et
non auprès de nous. Si vous fermez votre compte alors qu’une formule payante est en cours,
la période déjà payée va jusqu’à son terme, comme décrit au §4.3. Nous vous le disons
avant que vous confirmiez, et nous vous demandons d’en prendre acte.

## 8. Responsabilité

Rien ici ne limite une responsabilité qui ne peut être limitée par la loi, y compris la
responsabilité en cas de décès ou de dommage corporel causé par une négligence, en cas de
dol, ni les droits dont les consommateurs disposent en vertu de règles impératives.

Sous cette réserve : nous fournissons le service avec un soin et une compétence
raisonnables, mais nous ne sommes pas responsables des pertes indirectes ou
consécutives, de la perte de bénéfices ou d’activité, ni des conséquences du traitement
que vos tâches déclenchent sur vos propres systèmes. **Une requête planifiée n’est pas la
garantie que le traitement qui se trouve derrière a réussi**, et vous devriez concevoir
vos systèmes en partant de ce principe.

Au-delà de ces exceptions, **notre responsabilité est exclue dans toute la mesure permise
par le droit applicable**.

Nous préférons le dire clairement plutôt que de l’enfouir : Postqron est un ordonnanceur
facturé de zéro à quelques dizaines d’euros par mois, et il ne peut pas porter le risque
de ce qui dépend des tâches qu’il exécute. Si une exécution manquée ou dupliquée vous
causait un préjudice important, le service n’est pas le bon endroit où placer cette
dépendance, et aucune formulation ici ne change cette réalité technique.

## 9. Modifications des présentes conditions

Nous pouvons modifier ces conditions. Lorsqu’une modification affecte substantiellement
vos droits, nous vous en informons
30 jours
à l’avance. Si vous n’acceptez pas la modification, vous pouvez fermer votre compte avant
qu’elle prenne effet.

## 10. Droit applicable et juridiction

Les présentes conditions sont régies par
le droit italien.
Les litiges relèvent de la compétence exclusive
des tribunaux de Bergame, Italie,
**sauf** que, si vous êtes consommateur, vous conservez la protection des règles
impératives du pays où vous résidez et pouvez saisir vos tribunaux locaux.

---

**Contact :** hello@postqron.com
**Exploité par :** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
