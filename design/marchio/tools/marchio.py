#!/usr/bin/env python3
"""
Costruzione del marchio: simboli, logotipo, lockup.

Un solo posto in cui vivono le geometrie. Da qui escono sia la pagina di prova
(`proposte.py`) sia gli asset definitivi (`esporta.py`): se un tracciato cambia,
cambia ovunque, e non esiste la copia dimenticata che diverge in silenzio.
"""
from __future__ import annotations

from simboli import CANDIDATI
from wordmark import wordmark

# ------------------------------------------------------------------- palette
CIANO = "#0fb4e5"
VIOLA = "#743fe5"
PROFONDO = "#1e3056"

CAP = 700  # altezza delle maiuscole di Quicksand, in unità di em (upem 1000)
DESC = 200  # profondità della discendente della q


def gradiente(uid: str, lato: float = 32,
              box: tuple[float, float, float, float] | None = None) -> str:
    """
    Gradiente del marchio, in coordinate utente e non per oggetto.

    Con `objectBoundingBox` — il valore predefinito — ogni forma del simbolo
    prenderebbe la sfumatura intera sul proprio riquadro: la cresta del gallo
    partirebbe dal ciano e ripartirebbe dal ciano anche il becco. Ancorandolo
    alla griglia da 32 la sfumatura è una sola, e attraversa il disegno.
    """
    x0, y0, x1, y1 = box or (0.0, lato, lato, 0.0)
    return (
        f'<linearGradient id="{uid}" gradientUnits="userSpaceOnUse"'
        f' x1="{x0:g}" y1="{y0:g}" x2="{x1:g}" y2="{y1:g}">'
        f'<stop offset="0" stop-color="{CIANO}"/>'
        f'<stop offset="1" stop-color="{VIOLA}"/></linearGradient>'
    )


# ------------------------------------------------------------------- simboli

SIMBOLI = CANDIDATI

#: Direzione in produzione. Cambiare questa riga e rilanciare `esporta.py`
#: rifà kit, favicon, icona, card e il modulo TypeScript del sito.
SCELTO = "gallo"


def simbolo_svg(chiave: str, paint: str) -> str:
    """Contenuto SVG del simbolo, senza il tag <svg> né i <defs>."""
    s = SIMBOLI[chiave]
    if s["stroke"]:
        comune = (f'fill="none" stroke="{paint}" stroke-width="{s["stroke"]:g}"'
                  ' stroke-linecap="round" stroke-linejoin="round"')
        return "".join(f'<path {comune} d="{d}"/>' for d in s["tracciati"])
    return "".join(
        f'<path fill="{paint}" fill-rule="evenodd" d="{d}"/>' for d in s["tracciati"]
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
