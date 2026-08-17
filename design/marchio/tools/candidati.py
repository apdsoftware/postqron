#!/usr/bin/env python3
"""
Genera `design/marchio/lab/candidati.html`: le sei direzioni da guardare.

Le stesse prove per tutte: 48, 24 e 16 px in colore, monocromatiche e
invertite; il lockup col logotipo su chiaro, su scuro e a un colore; l'header
reale del sito; la scheda del browser a 16 px.
"""
from __future__ import annotations

from pathlib import Path

import marchio as M
from wordmark import wordmark

LAB = Path(__file__).resolve().parents[1] / "lab"
SIZES = (48, 24, 16)


def _iniziale_p() -> dict:
    """
    La P delle lettere disegnate, portata nel riquadro da 32.

    Serve alla direzione «solo logotipo»: una favicon non può contenere una
    parola, quindi anche chi rinuncia al simbolo deve staccare qualcosa dal
    logotipo. Meglio la sua stessa P che una forma inventata per l'occasione.
    """
    dati = wordmark("P", 600, 0)
    x0, y0, x1, y1 = dati["glifi"][0]["bbox"]  # coordinate font, y verso l'alto
    k = 26 / M.CAP
    return {
        "d": dati["d"],
        "transform": f"translate({16 - (x1 - x0) * k / 2 - x0 * k:.2f} {16 + 26 / 2:.2f})"
                     f" scale({k:.4f})",
    }


def svg_simbolo(chiave: str, gradiente: bool, sfondo: str, px: int, uid: str) -> str:
    paint = f"url(#{uid})" if gradiente else ("#fff" if sfondo == "scuro" else M.PROFONDO)
    defs = f"<defs>{M.gradiente(uid)}</defs>" if gradiente else ""
    if M.SIMBOLI[chiave].get("solo_logotipo"):
        # La P vive in unità di em, non sulla griglia da 32: il gradiente in
        # coordinate utente va ancorato al suo riquadro, o resta fuori campo e
        # la lettera esce di un colore solo.
        p = _iniziale_p()
        defs = f"<defs>{M.gradiente(uid, box=(81, 0, 560, -700))}</defs>" if gradiente else ""
        corpo = f'<g transform="{p["transform"]}"><path fill="{paint}" d="{p["d"]}"/></g>'
    else:
        corpo = M.simbolo_svg(chiave, paint)
    return f'<svg viewBox="0 0 32 32" width="{px}" height="{px}">{defs}{corpo}</svg>'


def svg_lockup(chiave: str, uid: str, gradiente: bool, colore_testo: str,
               altezza: int) -> str:
    """Simbolo e logotipo affiancati — o il solo logotipo, dove il simbolo non c'è."""
    logo = M.logotipo(600, -15, coda=False)
    alto, basso = M.CAP, M.DESC
    scala = altezza / (alto + basso)
    solo = M.SIMBOLI[chiave].get("solo_logotipo")

    if solo:
        larghezza = logo["advance"]
        corpo = f'<path d="{logo["d"]}" fill="{colore_testo}"/>'
        defs = ""
    else:
        lk = M.lockup(chiave, 600, -15, coda=False)
        larghezza = lk["larghezza"]
        paint = f"url(#{uid})" if gradiente else colore_testo
        defs = f"<defs>{M.gradiente(uid)}</defs>" if gradiente else ""
        corpo = (
            f'<g transform="{lk["simbolo_transform"]}">{M.simbolo_svg(chiave, paint)}</g>'
            f'<g transform="{lk["logo_transform"]}">'
            f'<path d="{logo["d"]}" fill="{colore_testo}"/></g>'
        )

    return (
        f'<svg viewBox="0 {-alto} {larghezza:.0f} {alto + basso}" height="{altezza}"'
        f' width="{larghezza * scala:.0f}">{defs}{corpo}</svg>'
    )


def sezione(chiave: str) -> str:
    s = M.SIMBOLI[chiave]

    scale = "".join(
        "".join(
            f'<div class="px"><div class="box {sfondo}">'
            f"{svg_simbolo(chiave, grad, sfondo, px, f'{chiave}-{i}-{px}')}</div>"
            f"<span>{px}</span></div>"
            for px in SIZES
        ) + '<div class="sep"></div>'
        for i, (grad, sfondo) in enumerate(
            ((True, "chiaro"), (False, "chiaro"), (False, "scuro"))
        )
    )

    lockups = "".join(
        f'<div class="box {sfondo} grande">'
        f"{svg_lockup(chiave, f'lk{i}-{chiave}', grad, testo, 40)}</div>"
        for i, (grad, testo, sfondo) in enumerate(
            ((True, M.PROFONDO, "chiaro"), (False, "#fff", "scuro"),
             (False, M.PROFONDO, "chiaro"))
        )
    )

    header = (
        '<div class="header">'
        + svg_lockup(chiave, f"hd-{chiave}", False, "#fff", 34)
        + '<nav><span>Funzionalità</span><span>Prezzi</span><span>FAQ</span>'
        '<em>Inizia ora</em></nav></div>'
    )

    tab = (
        '<div class="tab"><div class="favicon">'
        + svg_simbolo(chiave, True, "chiaro", 16, f"fv-{chiave}")
        + "</div><span>Postqron — cronjob gestiti</span></div>"
    )

    return f"""<section class="direzione" id="{chiave}">
  <h3>{s['nome']} <em>— {s['sottotitolo']}</em> <b>{s['famiglia']}</b></h3>
  <p class="costo"><strong>Cosa si perde:</strong> {COSTI[chiave]}</p>
  <div class="scale">{scale}</div>
  <div class="lockups">{lockups}</div>
  {header}
  {tab}
</section>"""


COSTI = {
    "gallo": "il registro. Una mascotte compra affetto, non rispetto, e il "
             "prodotto si vende a professionisti; per rendere davvero va nutrita "
             "— illustrazioni, adesivi, una voce — altrimenti resta un animale "
             "solo in un header serio. E l'occhio è il dettaglio che a 16 px "
             "lavora peggio di tutti.",
    "timbro": "la nitidezza alle misure piccole. Un anello è la forma che regge "
              "peggio i 16 px, e le due barre di annullo dentro il cerchio lo "
              "chiudono ancora. È anche il più vicino, fra i sei, a farsi "
              "scambiare per un quadrante — cioè per l'orologio da cui scappiamo.",
    "francobollo": "la dentellatura, che è tutto. A 48 px si legge «francobollo»; "
                   "a 16 px le tacche valgono un pixel e mezzo e resta un quadrato. "
                   "Il marchio perde la sua unica idea proprio alla misura in cui "
                   "verrà visto più spesso.",
    "onda": "la specificità. Dice «periodico» a chiunque abbia visto un "
            "oscilloscopio e non dice niente a tutti gli altri, e in un vicinato "
            "che ha già la linea dell'elettrocardiogramma di Healthchecks il "
            "rischio è di leggersi come «monitoraggio» invece che come «cron».",
    "pi": "la distintività, per intero. Una P geometrica ce l'hanno in venti, ed è "
          "esattamente il difetto — competente e dimenticabile — che ha fatto "
          "cadere il giro precedente. Si sceglie solo se si vuole un marchio che "
          "non dica nulla e non sbagli nulla.",
    "logotipo": "tutto ciò che un simbolo serve a fare: l'avatar, l'adesivo, la "
                "favicon, l'angolo di una slide, l'icona dell'applicazione. Resta "
                "la P staccata dal logotipo, che è un ripiego, non un marchio. "
                "Funziona per Cronitor perché il loro nero è già una posizione; "
                "va deciso sapendo che si rinuncia a un'immagine.",
}


HTML = """<!doctype html>
<html lang="it"><head><meta charset="utf-8"><title>Postqron — sei direzioni</title>
<style>
  body {{ margin:0; padding:40px 48px 80px; background:#f7fafd; color:#1e3056;
         font:14px/1.55 -apple-system, system-ui, sans-serif; }}
  h1 {{ font-size:22px; margin:0 0 4px; }}
  h1 + p {{ margin:0 0 28px; color:#6f8ba4; max-width:78ch; }}
  h3 {{ margin:0 0 10px; font-size:17px; font-weight:600; }}
  h3 em {{ font-style:normal; font-weight:400; color:#6f8ba4; font-size:14px; }}
  h3 b {{ font-size:10px; letter-spacing:.09em; text-transform:uppercase; color:#6f8ba4;
          border:1px solid #dbe6fb; border-radius:100px; padding:3px 9px; vertical-align:2px;
          font-weight:600; }}
  .direzione {{ margin:0 0 44px; padding:24px 26px; background:#fff; border:1px solid #e2ebff;
                border-radius:14px; }}
  .costo {{ max-width:82ch; margin:0 0 20px; color:#43536f; }}
  .costo strong {{ color:#1e3056; }}
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
             padding:0 32px; height:96px; border-radius:10px;
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
<h1>Postqron — sei direzioni, sei famiglie</h1>
<p>Mascotte, oggetto, forma astratta, monogramma, nessun simbolo. Ognuna alle
dimensioni in cui vivrà: 48, 24 e 16 px, in colore, monocromatica su chiaro e
invertita su scuro; poi il lockup, l'header reale del sito e la scheda del
browser a 16 px.</p>
{sezioni}
</body></html>
"""


def main() -> None:
    LAB.mkdir(parents=True, exist_ok=True)
    (LAB / "candidati.html").write_text(
        HTML.format(sezioni="".join(sezione(k) for k in M.SIMBOLI)), "utf-8"
    )
    print(LAB / "candidati.html")


if __name__ == "__main__":
    main()
