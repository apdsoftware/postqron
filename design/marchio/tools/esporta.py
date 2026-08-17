#!/usr/bin/env python3
"""
Esporta gli asset definitivi del marchio.

Gli SVG del kit vivono in `design/marchio/svg/`; quelli che il sito serve
davvero vengono scritti anche in `apps/web/public/`. I PNG (icona applicazione,
card social) si ottengono rasterizzando gli stessi SVG: non esiste una seconda
sorgente da tenere allineata a mano.

Uso:  python3 esporta.py
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

from PIL import Image

import marchio as M

RADICE = Path(__file__).resolve().parents[3]
KIT = RADICE / "design/marchio/svg"
PNG = RADICE / "design/marchio/png"
PUBLIC = RADICE / "apps/web/public"

INTESTAZIONE = "<!-- Postqron — marchio registrato. design/marchio/README.md -->"


# ------------------------------------------------------------------ documenti


def doc(viewbox: str, corpo: str, titolo: str = "Postqron", larghezza: str = "",
        altezza: str = "") -> str:
    misure = f' width="{larghezza}" height="{altezza}"' if larghezza else ""
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{viewbox}"{misure}'
        f' role="img" aria-label="{titolo}">\n  {INTESTAZIONE}\n'
        f"  <title>{titolo}</title>\n{corpo}\n</svg>\n"
    )


def simbolo_doc(paint: str, uid: str = "pq") -> str:
    defs = f"  <defs>{M.gradiente(uid)}</defs>\n" if paint.startswith("url") else ""
    return doc("0 0 32 32", defs + "  " + M.simbolo_svg(M.SCELTO, paint))


def marchio_doc(colore_testo: str, paint: str, uid: str = "pq") -> str:
    """Il marchio completo: simbolo e logotipo affiancati."""
    lk = M.lockup(M.SCELTO, 600, -15, coda=False)
    alto, basso = M.CAP, M.DESC
    defs = f"  <defs>{M.gradiente(uid)}</defs>\n" if paint.startswith("url") else ""
    corpo = (
        defs
        + f'  <g transform="translate(0 {alto})">\n'
        f'    <g transform="{lk["simbolo_transform"]}">'
        f'{M.simbolo_svg(M.SCELTO, paint)}</g>\n'
        f'    <g transform="{lk["logo_transform"]}">'
        f'<path d="{lk["logo"]["d"]}" fill="{colore_testo}"/></g>\n  </g>'
    )
    return doc(f"0 0 {round(lk['larghezza'])} {alto + basso}", corpo)


def logotipo_doc(colore: str) -> str:
    """Le sole lettere, q compresa: serve dove il simbolo è già presente."""
    lg = M.logotipo(600, -15, coda=False)
    alto, basso = M.CAP, M.DESC
    corpo = (
        f'  <g transform="translate(0 {alto})">'
        f'<path d="{lg["d"]}" fill="{colore}"/></g>'
    )
    return doc(f"0 0 {round(lg['advance'])} {alto + basso}", corpo)


def icona_doc() -> str:
    """
    Icona applicazione: campo pieno, simbolo in negativo.

    iOS e Android applicano la propria maschera all'icona e ignorano la
    trasparenza. Un simbolo su fondo trasparente diventerebbe quindi un simbolo
    su fondo nero o bianco a seconda del sistema: il fondo va dichiarato qui.
    """
    x0, y0, x1, y1 = M.SIMBOLI[M.SCELTO]["ink"]
    # Il simbolo occupa il 52% del lato, che è la proporzione delle icone iOS:
    # la maschera di sistema mangia gli angoli, e un disegno più grande ci
    # finisce dentro.
    k = 512 * 0.52 / max(x1 - x0, y1 - y0)
    dx = 256 - (x0 + x1) / 2 * k
    dy = 256 - (y0 + y1) / 2 * k
    corpo = (
        f"  <defs>{M.gradiente('pq-icona')}</defs>\n"
        f'  <rect width="512" height="512" fill="url(#pq-icona)"/>\n'
        f'  <g transform="translate({dx:.1f} {dy:.1f}) scale({k:.4f})">'
        f'{M.simbolo_svg(M.SCELTO, "#fff")}</g>'
    )
    return doc("0 0 512 512", corpo, larghezza="512", altezza="512")


def card_doc() -> str:
    """Card social 1200×630 (Open Graph): marchio in negativo su fondo profondo."""
    lk = M.lockup(M.SCELTO, 600, -15, coda=False)
    dominio = M.logotipo(500, 20, coda=False, testo="postqron.com")

    # Il marchio è alto 132 px, misurati dall'altezza delle maiuscole al fondo
    # della discendente; sotto, il dominio a 24 px di altezza delle maiuscole.
    # Tutte le coordinate sono riferite alla linea di base, non al riquadro:
    # è ciò che tiene allineate due composizioni di corpo diverso.
    k = 132 / (M.CAP + M.DESC)
    x = (1200 - lk["larghezza"] * k) / 2
    base = 218 + M.CAP * k

    kd = 24 / M.CAP
    xd = (1200 - dominio["advance"] * kd) / 2
    based = 420

    corpo = f"""  <defs>
    <linearGradient id="pq-card" x1="0" y1="1" x2="1" y2="0">
      <stop offset="0" stop-color="{M.CIANO}"/><stop offset="1" stop-color="{M.VIOLA}"/>
    </linearGradient>
    <radialGradient id="pq-alone" cx="0.5" cy="0.5" r="0.5">
      <stop offset="0" stop-color="{M.VIOLA}" stop-opacity="0.85"/>
      <stop offset="1" stop-color="{M.VIOLA}" stop-opacity="0"/>
    </radialGradient>
    <radialGradient id="pq-alone2" cx="0.5" cy="0.5" r="0.5">
      <stop offset="0" stop-color="{M.CIANO}" stop-opacity="0.7"/>
      <stop offset="1" stop-color="{M.CIANO}" stop-opacity="0"/>
    </radialGradient>
  </defs>
  <rect width="1200" height="630" fill="#111a2e"/>
  <ellipse cx="960" cy="120" rx="620" ry="420" fill="url(#pq-alone)"/>
  <ellipse cx="180" cy="600" rx="560" ry="380" fill="url(#pq-alone2)"/>
  <g transform="translate({x:.1f} {base:.1f}) scale({k:.5f})">
    <g transform="{lk["simbolo_transform"]}">{M.simbolo_svg(M.SCELTO, "#fff")}</g>
    <g transform="{lk["logo_transform"]}"><path d="{lk["logo"]["d"]}" fill="#fff"/></g>
  </g>
  <g transform="translate({xd:.1f} {based:.1f}) scale({kd:.5f})" opacity="0.6">
    <path d="{dominio["d"]}" fill="#fff"/>
  </g>"""
    return doc("0 0 1200 630", corpo, "Postqron", "1200", "630")


# ---------------------------------------------------------------- rasterizzazione
#
# I PNG li disegna un browser, non un convertitore da riga di comando.
#
# Prima versione con `qlmanage`: inscrive sempre il disegno in un quadrato, con
# una lettereggiatura che non dichiara, e la card usciva scalata e tagliata.
# Il browser è anche il motore che renderà davvero questi SVG sul sito, quindi
# rasterizzare lì significa che PNG e vettore non possono divergere.
#
# `esporta.py --servi` alza un server locale, apre `lab/raster.html`, e la
# pagina rimanda indietro ogni PNG via POST.

RASTER: list[tuple[str, int, int]] = [
    ("postqron-icona.svg", 180, 180),
    ("postqron-icona.svg", 512, 512),
    ("postqron-icona.svg", 256, 256),
    ("postqron-card-social.svg", 1200, 630),
]

DESTINAZIONI = {
    ("postqron-icona.svg", 180): [PUBLIC / "apple-touch-icon.png"],
    ("postqron-icona.svg", 512): [PNG / "postqron-icona-512.png"],
    ("postqron-icona.svg", 256): [PNG / "postqron-icona-256.png"],
    ("postqron-card-social.svg", 1200): [PUBLIC / "social-card.png",
                                         PNG / "postqron-card-social.png"],
}

PAGINA = """<!doctype html>
<html lang="it"><head><meta charset="utf-8"><title>Postqron — rasterizzazione</title>
<style>body{font:14px/1.6 system-ui;padding:32px;color:#1e3056}</style></head><body>
<h1>Rasterizzazione</h1><ol id="esito"></ol>
<script>
const LAVORI = %s
async function png(url, w, h) {
  const img = new Image()
  img.width = w; img.height = h
  await new Promise((ok, ko) => { img.onload = ok; img.onerror = ko; img.src = url })
  const tela = document.createElement('canvas')
  tela.width = w; tela.height = h
  tela.getContext('2d').drawImage(img, 0, 0, w, h)
  return tela.toDataURL('image/png')
}
;(async () => {
  for (const lavoro of LAVORI) {
    const dati = await png('../svg/' + lavoro.file, lavoro.w, lavoro.h)
    await fetch('/salva?file=' + encodeURIComponent(lavoro.file) + '&w=' + lavoro.w,
                { method: 'POST', body: dati })
    document.querySelector('#esito').insertAdjacentHTML(
      'beforeend', `<li>${lavoro.file} — ${lavoro.w}×${lavoro.h}</li>`)
  }
  document.title = 'fatto'
  document.body.insertAdjacentHTML('beforeend', '<p id="fatto">Fatto.</p>')
})()
</script></body></html>
"""


def servi(porta: int = 8897) -> None:
    """Serve `design/marchio/` e accoglie i PNG che la pagina rimanda indietro."""
    import base64
    import functools
    import io
    import http.server
    import urllib.parse

    radice = RADICE / "design/marchio"
    lavori = [{"file": f, "w": w, "h": h} for f, w, h in RASTER]
    (radice / "lab/raster.html").write_text(PAGINA % json.dumps(lavori), "utf-8")

    attesi = {(f, w) for f, w, _ in RASTER}

    class Handler(http.server.SimpleHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802 — nome imposto da BaseHTTPRequestHandler
            query = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            file, larghezza = query["file"][0], int(query["w"][0])
            corpo = self.rfile.read(int(self.headers["Content-Length"]))
            grezzo = base64.b64decode(corpo.split(b",", 1)[1])
            # Il PNG che esce da `toDataURL` non è filtrato: la card social
            # passava i 600 kB per un disegno di quattro forme. Un giro di
            # Pillow con `optimize` la riporta a una frazione, senza perdite.
            immagine = Image.open(io.BytesIO(grezzo))
            for destinazione in DESTINAZIONI[(file, larghezza)]:
                destinazione.parent.mkdir(parents=True, exist_ok=True)
                immagine.save(destinazione, optimize=True)
                peso = destinazione.stat().st_size // 1024
                print(f"png {destinazione.relative_to(RADICE)} — {peso} kB")
            attesi.discard((file, larghezza))
            self.send_response(204)
            self.end_headers()
            if not attesi:
                ico(PNG / "postqron-icona-256.png", PUBLIC / "favicon.ico")
                print("ico", "apps/web/public/favicon.ico")

        def log_message(self, *_: object) -> None:
            pass

    server = http.server.ThreadingHTTPServer(
        ("127.0.0.1", porta), functools.partial(Handler, directory=str(radice))
    )
    print(f"apri http://127.0.0.1:{porta}/lab/raster.html")
    server.serve_forever()


def ico(sorgente: Path, destinazione: Path) -> None:
    """
    Favicon .ico con tre misure.

    L'SVG basta a ogni browser in circolazione, ma non a chi salva il sito fra i
    preferiti su Windows né ai lettori di feed: il file .ico resta il formato che
    tutti sanno leggere, e pesa tre kilobyte.
    """
    Image.open(sorgente).save(destinazione, sizes=[(16, 16), (32, 32), (48, 48)])


# ------------------------------------------------------------------------ main

ASSET_SVG = {
    "postqron-marchio.svg": lambda: marchio_doc(M.PROFONDO, "url(#pq)"),
    "postqron-marchio-invertito.svg": lambda: marchio_doc("#fff", "#fff"),
    "postqron-marchio-mono.svg": lambda: marchio_doc(M.PROFONDO, M.PROFONDO),
    "postqron-simbolo.svg": lambda: simbolo_doc("url(#pq)"),
    "postqron-simbolo-invertito.svg": lambda: simbolo_doc("#fff"),
    "postqron-simbolo-mono.svg": lambda: simbolo_doc(M.PROFONDO),
    "postqron-logotipo.svg": lambda: logotipo_doc(M.PROFONDO),
    "postqron-icona.svg": icona_doc,
    "postqron-card-social.svg": card_doc,
}


def modulo_ts() -> str:
    """
    Le geometrie del marchio come modulo TypeScript.

    Il componente `SiteLogo.vue` disegna il marchio inline — è l'unico modo
    perché erediti colore e dimensione dalla pagina — ma i tracciati non vanno
    ricopiati lì a mano: sarebbero una seconda sorgente destinata a divergere
    dal kit in `design/marchio/`. Questo file li importa da lì.
    """
    lk = M.lockup(M.SCELTO, 600, -15, coda=False)
    simbolo = M.SIMBOLI[M.SCELTO]
    tracciati = ",\n  ".join(f"'{d}'" for d in simbolo["tracciati"])
    return f"""/*
 * Geometrie del marchio Postqron.
 *
 * GENERATO da `design/marchio/tools/esporta.py` — non modificare a mano.
 * Il disegno, le sue ragioni e le regole d'uso stanno in
 * `design/marchio/README.md`.
 */

/** Riquadro del simbolo: griglia quadrata di 32 unità. */
export const SIMBOLO_VIEWBOX = '0 0 32 32'

/**
 * Tracciati del simbolo, con le controforme come sottotracciati in `evenodd`.
 *
 * Sono uno o due e non venti: forme separate prenderebbero ognuna il proprio
 * riquadro di gradiente, e il marchio diventerebbe un collage di sfumature.
 */
export const SIMBOLO_TRACCIATI = [
  {tracciati},
] as const

/** Spessore del tratto, o 0 se il simbolo è un pieno. */
export const SIMBOLO_STROKE = {simbolo["stroke"]:g}

/**
 * Marchio completo: simbolo e logotipo affiancati.
 *
 * Il riquadro va dall'altezza delle maiuscole al fondo della discendente, che
 * è l'estensione verticale reale del disegno: chi lo compone gli dà un'altezza
 * e ottiene la proporzione giusta senza conoscerne la costruzione.
 */
export const MARCHIO = {{
  viewBox: '0 0 {round(lk["larghezza"])} {M.CAP + M.DESC}',
  larghezza: {round(lk["larghezza"])},
  altezza: {M.CAP + M.DESC},
  /** Dal riquadro da 32 del simbolo alla cassa del logotipo. */
  simboloTransform: '{lk["simbolo_transform"]}',
  /** Le lettere, e dove cominciano. */
  logoTransform: '{lk["logo_transform"]}',
  lettere: '{lk["logo"]["d"]}',
  /** Le lettere sono disegnate sulla linea di base, non sul bordo del riquadro. */
  lineaDiBase: {M.CAP},
}} as const
"""


def main() -> None:
    KIT.mkdir(parents=True, exist_ok=True)
    for nome, costruttore in ASSET_SVG.items():
        (KIT / nome).write_text(costruttore(), "utf-8")
        print("svg", nome)

    # L'unico vettore che il sito serve come file: la favicon. Il marchio
    # dell'header vive invece nel componente, che importa il modulo qui sotto.
    (PUBLIC / "favicon.svg").write_text(simbolo_doc("url(#pq)"), "utf-8")
    print("svg apps/web/public/favicon.svg")
    (RADICE / "apps/web/utils/marchio.ts").write_text(modulo_ts(), "utf-8")
    print("ts  apps/web/utils/marchio.ts")

    if "--servi" in sys.argv:
        servi()


if __name__ == "__main__":
    main()
