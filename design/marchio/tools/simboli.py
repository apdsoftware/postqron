#!/usr/bin/env python3
"""
La gamma di riduzione del gallo, dal figurativo al segno.

Il concetto è confermato — il gallo è l'animale che canta a ora fissa, e in
questa categoria non lo ha nessuno (`PANORAMA.md`). Qui si cerca il **grado di
figurazione** giusto, che è una scala e non un elenco di alternative.

L'ipotesi che governa la scala: i due difetti della prima testa — l'occhio che
si chiude sotto i 20 px e il registro troppo simpatico per un prodotto che
chiede una partita IVA — **sono lo stesso difetto**. Nascono entrambi dal grado
di figurazione: un animale disegnato ha bisogno di occhi e becchi, e sono
proprio quelli a collassare alle misure piccole e a spostare il tono verso il
giocattolo. Scendendo lungo la scala i due dovrebbero cadere insieme.

Ogni simbolo vive su una griglia di 32 unità per lato. Le forme possono essere
più di una: il gradiente è ancorato alla griglia
(`gradientUnits="userSpaceOnUse"`) e non al riquadro del singolo oggetto, quindi
tracciati sovrapposti condividono una sfumatura sola. Le controforme restano
sottotracciati in `evenodd` del tracciato che le contiene.

Sotto le 4 unità di griglia un dettaglio vale meno di 2 px a 16 px di resa: è la
soglia che decide cosa può esistere e cosa no.

`ink` è il riquadro dell'inchiostro, misurato con `getBBox()` nel browser e
riportato qui: serve al lockup, che allinea il simbolo alle lettere sull'altezza
reale del disegno e non su quella nominale del riquadro.
"""
from __future__ import annotations

import math

# ------------------------------------------------------------------ primitive


def _p(x: float, y: float) -> str:
    return f"{x:.2f} {y:.2f}"


def _cerchio(cx: float, cy: float, r: float, orario: bool = True) -> str:
    """Cerchio come sottotracciato chiuso, in due archi."""
    verso = 1 if orario else 0
    return (
        f"M{_p(cx - r, cy)}"
        f"A{r:.2f} {r:.2f} 0 0 {verso} {_p(cx + r, cy)}"
        f"A{r:.2f} {r:.2f} 0 0 {verso} {_p(cx - r, cy)}Z"
    )


def _sul_cerchio(cx: float, cy: float, r: float, gradi: float) -> tuple[float, float]:
    a = math.radians(gradi)
    return cx + r * math.cos(a), cy + r * math.sin(a)


def _arco_di_freccia(da: tuple[float, float], a: tuple[float, float],
                     freccia: float, verso: int = 1) -> str:
    """
    Arco dichiarato per **freccia** — la sua altezza — invece che per raggio.

    È la misura che interessa a chi disegna: «questa gobba sporge di tre unità».
    Oltre la metà della corda serve l'arco maggiore, perché un arco minore non
    supera il semicerchio per quanto si stringa il raggio: i primi tentativi di
    cresta erano tre gobbe da mezzo millimetro proprio per questo.
    """
    mezza_corda = math.hypot(a[0] - da[0], a[1] - da[1]) / 2
    raggio = (freccia**2 + mezza_corda**2) / (2 * freccia)
    maggiore = 1 if freccia > mezza_corda else 0
    return f"A{raggio:.2f} {raggio:.2f} 0 {maggiore} {verso} {_p(*a)}"


def _pettine(x0: float, x1: float, base: float, altezze: tuple[float, ...],
             cappello: float = 2.5, valle: float = 0.42, pancia: float = 0.0) -> str:
    """
    La cresta: punte affusolate su una base carnosa.

    Il primo disegno erano tre gobbe semicircolari contigue, e leggeva
    **nuvola** — che in un vicinato di infrastruttura è la lettura peggiore
    possibile. La differenza fra una nuvola e una cresta non è il numero dei
    lobi: sono le **valli**. Una nuvola ha gobbe che si toccano in alto; una
    cresta ha punte che scendono fra loro fin quasi alla base.

    - `altezze` diverse fra loro: punte tutte uguali fanno una corona.
    - `cappello` è il raggio della punta arrotondata; a zero verrebbe una
      corona di spine, sopra le tre unità si torna verso la nuvola.
    - `valle` è la frazione dell'altezza a cui scende l'incavo: sotto il 30 % le
      punte si staccano e sembrano fiamme, sopra il 60 % si richiudono.
    """
    n = len(altezze)
    passo = (x1 - x0) / n
    centri = [x0 + passo * (i + 0.5) for i in range(n)]

    pezzi = [f"M{_p(x0, base)}"]
    for i, (cx, h) in enumerate(zip(centri, altezze)):
        cima = base - h
        pezzi.append(f"L{_p(cx - cappello, cima + cappello)}")
        pezzi.append(f"A{cappello:.2f} {cappello:.2f} 0 0 1 "
                     f"{_p(cx + cappello, cima + cappello)}")
        if i + 1 < n:
            # L'incavo scende a una frazione della punta più bassa fra le due.
            fondo = base - min(h, altezze[i + 1]) * valle
            pezzi.append(f"L{_p((cx + centri[i + 1]) / 2, fondo)}")
    pezzi.append(f"L{_p(x1, base)}")
    if pancia:
        # Il bordo inferiore torna indietro gonfiando verso il basso. Con una
        # base dritta le tre punte leggevano **montagne**: è la linea di terra
        # a fare il paesaggio. Curvandola diventano un organo attaccato a
        # qualcosa, che è quello che una cresta è.
        pezzi.append(_arco_di_freccia((x1, base), (x0, base), pancia))
    pezzi.append("Z")
    return "".join(pezzi)


# --------------------------------------------------------- G1 · testa, pulita


def _testa() -> list[str]:
    """
    Il grado più figurativo: testa di profilo con cresta, occhio, becco,
    bargiglio. È il punto di partenza della scala, non la sua conclusione.
    """
    cx, cy, r = 14.6, 19.2, 7.9

    def punto(g: float) -> tuple[float, float]:
        return _sul_cerchio(cx, cy, r, g)

    def arco(fino: float) -> str:
        return f"A{r:.2f} {r:.2f} 0 0 1 {_p(*punto(fino))}"

    contorno = [f"M{_p(*punto(160))}", arco(230)]
    for da, a in ((234, 260), (260, 286), (286, 312)):
        contorno.append(_arco_di_freccia(punto(da), punto(a), 4.3))
    contorno.append(arco(342))
    contorno.append(f"L{_p(cx + r + 5.4, cy + 1.0)}")
    contorno.append(f"L{_p(*punto(22))}")
    contorno.append(arco(36))
    contorno.append(_arco_di_freccia(punto(36), punto(76), 3.0))
    contorno.append(arco(160))
    contorno.append("Z")

    return ["".join(contorno) + _cerchio(17.4, 16.6, 2.3, orario=False)]


# ------------------------------------------------- G2 · profilo intero, pieno


def _profilo() -> list[str]:
    """
    Il gallo intero di profilo, in silhouette piena.

    È il registro della **banderuola**: il gallo segnavento sta sui tetti da
    cinque secoli e non è mai stato un personaggio. Nessun occhio da chiudere —
    la silhouette non ne ha bisogno — e nessuna faccia a cui affezionarsi.

    Il corpo è una goccia; la coda tre falci; il collo sale in diagonale perché
    è quella la posa del canto.
    """
    # Corpo, collo e testa sono forme distinte che si uniscono al disegno: il
    # gradiente è ancorato alla griglia, quindi restano una sfumatura sola.
    # Tracciarne il contorno unico avrebbe voluto dire calcolare tre
    # intersezioni per ottenere esattamente la stessa figura.
    corpo = "M13.6 12.8A8.6 7.6 0 0 1 13.6 28.0A8.6 7.6 0 0 1 13.6 12.8Z"
    collo = "M15.8 19.6L19.6 8.6L24.0 10.4L21.2 21.0Z"
    testa = _cerchio(23.0, 9.0, 4.0)
    cresta = _pettine(19.4, 26.6, 6.6, (3.0, 4.0, 3.2), cappello=1.0, valle=0.4,
                      pancia=1.6)
    becco = f"M{_p(26.2, 7.4)}L{_p(31.0, 9.4)}L{_p(26.2, 11.4)}Z"
    # Bargiglio: la goccia sotto il becco. Sotto le 2,5 unità sparirebbe, quindi
    # è grande abbastanza da esistere o non ci sarebbe affatto.
    bargiglio = _cerchio(24.4, 13.6, 2.4)
    # Coda: tre falci che si aprono a ventaglio all'indietro. Una sola sembra
    # una foglia, cinque diventano una spazzola.
    coda = (
        "M9.0 17.0C4.4 13.6 2.6 8.6 2.4 3.2C6.0 6.4 9.2 10.4 11.6 15.0Z"
        "M10.6 15.4C8.2 10.6 8.0 5.8 9.4 1.2C12.0 5.4 13.4 10.0 13.8 14.6Z"
        "M12.8 15.0C13.4 10.4 15.4 6.6 18.6 3.6C18.6 8.2 17.6 12.2 15.8 15.6Z"
    )
    zampe = "M12.0 26.6H14.0V30.4H12.0ZM16.6 26.2H18.6V30.4H16.6Z"
    return [coda, corpo, collo, zampe, testa + becco + bargiglio, cresta]


# ---------------------------------------------------- G3 · testa in un tratto


def _tratto() -> list[str]:
    """
    La stessa testa, ridotta a una linea continua aperta.

    Il tratto è il registro adulto della figurazione: dice «disegno», non
    «personaggio». E toglie il problema alla radice, perché non c'è nessun
    occhio da rimpicciolire — la faccia resta il vuoto dentro la linea.
    """
    cx, cy, r = 14.6, 19.6, 8.8

    def punto(g: float) -> tuple[float, float]:
        return _sul_cerchio(cx, cy, r, g)

    def arco(fino: float) -> str:
        return f"A{r:.2f} {r:.2f} 0 0 1 {_p(*punto(fino))}"

    pezzi = [f"M{_p(*punto(120))}", arco(228)]
    for da, a in ((232, 262), (262, 292), (292, 322)):
        pezzi.append(_arco_di_freccia(punto(da), punto(a), 4.2))
    pezzi.append(arco(346))
    pezzi.append(f"L{_p(cx + r + 5.0, cy + 0.8)}")
    pezzi.append(f"L{_p(*punto(26))}")
    pezzi.append(arco(88))
    return ["".join(pezzi)]


# ------------------------------------------------ G4 · costruzione geometrica


def _geometrico() -> list[str]:
    """
    Lo stesso gallo, ma costruito come è costruito il logotipo.

    Quicksand è una geometrica: cerchi perfetti e aste diritte. Qui il gallo usa
    lo stesso alfabeto di forme — una testa che è un cerchio, tre cerchi di
    cresta, un triangolo di becco — così simbolo e lettere condividono la
    costruzione invece di limitarsi a stare accanto.

    Nessuna curva è disegnata a mano: sono tutti archi di cerchio su un reticolo.
    """
    testa = _cerchio(14.0, 19.0, 8.0) + _cerchio(17.4, 16.4, 2.6, orario=False)
    cresta = "".join(
        _cerchio(x, y, 3.0) for x, y in ((9.4, 10.6), (14.0, 8.6), (18.4, 10.4))
    )
    becco = f"M{_p(19.6, 15.4)}L{_p(29.0, 19.4)}L{_p(19.6, 23.4)}Z"
    return [testa, cresta, becco]


# ----------------------------------------------------- G5 · cresta più becco


def _cresta_becco() -> list[str]:
    """
    La testa sparisce e restano i due segni che la dicono: il pettine e il becco.

    È il punto della scala in cui il gallo smette di essere disegnato e comincia
    a essere sottinteso. Chi conosce il nome del prodotto lo vede; chi non lo
    conosce vede un segno, che è comunque meglio di un animale male stampato.
    """
    return [
        _pettine(3.0, 20.5, 19.5, (9.0, 12.0, 10.0), cappello=2.2, pancia=4.4),
        f"M{_p(19.0, 15.8)}L{_p(29.5, 20.2)}L{_p(19.0, 24.6)}Z",
    ]


# --------------------------------------------------------------- G6 · cresta


def _solo_cresta() -> list[str]:
    """
    Il grado massimo di riduzione: le tre gobbe del pettine, e nient'altro.

    Tre punte disuguali su una base sono un segno geometrico che a 16 px non ha
    niente da perdere: non ci sono controforme che si chiudano né dettagli che
    si sfochino. Resta gallo per chiunque abbia sentito il nome una volta — e
    resta un segno, non una figura, per tutti gli altri.
    """
    return [_pettine(3.0, 29.0, 21.5, (11.0, 15.0, 12.5), pancia=5.5)]


# ---------------------------------------------------------------- il catalogo
#
# `stroke` diverso da zero significa tracciato aperto: il disegno è la linea,
# non il pieno. `ink` è il riquadro misurato, non quello nominale.

CANDIDATI: dict[str, dict] = {
    "testa": {
        "nome": "Testa",
        "famiglia": "figurativo",
        "sottotitolo": "il gallo disegnato: cresta, occhio, becco, bargiglio",
        "tracciati": _testa(),
        "stroke": 0.0,
        "ink": (6.7, 7.2, 27.9, 28.3),
    },
    "profilo": {
        "nome": "Profilo",
        "famiglia": "figurativo",
        "sottotitolo": "la banderuola: il gallo intero, in silhouette",
        "tracciati": _profilo(),
        "stroke": 0.0,
        "ink": (4.8, 1.8, 27.8, 30.6),
    },
    "tratto": {
        "nome": "Tratto",
        "famiglia": "lineare",
        "sottotitolo": "la stessa testa, in una linea continua sola",
        "tracciati": _tratto(),
        "stroke": 3.4,
        "ink": (5.6, 6.3, 30.4, 28.8),
    },
    "geometrico": {
        "nome": "Geometrico",
        "famiglia": "costruito",
        "sottotitolo": "cerchi e triangoli, l'alfabeto di forme del logotipo",
        "tracciati": _geometrico(),
        "stroke": 0.0,
        "ink": (6.0, 5.6, 29.0, 27.0),
    },
    "cresta-becco": {
        "nome": "Cresta e becco",
        "famiglia": "segno",
        "sottotitolo": "la testa sottintesa, ridotta ai due segni che la dicono",
        "tracciati": _cresta_becco(),
        "stroke": 0.0,
        "ink": (3.9, 13.0, 29.5, 26.0),
    },
    "cresta": {
        "nome": "Cresta",
        "famiglia": "segno",
        "sottotitolo": "le tre gobbe del pettine, e nient'altro",
        "tracciati": _solo_cresta(),
        "stroke": 0.0,
        "ink": (2.4, 11.5, 30.2, 24.0),
    },
}
