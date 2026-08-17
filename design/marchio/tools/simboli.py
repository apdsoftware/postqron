#!/usr/bin/env python3
"""
La gamma della cresta.

La direzione è decisa: il pettine da solo, non il gallo. Qui si cerca il
disegno, lungo gli assi che il primo pettine sbagliava — era pieno, largo,
basso, con le punte tutte alla stessa altezza, e il risultato era inerte: da lì
la lettura «colline» o «corona».

Gli assi attraversati sono cinque, e ogni variante ne sposta uno o due:

- **peso** — massa piena, contorno, o punte staccate;
- **proporzioni** — largo e basso è la forma anatomica, alto e stretto sta
  meglio accanto a un logotipo;
- **graduazione** — tre punte di altezza diversa smettono di essere un profilo
  e diventano un ritmo. Né le colline né le corone sono graduate;
- **terminali** — punta acuta o raccordata cambiano il registro; raccordare
  troppo riporta alle colline;
- **simmetria** — un pettine simmetrico è un ornamento, uno sbilanciato è un
  segno.

Il gradiente non è un asse a parte: essendo ancorato alla griglia
(`gradientUnits="userSpaceOnUse"`) e non al riquadro del singolo oggetto,
attraversa il disegno da sinistra a destra. Le punte lo incontrano a tappe
diverse per costruzione, senza che nessuno debba assegnargliele.

Ogni simbolo vive su una griglia di 32 unità per lato. Sotto le 4 unità un
dettaglio vale meno di 2 px a 16 px di resa, e il rendering subpixel lo sfoca
via: è la soglia che decide cosa può esistere.

`ink` è il riquadro dell'inchiostro, misurato con `getBBox()` nel browser e
riportato qui: serve al lockup, che allinea il simbolo alle lettere sull'altezza
reale del disegno e non su quella nominale del riquadro.
"""
from __future__ import annotations

import math

# ------------------------------------------------------------------ primitive


def _p(x: float, y: float) -> str:
    return f"{x:.2f} {y:.2f}"


def _arco_di_freccia(da: tuple[float, float], a: tuple[float, float],
                     freccia: float) -> str:
    """
    Arco dichiarato per **freccia** — la sua altezza — invece che per raggio.

    È la misura che interessa a chi disegna: «questa pancia scende di tre
    unità». Oltre la metà della corda serve l'arco maggiore, perché un arco
    minore non supera il semicerchio per quanto si stringa il raggio.
    """
    mezza_corda = math.hypot(a[0] - da[0], a[1] - da[1]) / 2
    raggio = (freccia**2 + mezza_corda**2) / (2 * freccia)
    maggiore = 1 if freccia > mezza_corda else 0
    return f"A{raggio:.2f} {raggio:.2f} 0 {maggiore} 1 {_p(*a)}"


def _pettine(punte: tuple[tuple[float, float], ...], base: float,
             larghezza: float, cappello: float, valle: float,
             pancia: float = 0.0, inclinazione: float = 0.0) -> str:
    """
    Il pettine: punte affusolate unite da valli, su una base curva.

    `punte` è una sequenza di `(centro, altezza)`: i centri sono espliciti e non
    equidistanti per costruzione, perché la spaziatura è uno degli assi da
    muovere. `valle` è la frazione dell'altezza a cui scende l'incavo fra due
    punte — sotto il 20 % le punte si staccano e diventano fiamme, sopra il 60 %
    si richiudono e tornano gobbe.

    `inclinazione` sposta ogni punto a destra in proporzione a quanto è alto: è
    l'unico modo di far pendere il disegno in avanti senza una trasformazione,
    che il modulo TypeScript del sito non saprebbe portarsi dietro.
    """
    def x(px: float, py: float) -> float:
        return px + inclinazione * (base - py)

    def punto(px: float, py: float) -> str:
        return _p(x(px, py), py)

    sinistra = punte[0][0] - larghezza / 2
    destra = punte[-1][0] + larghezza / 2

    pezzi = [f"M{punto(sinistra, base)}"]
    for i, (cx, h) in enumerate(punte):
        cima = base - h
        pezzi.append(f"L{punto(cx - cappello, cima + cappello)}")
        # La punta è un semicerchio: raggio zero darebbe una spina, e sopra le
        # due unità si torna verso la gobba.
        pezzi.append(
            f"A{cappello:.2f} {cappello:.2f} 0 0 1 {punto(cx + cappello, cima + cappello)}"
        )
        if i + 1 < len(punte):
            prossimo = punte[i + 1]
            fondo = base - min(h, prossimo[1]) * valle
            pezzi.append(f"L{punto((cx + prossimo[0]) / 2, fondo)}")
    pezzi.append(f"L{punto(destra, base)}")
    if pancia:
        # Con la base dritta le punte leggevano **montagne**: è la linea di
        # terra a fare il paesaggio. Curvandola diventano un organo attaccato a
        # qualcosa, che è quello che una cresta è.
        pezzi.append(_arco_di_freccia((x(destra, base), base),
                                      (x(sinistra, base), base), pancia))
    pezzi.append("Z")
    return "".join(pezzi)


def _lama(cx: float, base: float, altezza: float, larghezza: float,
          cappello: float) -> str:
    """Una punta sola, staccata: affusolata dalla base alla cima arrotondata."""
    cima = base - altezza
    return (
        f"M{_p(cx - larghezza / 2, base)}"
        f"L{_p(cx - cappello, cima + cappello)}"
        f"A{cappello:.2f} {cappello:.2f} 0 0 1 {_p(cx + cappello, cima + cappello)}"
        f"L{_p(cx + larghezza / 2, base)}Z"
    )


# ------------------------------------------------------------ 1 · la graduata

#: Le tre altezze in salita: basso, medio, alto.
SALITA = (9.0, 13.5, 18.5)

#: Le stesse tre altezze, ma non ordinate — la più alta al centro.
#:
#: È la differenza che la gamma ha scoperto a schermo. Tre punte **in salita**
#: non leggono «cresta»: leggono **grafico di crescita**, e le lame staccate
#: leggono addirittura le barre del marchio Hexagon che stiamo sostituendo.
#: Bastano le stesse altezze in un ordine diverso perché il grafico sparisca e
#: resti il ritmo, che è quel che si cercava. È anche l'ordine anatomico: su un
#: gallo vero il pettine è più alto in mezzo.
CENTRATA = (11.0, 18.5, 14.0)


def _graduata() -> list[str]:
    """
    Punte in salita, alte e strette, terminali raccordati, base curva.

    È l'ipotesi centrale della gamma: tre altezze diverse smettono di essere un
    profilo e diventano un **ritmo**, che è letteralmente il prodotto. E tolgono
    le due letture sbagliate alla radice, perché né le colline né le corone
    sono graduate.
    """
    return [_pettine(
        punte=((9.6, SALITA[0]), (16.0, SALITA[1]), (22.4, SALITA[2])),
        base=26.0, larghezza=6.2, cappello=1.8, valle=0.30, pancia=2.6,
    )]


def _centrata() -> list[str]:
    """
    Le stesse tre altezze della graduata, con la più alta al centro.

    È l'unica differenza fra questa e la precedente, ed è quella che decide: in
    salita si legge un grafico, non ordinate si legge un ritmo. In più è
    l'ordine che un pettine ha davvero.
    """
    return [_pettine(
        punte=((9.6, CENTRATA[0]), (16.0, CENTRATA[1]), (22.4, CENTRATA[2])),
        base=26.0, larghezza=6.2, cappello=1.8, valle=0.30, pancia=2.6,
    )]


# -------------------------------------------------------------- 3 · l'acuta


def _acuta() -> list[str]:
    """
    Le punte quasi a spillo, con le valli che scendono fino alla base.

    È l'estremo del registro «misura»: geometria netta e molto spazio negativo.
    Il rischio è opposto a quello delle colline — troppo aguzza si legge
    «fiamma» o «corona di spine».
    """
    return [_pettine(
        punte=((9.6, CENTRATA[0]), (16.0, CENTRATA[1]), (22.4, CENTRATA[2])),
        base=26.0, larghezza=6.6, cappello=0.7, valle=0.14, pancia=1.6,
    )]


# --------------------------------------------------------------- 3 · le lame


def _lame() -> list[str]:
    """
    Le tre punte staccate: il pettine diventa una sequenza, non una sagoma.

    È la variante in cui il gradiente fa da struttura senza che glielo si
    chieda: attraversando la griglia da sinistra a destra, ogni lama incontra
    una tappa diversa fra le due fermate, e il colore racconta la successione.
    """
    return [
        _lama(9.0, 26.0, SALITA[0], 5.0, 1.7),
        _lama(16.0, 26.0, SALITA[1], 5.0, 1.7),
        _lama(23.0, 26.0, SALITA[2], 5.0, 1.7),
    ]


# ------------------------------------------------------------ 4 · il contorno


def _contorno() -> list[str]:
    """
    La graduata svuotata: resta la linea, il pieno se ne va.

    È il grado più leggero della gamma, e quello che a 16 px ha più da perdere:
    le valli sono larghe due unità e mezza, cioè poco più di un pixel.
    """
    return [_pettine(
        punte=((9.6, CENTRATA[0]), (16.0, CENTRATA[1]), (22.4, CENTRATA[2])),
        base=25.4, larghezza=6.6, cappello=1.4, valle=0.30, pancia=2.4,
    )]


# ------------------------------------------------------------- 5 · la sghemba


def _sghemba() -> list[str]:
    """
    La graduata che pende in avanti, con le punte non equidistanti.

    Una cresta simmetrica è un ornamento; una sbilanciata è un segno. Qui la
    spinta viene da due cose insieme: l'inclinazione, che sposta ogni punto a
    destra in proporzione all'altezza, e la spaziatura che si allarga salendo.
    """
    return [_pettine(
        punte=((9.4, 11.0), (15.2, 18.5), (22.8, 14.0)),
        base=26.0, larghezza=6.0, cappello=1.7, valle=0.28, pancia=2.4,
        inclinazione=0.14,
    )]


# --------------------------------------------------------------- 6 · la bassa


def _bassa() -> list[str]:
    """
    La proporzione anatomica — larga e bassa — ma con le punte graduate.

    Sta nella gamma per isolare un asse solo: serve a vedere se il difetto del
    primo pettine fosse la proporzione o la graduazione. Se questa continua a
    leggersi «colline», allora era la graduazione, e le altre cinque hanno
    ragione a essere alte.
    """
    return [_pettine(
        punte=((7.6, 8.5), (16.0, 13.5), (24.4, 10.5)),
        base=24.5, larghezza=8.4, cappello=2.4, valle=0.34, pancia=3.4,
    )]


# ---------------------------------------------------------------- il catalogo
#
# `stroke` diverso da zero significa tracciato aperto: il disegno è la linea,
# non il pieno. `ink` è il riquadro misurato, non quello nominale.

CANDIDATI: dict[str, dict] = {
    "graduata": {
        "nome": "Graduata",
        "famiglia": "punte in salita",
        "sottotitolo": "tre altezze diverse: un ritmo, non un profilo",
        "tracciati": _graduata(),
        "stroke": 0.0,
        "ink": (6.5, 7.5, 25.5, 28.6),
    },
    "centrata": {
        "nome": "Centrata",
        "famiglia": "ordine delle punte",
        "sottotitolo": "le stesse altezze, con la più alta in mezzo",
        "tracciati": _centrata(),
        "stroke": 0.0,
        "ink": (6.5, 7.5, 25.5, 28.6),
    },
    "acuta": {
        "nome": "Acuta",
        "famiglia": "terminali a spillo",
        "sottotitolo": "punte a spillo e valli fino alla base",
        "tracciati": _acuta(),
        "stroke": 0.0,
        "ink": (6.3, 7.5, 25.7, 27.6),
    },
    "lame": {
        "nome": "Lame",
        "famiglia": "punte staccate",
        "sottotitolo": "il gradiente diventa la successione delle punte",
        "tracciati": _lame(),
        "stroke": 0.0,
        "ink": (6.5, 7.5, 25.5, 26.0),
    },
    "contorno": {
        "nome": "Contorno",
        "famiglia": "peso: solo linea",
        "sottotitolo": "la graduata svuotata, tutta spazio negativo",
        "tracciati": _contorno(),
        "stroke": 2.6,
        "ink": (5.0, 5.6, 27.0, 29.1),
    },
    "sghemba": {
        "nome": "Sghemba",
        "famiglia": "asimmetria",
        "sottotitolo": "pende in avanti, e le punte non sono equidistanti",
        "tracciati": _sghemba(),
        "stroke": 0.0,
        "ink": (6.0, 7.5, 27.0, 28.4),
    },
    "bassa": {
        "nome": "Bassa",
        "famiglia": "proporzione anatomica",
        "sottotitolo": "larga e bassa, ma graduata: il controllo della gamma",
        "tracciati": _bassa(),
        "stroke": 0.0,
        "ink": (3.4, 11.0, 28.6, 27.9),
    },
}
