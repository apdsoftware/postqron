#!/usr/bin/env python3
"""Genera `design/marchio/lab/proposte.html`: le direzioni da guardare."""
from __future__ import annotations

from pathlib import Path

import marchio as M

LAB = Path(__file__).resolve().parents[1] / "lab"
SIZES = (48, 24, 16)

VERDETTI = {
    "blocco": "Il simbolo <b>prende il posto della q</b> nel logotipo, invece di "
              "starle accanto: marchio e nome sono un oggetto solo, e il simbolo "
              "si stacca da lì per la favicon. È l'unica delle tre che il nome non "
              "potrebbe prestare a nessun altro prodotto.",
    "indent": "Dice «configurazione come codice» nel modo più diretto e regge "
              "benissimo i 16 px. Legge però come l'icona «vista a elenco» di una "
              "barra strumenti: competente e dimenticabile.",
    "gradino": "Il più insolito dei tre. È alto e stretto, non sta bene accanto "
               "alla parola, e a mente fredda si legge come un tubo o una zeta.",
}


def svg_simbolo(chiave: str, paint: str, uid: str, px: int) -> str:
    defs = f"<defs>{M.gradiente(uid)}</defs>" if paint.startswith("url") else ""
    return (
        f'<svg viewBox="0 0 32 32" width="{px}" height="{px}">{defs}'
        f"{M.simbolo_svg(chiave, paint)}</svg>"
    )


def svg_lockup(chiave: str, uid: str, colore_simbolo: str, colore_testo: str,
               altezza: int, peso: float = 600, tracking: float = -15,
               coda: bool = False) -> str:
    lk = M.lockup(chiave, peso, tracking, coda)
    alto, basso = lk["alto"], lk["basso"]
    vb = f"0 {-alto} {lk['larghezza']:.0f} {alto + basso}"
    defs = f"<defs>{M.gradiente(uid)}</defs>" if colore_simbolo.startswith("url") else ""
    coda_d = lk["logo"]["coda"]
    coda_svg = f'<path d="{coda_d}" fill="{colore_testo}"/>' if coda_d else ""
    scala = altezza / (alto + basso)
    return (
        f'<svg viewBox="{vb}" height="{altezza}" width="{lk["larghezza"] * scala:.0f}">{defs}'
        f'<g transform="{lk["simbolo_transform"]}">{M.simbolo_svg(chiave, colore_simbolo)}</g>'
        f'<g transform="{lk["logo_transform"]}">'
        f'<path d="{lk["logo"]["d"]}" fill="{colore_testo}"/>{coda_svg}</g></svg>'
    )


def svg_logotipo(altezza: int, peso: float, tracking: float, coda: bool,
                 colore: str = M.PROFONDO) -> str:
    logo = M.logotipo(peso, tracking, coda)
    alto, basso = M.CAP, M.DESC
    larghezza = logo["advance"]
    scala = altezza / (alto + basso)
    coda_svg = f'<path d="{logo["coda"]}" fill="{colore}"/>' if logo["coda"] else ""
    return (
        f'<svg viewBox="0 {-alto} {larghezza:.0f} {alto + basso}" height="{altezza}"'
        f' width="{larghezza * scala:.0f}">'
        f'<path d="{logo["d"]}" fill="{colore}"/>{coda_svg}</svg>'
    )


# --------------------------------------------------------------------- pagina


def sezione_logotipo() -> str:
    righe = []
    for peso in (500, 600, 700):
        celle = "".join(
            f'<div class="prova"><div>{svg_logotipo(34, peso, tr, False)}</div>'
            f"<span>wght {peso} · tracking {tr}</span></div>"
            for tr in (0, -15, -30)
        )
        righe.append(f'<div class="fila">{celle}</div>')

    coda = "".join(
        f'<div class="prova"><div>{svg_logotipo(px, 600, -15, True)}</div>'
        f"<span>discendente prolungata · {px} px</span></div>"
        for px in (72, 44, 28)
    )
    senza = "".join(
        f'<div class="prova"><div>{svg_logotipo(px, 600, -15, False)}</div>'
        f"<span>lettere sole · {px} px</span></div>"
        for px in (72, 44, 28)
    )
    innesto = "".join(
        f'<div class="prova"><div>{svg_innesto(px)}</div>'
        f"<span>simbolo al posto della q · {px} px</span></div>"
        for px in (72, 44, 28)
    )
    innesto_scuro = (
        '<div class="prova"><div class="box scuro grande">'
        + svg_innesto(44, "#fff", "#fff")
        + "</div><span>invertito</span></div>"
        + '<div class="prova"><div class="box chiaro grande">'
        + svg_innesto(44, M.PROFONDO, M.PROFONDO)
        + "</div><span>monocromatico</span></div>"
    )
    return (
        "<h2>Logotipo — peso, crenatura, discendente</h2>"
        + "".join(righe)
        + f'<div class="fila">{coda}</div><div class="fila">{senza}</div>'
        + f'<div class="fila">{innesto}</div><div class="fila">{innesto_scuro}</div>'
    )


def svg_innesto(altezza: int, colore: str = M.PROFONDO,
                simbolo: str | None = None) -> str:
    """Logotipo con il simbolo «blocco» innestato al posto della q."""
    lg = M.logotipo_innestato()
    alto, basso = M.CAP, M.DESC
    scala = altezza / (alto + basso)
    uid = f"in-{altezza}-{colore.strip('#')}"
    paint = simbolo or f"url(#{uid})"
    defs = f"<defs>{M.gradiente(uid)}</defs>" if simbolo is None else ""
    return (
        f'<svg viewBox="0 {-alto} {lg["advance"]:.0f} {alto + basso}" height="{altezza}"'
        f' width="{lg["advance"] * scala:.0f}">{defs}'
        f'<path d="{lg["prima"]}" fill="{colore}"/>'
        f'<g transform="{lg["dopo_transform"]}"><path d="{lg["dopo"]}" fill="{colore}"/></g>'
        f'<g transform="{lg["simbolo_transform"]}">'
        f"{M.simbolo_svg('blocco', paint)}</g></svg>"
    )


def sezione_direzione(chiave: str) -> str:
    s = M.SIMBOLI[chiave]

    def cella(i: int, sfondo: str, px: int, gradiente: bool) -> str:
        uid = f"{chiave}-{i}-{px}"
        paint = f"url(#{uid})" if gradiente else ("#fff" if sfondo == "scuro" else M.PROFONDO)
        return (
            f'<div class="px"><div class="box {sfondo}">'
            f"{svg_simbolo(chiave, paint, uid, px)}</div><span>{px}</span></div>"
        )

    scale = "".join(
        "".join(cella(i, sfondo, px, gradiente) for px in SIZES) + '<div class="sep"></div>'
        for i, (gradiente, sfondo) in enumerate(
            ((True, "chiaro"), (False, "chiaro"), (False, "scuro"))
        )
    )

    # «Blocco» non si compone: il simbolo prende il posto della q. Le altre due
    # direzioni restano un simbolo accostato al logotipo, che è la forma
    # normale — ed è anche parte di ciò che le rende meno memorabili.
    innestato = chiave == "blocco"

    def marchio(colore_simbolo: str, colore_testo: str, altezza: int, uid: str) -> str:
        if innestato:
            return svg_innesto(altezza, colore_testo,
                               None if colore_simbolo.startswith("url") else colore_simbolo)
        return svg_lockup(chiave, uid, colore_simbolo, colore_testo, altezza)

    lockup_chiaro = marchio(f"url(#lk-{chiave})", M.PROFONDO, 40, f"lk-{chiave}")
    lockup_scuro = marchio("#fff", "#fff", 40, f"lkd-{chiave}")
    lockup_mono = marchio(M.PROFONDO, M.PROFONDO, 40, f"lkm-{chiave}")

    header = (
        '<div class="header">'
        + marchio("#fff", "#fff", 34, f"hd-{chiave}")
        + '<nav><span>Funzionalità</span><span>Prezzi</span><span>FAQ</span>'
        '<em>Inizia ora</em></nav></div>'
    )

    tab = (
        '<div class="tab"><div class="favicon">'
        + svg_simbolo(chiave, f"url(#fv-{chiave})", f"fv-{chiave}", 16)
        + "</div><span>Postqron — cronjob come codice</span></div>"
    )

    verdetto = VERDETTI[chiave]
    return f"""<section class="direzione{' consigliata' if innestato else ''}">
  <h3>{s['nome']} <em>— {s['sottotitolo']}</em>
    {'<b>consigliata</b>' if innestato else ''}</h3>
  <p class="verdetto">{verdetto}</p>
  <div class="scale">{scale}</div>
  <div class="lockups">
    <div class="box chiaro grande">{lockup_chiaro}</div>
    <div class="box scuro grande">{lockup_scuro}</div>
    <div class="box chiaro grande">{lockup_mono}</div>
  </div>
  {header}
  {tab}
</section>"""


HTML = """<!doctype html>
<html lang="it"><head><meta charset="utf-8"><title>Postqron — proposte di marchio</title>
<style>
  body {{ margin:0; padding:40px 48px 80px; background:#f7fafd; color:#1e3056;
         font:14px/1.55 -apple-system, system-ui, sans-serif; }}
  h1 {{ font-size:22px; margin:0 0 4px; }}
  h1 + p {{ margin:0 0 28px; color:#6f8ba4; max-width:70ch; }}
  h2 {{ margin:44px 0 14px; font-size:12px; letter-spacing:.1em; text-transform:uppercase;
        color:#6f8ba4; font-weight:700; }}
  h3 {{ margin:0 0 14px; font-size:17px; font-weight:600; }}
  h3 em {{ font-style:normal; font-weight:400; color:#6f8ba4; font-size:14px; }}
  .fila {{ display:flex; gap:36px; align-items:flex-end; flex-wrap:wrap; margin-bottom:20px; }}
  .prova {{ display:flex; flex-direction:column; gap:8px; }}
  .prova span {{ font-size:10px; color:#8ea6c4; }}
  .direzione {{ margin:0 0 56px; padding:24px 26px; background:#fff; border:1px solid #e2ebff;
                border-radius:14px; }}
  .direzione.consigliata {{ border-color:#4278e5; box-shadow:0 0 0 3px rgb(66 120 229 / 12%); }}
  h3 b {{ font-size:11px; letter-spacing:.08em; text-transform:uppercase; color:#fff;
          background:#4278e5; border-radius:100px; padding:3px 10px; vertical-align:2px; }}
  .verdetto {{ max-width:76ch; margin:0 0 18px; color:#43536f; }}
  .verdetto b {{ font-weight:600; color:#1e3056; }}
  .scale {{ display:flex; align-items:flex-end; gap:16px; margin-bottom:22px; }}
  .sep {{ width:1px; align-self:stretch; background:#e2ebff; margin:0 10px; }}
  .px {{ display:flex; flex-direction:column; align-items:center; gap:6px; }}
  .px span {{ font-size:10px; color:#8ea6c4; }}
  .box {{ display:flex; align-items:center; justify-content:center; padding:10px;
          border-radius:8px; }}
  .box.chiaro {{ background:#fff; border:1px solid #e2ebff; }}
  .box.scuro {{ background:#111a2e; }}
  .box.grande {{ padding:22px 30px; }}
  .lockups {{ display:flex; gap:16px; flex-wrap:wrap; margin-bottom:22px; }}
  .header {{ display:flex; align-items:center; justify-content:space-between;
             padding:0 32px; height:100px; border-radius:10px;
             background:linear-gradient(127deg,#0fb4e5 0%,#743fe5 91%); margin-bottom:18px; }}
  .header nav {{ display:flex; gap:26px; align-items:center; color:#fff; font-size:13px;
                 font-weight:500; letter-spacing:.06em; }}
  .header em {{ font-style:normal; border:1px solid #fff; border-radius:100px;
                padding:5px 22px; font-size:12px; }}
  .tab {{ display:flex; align-items:center; gap:9px; width:290px; padding:8px 12px;
          background:#e6ecf5; border-radius:9px 9px 0 0; font-size:12px; color:#43536f;
          white-space:nowrap; overflow:hidden; }}
  .favicon {{ flex:none; display:flex; }}
  svg {{ display:block; }}
</style></head><body>
<h1>Postqron — tre direzioni di marchio</h1>
<p>Ogni direzione è mostrata alle dimensioni in cui vivrà: 48, 24 e 16 px, in colore,
monocromatica su chiaro e invertita su scuro; poi il lockup, l'header reale del sito e
la scheda del browser a 16 px.</p>
{logotipo}
<h2>Le tre direzioni</h2>
{direzioni}
</body></html>
"""


def main() -> None:
    LAB.mkdir(parents=True, exist_ok=True)
    direzioni = "".join(sezione_direzione(k) for k in M.SIMBOLI)
    (LAB / "proposte.html").write_text(
        HTML.format(logotipo=sezione_logotipo(), direzioni=direzioni), "utf-8"
    )
    print(LAB / "proposte.html")


if __name__ == "__main__":
    main()
