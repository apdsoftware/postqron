#!/usr/bin/env python3
"""
I simboli candidati, costruiti geometricamente.

Ogni simbolo vive su una griglia di 32 unità per lato ed è **un solo
tracciato**, o due, con le controforme come sottotracciati in `evenodd`. Non è
un vezzo: forme sovrapposte come elementi separati prendono ognuna il proprio
riquadro di gradiente, e il marchio diventa un collage di sfumature invece di
una sfumatura sola.

Il vincolo che governa tutto è la resa a 16 px: sulla griglia da 32 un
dettaglio sotto le 4 unità vale meno di 2 px, e il rendering subpixel lo sfoca
via. È il motivo per cui qui non ci sono capelli, dentini o pallini.

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


def _rettangolo(x0: float, y0: float, x1: float, y1: float, r: float) -> str:
    return (
        f"M{_p(x0 + r, y0)}H{x1 - r:.2f}A{r:.2f} {r:.2f} 0 0 1 {_p(x1, y0 + r)}"
        f"V{y1 - r:.2f}A{r:.2f} {r:.2f} 0 0 1 {_p(x1 - r, y1)}H{x0 + r:.2f}"
        f"A{r:.2f} {r:.2f} 0 0 1 {_p(x0, y1 - r)}V{y0 + r:.2f}"
        f"A{r:.2f} {r:.2f} 0 0 1 {_p(x0 + r, y0)}Z"
    )


# ---------------------------------------------------------------- 1. il gallo


def _gallo() -> list[str]:
    """
    Testa di gallo di profilo, verso destra: un tracciato solo più l'occhio.

    Il contorno si costruisce camminando lungo il cerchio della testa e
    sostituendo tre archi con altrettanti elementi — cresta, becco, bargiglio.
    Disegnare l'unione a mano avrebbe voluto dire calcolare le intersezioni fra
    cerchi; percorrerla no.
    """
    cx, cy, r = 14.6, 19.2, 7.9

    def punto(g: float) -> str:
        return _p(*_sul_cerchio(cx, cy, r, g))

    def arco(fino: float) -> str:
        return f"A{r:.2f} {r:.2f} 0 0 1 {punto(fino)}"

    def bernoccolo(da: float, a: float, sporgenza: float) -> str:
        """
        Arco che gonfia verso l'esterno di `sporgenza` fra due angoli.

        La sporgenza è la freccia dell'arco, non il suo raggio: è la misura che
        interessa a chi disegna. Oltre la metà della corda serve l'arco maggiore
        — un arco minore, per quanto si stringa il raggio, non supera il
        semicerchio, e i primi tentativi di cresta erano tre gobbe da mezzo
        millimetro.
        """
        x0, y0 = _sul_cerchio(cx, cy, r, da)
        x1, y1 = _sul_cerchio(cx, cy, r, a)
        mezza_corda = math.hypot(x1 - x0, y1 - y0) / 2
        raggio = (sporgenza**2 + mezza_corda**2) / (2 * sporgenza)
        maggiore = 1 if sporgenza > mezza_corda else 0
        return f"A{raggio:.2f} {raggio:.2f} 0 {maggiore} 1 {_p(x1, y1)}"

    # In coordinate SVG la y cresce verso il basso: 270° è la sommità.
    contorno = [f"M{punto(160)}"]
    contorno.append(arco(230))
    # Cresta: tre gobbe sulla sommità, spostate in avanti come nel gallo vero.
    for da, a in ((234, 260), (260, 286), (286, 312)):
        contorno.append(bernoccolo(da, a, 4.3))
    contorno.append(arco(342))
    # Becco: un cuneo stretto, non uno smusso della testa. È l'angolo che decide
    # se si legge «gallo» o «uccellino»: sopra i venti gradi la testa si allunga
    # e basta.
    contorno.append(f"L{_p(cx + r + 5.4, cy + 1.0)}")
    contorno.append(f"L{punto(22)}")
    contorno.append(arco(36))
    # Bargiglio: la gobba sotto il becco, che chiude la lettura «gallo».
    contorno.append(bernoccolo(36, 76, 3.0))
    contorno.append(arco(160))
    contorno.append("Z")

    occhio = _cerchio(17.4, 16.6, 2.3, orario=False)
    return ["".join(contorno) + occhio]


# --------------------------------------------------------------- 2. il timbro


def _timbro() -> list[str]:
    """
    Timbro postale: anello e barre di annullo.

    L'annullo è la sola immagine che dica «è passato di qui a quest'ora» senza
    disegnare un quadrante — e «Post» è la prima metà del nome.
    """
    anello = _cerchio(16, 16, 14.0) + _cerchio(16, 16, 9.4, orario=False)
    barra_alta = _rettangolo(0.5, 12.3, 31.5, 16.3, 2.0)
    barra_bassa = _rettangolo(0.5, 18.0, 31.5, 22.0, 2.0)
    return [anello, barra_alta + barra_bassa]


# ---------------------------------------------------------- 3. il francobollo


def _francobollo(lato: float = 25.0, denti: int = 4, bocca: float = 3.6,
                 profondita: float = 1.6) -> list[str]:
    """
    Francobollo: il quadrato con la dentellatura.

    La dentellatura è l'unico contorno al mondo che significhi «affrancatura», e
    «Post» nel nome viene sia dalla posta sia dal verbo HTTP.

    Quattro tacche per lato e non dodici, e larghe più del doppio di quanto sono
    fonde: con tacche semicircolari — il primo tentativo — il quadrato usciva
    frastagliato e si leggeva «sole» o «esplosione», non «francobollo». Resta
    che a 16 px una tacca fonda un pixel è al limite del visibile: è il prezzo
    dichiarato di questa direzione.
    """
    x0 = y0 = (32 - lato) / 2
    x1 = y1 = x0 + lato
    passo = lato / denti
    mezzo = (passo - bocca) / 2
    raggio = (profondita**2 + (bocca / 2) ** 2) / (2 * profondita)

    def bordo(x: float, y: float, dx: float, dy: float) -> list[str]:
        pezzi = []
        for _ in range(denti):
            x, y = x + dx * mezzo, y + dy * mezzo
            pezzi.append(f"L{_p(x, y)}")
            x, y = x + dx * bocca, y + dy * bocca
            pezzi.append(f"A{raggio:.2f} {raggio:.2f} 0 0 0 {_p(x, y)}")
            x, y = x + dx * mezzo, y + dy * mezzo
            pezzi.append(f"L{_p(x, y)}")
        return pezzi

    contorno = [f"M{_p(x0, y0)}"]
    contorno += bordo(x0, y0, 1, 0)
    contorno += bordo(x1, y0, 0, 1)
    contorno += bordo(x1, y1, -1, 0)
    contorno += bordo(x0, y1, 0, -1)
    contorno.append("Z")
    return ["".join(contorno)]


# ----------------------------------------------------------- 4. l'onda quadra


def _onda() -> list[str]:
    """
    Onda quadra: il segnale periodico dell'elettronica digitale.

    Dice regolarità senza disegnare un quadrante, ed è l'unica forma di quel
    repertorio che il vicinato non abbia già preso.
    """
    return [f"M{_p(5, 22)}H11V10H19V22H27"]


# --------------------------------------------------------------- 5. la P sola


def _pi() -> list[str]:
    """Monogramma: la P come asta e occhiello circolare."""
    asta = _rettangolo(6, 3.5, 11, 28.5, 2.5)
    occhiello = _cerchio(15.2, 11.5, 8.2) + _cerchio(15.2, 11.5, 3.7, orario=False)
    return [asta + occhiello]


# ---------------------------------------------------------------- il catalogo
#
# `stroke` diverso da zero significa tracciato aperto: il disegno è la linea,
# non il pieno. `ink` è il riquadro misurato, non quello nominale.

CANDIDATI: dict[str, dict] = {
    "gallo": {
        "nome": "Cresta",
        "famiglia": "mascotte",
        "sottotitolo": "il gallo, l'animale che canta a ora fissa",
        "tracciati": _gallo(),
        "stroke": 0.0,
        "ink": (5.3, 5.9, 29.0, 27.5),
    },
    "timbro": {
        "nome": "Timbro",
        "famiglia": "oggetto",
        "sottotitolo": "l'annullo postale: la prova che è passato di qui",
        "tracciati": _timbro(),
        "stroke": 0.0,
        "ink": (0.5, 2.0, 31.5, 30.0),
    },
    "francobollo": {
        "nome": "Francobollo",
        "famiglia": "oggetto",
        "sottotitolo": "la dentellatura, e «Post» che è posta e verbo HTTP",
        "tracciati": _francobollo(),
        "stroke": 0.0,
        "ink": (3.5, 3.5, 28.5, 28.5),
    },
    "onda": {
        "nome": "Onda quadra",
        "famiglia": "forma astratta",
        "sottotitolo": "il segnale periodico, senza quadranti",
        "tracciati": _onda(),
        "stroke": 5.0,
        "ink": (2.5, 7.5, 29.5, 24.5),
    },
    "pi": {
        "nome": "P",
        "famiglia": "monogramma",
        "sottotitolo": "l'iniziale, e nient'altro",
        "tracciati": _pi(),
        "stroke": 0.0,
        "ink": (6.0, 3.3, 23.4, 28.5),
    },
    "logotipo": {
        "nome": "Solo logotipo",
        "famiglia": "senza simbolo",
        "sottotitolo": "il nome disegnato, e basta",
        # Nessun simbolo nel lockup. Per la favicon, dove un logotipo non entra,
        # si stacca la P dalle stesse lettere disegnate.
        "tracciati": [],
        "stroke": 0.0,
        "ink": (0.0, 0.0, 32.0, 32.0),
        "solo_logotipo": True,
    },
}
