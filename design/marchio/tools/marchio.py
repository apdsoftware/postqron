#!/usr/bin/env python3
"""
Costruzione del marchio: simboli, logotipo, lockup.

Un solo posto in cui vivono le geometrie. Da qui escono sia la pagina di prova
(`proposte.py`) sia gli asset definitivi (`esporta.py`): se un tracciato cambia,
cambia ovunque, e non esiste la copia dimenticata che diverge in silenzio.
"""
from __future__ import annotations

from wordmark import wordmark

# ------------------------------------------------------------------- palette
CIANO = "#0fb4e5"
VIOLA = "#743fe5"
PROFONDO = "#1e3056"

CAP = 700  # altezza delle maiuscole di Quicksand, in unità di em (upem 1000)
DESC = 200  # profondità della discendente della q


def gradiente(uid: str) -> str:
    return (
        f'<linearGradient id="{uid}" x1="0" y1="1" x2="1" y2="0">'
        f'<stop offset="0" stop-color="{CIANO}"/>'
        f'<stop offset="1" stop-color="{VIOLA}"/></linearGradient>'
    )


# ------------------------------------------------------------------- simboli
#
# Tre direzioni, tutte su griglia 32×32 con lo stesso spessore di tratto (5
# unità = 2,5 px a 16 px di resa). Lo spessore non è arbitrario: sotto le 2 px
# il rendering subpixel lo sfoca e il marchio diventa una macchia grigia.

def blocco_tracciati(rx: float = 7.5, rxc: float = 4.5) -> list[str]:
    """
    I due tracciati del simbolo, già traslati nel riquadro 32×32.

    Occhiello: rettangolo di 20×20 unità, parete di 4, controforma di 12×12.
    Asta: 4 unità di larghezza, scende fino a 3 unità dal bordo inferiore.

    Il raggio è l'unico parametro davvero in discussione. Troppo piccolo e il
    simbolo innestato nel logotipo si legge come il quadratino del glifo
    mancante; troppo grande e smette di dire «blocco» e torna a dire «cerchio».
    A 7,5 i lati restano dritti e gli angoli non sono più spigoli: è il punto in
    cui il simbolo è insieme una lettera e un campo di testo.
    """
    def rettangolo(x0: float, y0: float, x1: float, y1: float, r: float) -> str:
        x0, y0, x1, y1 = x0 + 3, y0 - 1, x1 + 3, y1 - 1
        return (
            f"M{x0 + r:g} {y0:g}H{x1 - r:g}A{r:g} {r:g} 0 0 1 {x1:g} {y0 + r:g}"
            f"V{y1 - r:g}A{r:g} {r:g} 0 0 1 {x1 - r:g} {y1:g}H{x0 + r:g}"
            f"A{r:g} {r:g} 0 0 1 {x0:g} {y1 - r:g}V{y0 + r:g}"
            f"A{r:g} {r:g} 0 0 1 {x0 + r:g} {y0:g}Z"
        )

    return [
        rettangolo(3, 4, 23, 24, rx) + rettangolo(7, 8, 19, 20, rxc),
        "M22 3h4v24a2 2 0 0 1-4 0Z",
    ]


def _blocco(rx: float = 7.5, rxc: float = 4.5) -> str:
    occhiello, asta = blocco_tracciati(rx, rxc)
    return (
        f'<path fill="{{paint}}" fill-rule="evenodd" d="{occhiello}"/>'
        f'<path fill="{{paint}}" d="{asta}"/>'
    )


SIMBOLI: dict[str, dict] = {
    "blocco": {
        "nome": "Blocco",
        "sottotitolo": "la q come blocco di configurazione",
        # Occhiello quadrato con controforma quadrata: un campo, non un
        # quadrante. L'asta scende sotto la riga — è ciò che la q fa nel nome.
        "ink": (6, 3, 26, 29),
        # Parete di 4 unità: innestata nel logotipo vale 108 unità di em contro
        # le 100 dell'asta delle lettere. Con 5 la q pesava visibilmente più del
        # resto della parola; con 4 la controforma cresce anche a 16 px.
        "markup": _blocco(7.5, 4.5),
    },
    "indent": {
        "nome": "Indent",
        "sottotitolo": "la rotaia e le righe rientrate",
        "ink": (3, 4, 29, 28),
        "markup": """<g fill="{paint}">
  <rect x="3" y="4" width="5" height="24" rx="2.5"/>
  <rect x="12" y="4" width="17" height="5" rx="2.5"/>
  <rect x="16" y="13.5" width="13" height="5" rx="2.5"/>
  <rect x="16" y="23" width="8" height="5" rx="2.5"/>
</g>""",
    },
    "gradino": {
        "nome": "Gradino",
        "sottotitolo": "l'indentazione in un tratto solo",
        "ink": (3.5, 3.5, 28.5, 28.5),
        "markup": """<path d="M6 6h7v10h7v10h6" fill="none" stroke="{paint}"
      stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/>""",
    },
}


def simbolo_svg(chiave: str, paint: str, uid: str = "") -> str:
    """Contenuto SVG del simbolo, senza il tag <svg> né i <defs>."""
    return SIMBOLI[chiave]["markup"].format(paint=paint)


def simbolo(chiave: str, paint: str, uid: str, extra: str = "") -> str:
    defs = f"<defs>{gradiente(uid)}</defs>" if paint.startswith("url") else ""
    return (
        f'<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg" {extra}>'
        f"{defs}{simbolo_svg(chiave, paint)}</svg>"
    )


# ------------------------------------------------------------------ logotipo
#
# Il logotipo non è testo composto: è disegnato. I tracciati vengono da
# Quicksand (SIL OFL, già nel repository e già servito dal nostro dominio) e poi
# corretti a mano — crenatura propria e discendente della q prolungata.


def logotipo(peso: float = 600, tracking: float = -15, coda: bool = True,
             testo: str = "Postqron") -> dict:
    """Tracciati del logotipo, in unità di em con la linea di base a y = 0."""
    dati = wordmark(testo, peso, tracking)
    glifi = {i: g for i, g in enumerate(dati["glifi"])}

    fine = dati["advance"]
    coda_d = ""
    spessore = _spessore_asta(peso)
    asta_destra = fondo = 0.0
    if coda:
        q = glifi[4]
        # Bordo destro e fondo dell'asta della q, in coordinate assolute. Il
        # bbox è in coordinate font (y verso l'alto): −200 diventa +200 in giù.
        asta_destra = q["x"] + q["bbox"][2]
        fondo = -q["bbox"][1]
        # La coda parte dal fondo dell'asta della q e corre fino al bordo destro
        # del logotipo: sottolinea «ron», cioè la seconda metà del nome.
        y0 = fondo - spessore
        coda_d = (
            f"M{asta_destra - spessore:.0f} {y0:.0f}"
            f"H{fine - spessore / 2:.0f}"
            f"a{spessore / 2:.0f} {spessore / 2:.0f} 0 0 1 0 {spessore:.0f}"
            f"H{asta_destra - spessore:.0f}Z"
        )

    return {
        "d": dati["d"],
        "lsb": glifi[0]["bbox"][0],
        "coda": coda_d,
        "advance": fine,
        "asta_q": asta_destra,
        "fondo_q": fondo,
        "spessore": spessore,
    }


def _spessore_asta(peso: float) -> float:
    """Spessore dell'asta verticale di Quicksand al peso dato, in unità di em."""
    from fontTools.ttLib import TTFont
    from fontTools.varLib import instancer
    from fontTools.pens.boundsPen import BoundsPen
    from wordmark import FONT

    font = instancer.instantiateVariableFont(TTFont(FONT), {"wght": peso}, inplace=True)
    glyphset = font.getGlyphSet()
    # L'asta della «l» è una verticale isolata: la sua larghezza è lo spessore.
    pen = BoundsPen(glyphset)
    glyphset[font.getBestCmap()[ord("l")]].draw(pen)
    return round(pen.bounds[2] - pen.bounds[0], 1)


def logotipo_innestato(peso: float = 600, tracking: float = -15) -> dict:
    """
    Logotipo in cui il simbolo prende il posto della q.

    La q è l'unica lettera del nome con una discendente, ed è la lettera che il
    simbolo disegna: innestarlo nel logotipo fa dei due un oggetto solo, invece
    di due oggetti accostati che ripetono la stessa forma a mezzo centimetro di
    distanza.
    """
    dati = wordmark("Postqron", peso, tracking)
    q = dati["glifi"][4]

    x_height = 503
    ink_x0, ink_y0, ink_x1, ink_y1 = SIMBOLI["blocco"]["ink"]
    # Il simbolo occupa in verticale quel che occupa la q: dall'altezza della x
    # al fondo della discendente.
    k = (x_height + DESC) / (ink_y1 - ink_y0)
    larghezza = (ink_x1 - ink_x0) * k

    # Centrato nella cassa che la q lascia libera, così il ritmo del rigo tiene.
    dx = q["x"] + (q["advance"] - larghezza) / 2 - ink_x0 * k
    dy = -x_height - ink_y0 * k

    # I tracciati delle altre sette lettere, la q esclusa.
    prima = wordmark("Post", peso, tracking)
    dopo = wordmark("ron", peso, tracking)
    offset = dati["glifi"][5]["x"]

    return {
        "prima": prima["d"],
        "dopo": dopo["d"],
        "dopo_transform": f"translate({offset:.0f} 0)",
        "simbolo_transform": f"translate({dx:.1f} {dy:.1f}) scale({k:.4f})",
        "advance": dati["advance"],
        "lsb": dati["glifi"][0]["bbox"][0],
    }


# -------------------------------------------------------------------- lockup


def lockup(chiave: str, peso: float = 600, tracking: float = -15, coda: bool = True,
           gap: float = 300) -> dict:
    """Simbolo e logotipo affiancati, in un unico sistema di coordinate."""
    logo = logotipo(peso, tracking, coda)
    x0, y0, x1, y1 = SIMBOLI[chiave]["ink"]

    # L'inchiostro del simbolo copre dall'altezza delle maiuscole al fondo della
    # discendente: è l'estensione verticale del logotipo, non la sua altezza
    # nominale, ciò che fa sembrare due elementi un elemento solo.
    k = (CAP + DESC) / (y1 - y0)
    dx = -x0 * k
    dy = -CAP - y0 * k

    larghezza_simbolo = (x1 - x0) * k
    # Il gap è ottico: si misura fra gli inchiostri, quindi si sottrae
    # l'avvicinamento laterale sinistro della P.
    origine_logo = larghezza_simbolo + gap - logo["lsb"]

    return {
        "simbolo_transform": f"translate({dx + 0:.1f} {dy:.1f}) scale({k:.4f})",
        "logo_transform": f"translate({origine_logo:.1f} 0)",
        "larghezza": origine_logo + logo["advance"],
        "logo": logo,
        "alto": CAP,
        "basso": DESC,
    }
