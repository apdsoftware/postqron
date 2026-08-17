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

    consigliata = chiave == M.SCELTO
    return f"""<section class="direzione{' consigliata' if consigliata else ''}" id="{chiave}">
  <h3>{s['nome']} <em>— {s['sottotitolo']}</em> <b>{s['famiglia']}</b>
    {'<i>consigliata</i>' if consigliata else ''}</h3>
  <p class="costo"><strong>Cosa si perde:</strong> {COSTI[chiave]}</p>
  <div class="lockups">{lockups}</div>
  {header}
  <div class="scale">{scale}</div>
  {tab}
</section>"""


COSTI = {
    "graduata": "la lettura, ed è il difetto che la gamma ha trovato a schermo: tre "
                "punte **in salita** non dicono «cresta», dicono **grafico di "
                "crescita**. Sta qui perché è l'idea di partenza nella sua forma "
                "diretta, e perché il confronto con la Centrata — stesse altezze, "
                "ordine diverso — è la cosa più utile di tutta la pagina.",
    "centrata": "la simmetria dell'ordine, che qualcuno chiamerà disordine. È la "
                "variante che risolve il difetto trovato a schermo — in salita si "
                "legge un grafico di crescita, non ordinate si legge un ritmo — e "
                "il prezzo è che la progressione non si «capisce» più a colpo "
                "d'occhio: va guardata, non letta.",
    "acuta": "la calma. Le valli fino alla base e le punte a spillo danno "
             "precisione e tolgono peso, ma spingono verso la fiamma e la corona "
             "di spine: è il registro più nervoso dei sei, e il meno adatto a "
             "stare in un header per otto ore di fila.",
    "lame": "tutto, e va detto senza girarci intorno. Staccando le punte in salita "
            "il disegno diventa **tre barre di altezza crescente**: cioè "
            "esattamente il marchio del template Hexagon che questa issue esiste "
            "per sostituire. Il gradiente ci lavora meglio che altrove, ma non "
            "compra niente contro quella somiglianza.",
    "contorno": "i 16 px, ed è il prezzo dichiarato in partenza. Il tratto respira "
                "e alle misure grandi è il più elegante della gamma; alla misura "
                "della favicon le valli larghe due unità e mezza si chiudono e "
                "resta una macchia con tre bozzi.",
    "sghemba": "la stabilità. L'inclinazione dà spinta e toglie ordine: accanto a "
               "un logotipo dritto la cresta pende, e in un lockup centrato "
               "quell'asse in più si nota. Guadagna in energia quello che perde "
               "in compostezza.",
    "bassa": "quasi tutto quello che la gamma stava cercando. È qui per isolare un "
             "asse solo — se anche graduata continua a leggersi «colline», allora "
             "il difetto era la proporzione e non la graduazione. Serve al "
             "confronto, non alla scelta.",
}


HTML = """<!doctype html>
<html lang="it"><head><meta charset="utf-8"><title>Postqron — sei direzioni</title>
<style>
  body {{ margin:0; padding:40px 48px 80px; background:#f7fafd; color:#1e3056;
         font:14px/1.55 -apple-system, system-ui, sans-serif; }}
  h1 {{ font-size:22px; margin:0 0 4px; }}
  h1 + p {{ margin:0 0 10px; color:#6f8ba4; max-width:78ch; }}
  .nota {{ margin:0 0 28px; color:#6f8ba4; max-width:78ch; }}
  h1 + p b, .nota b {{ color:#43536f; font-weight:600; }}
  h3 {{ margin:0 0 10px; font-size:17px; font-weight:600; }}
  h3 em {{ font-style:normal; font-weight:400; color:#6f8ba4; font-size:14px; }}
  h3 b {{ font-size:10px; letter-spacing:.09em; text-transform:uppercase; color:#6f8ba4;
          border:1px solid #dbe6fb; border-radius:100px; padding:3px 9px; vertical-align:2px;
          font-weight:600; }}
  .direzione {{ margin:0 0 44px; padding:24px 26px; background:#fff; border:1px solid #e2ebff;
                border-radius:14px; }}
  .direzione.consigliata {{ border-color:#4278e5; box-shadow:0 0 0 3px rgb(66 120 229 / 12%); }}
  h3 i {{ font-style:normal; font-size:10px; letter-spacing:.09em; text-transform:uppercase;
          color:#fff; background:#4278e5; border-radius:100px; padding:3px 10px;
          vertical-align:2px; font-weight:700; }}
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
<h1>Postqron — la gamma della cresta</h1>
<p>La direzione è decisa: il pettine da solo. Qui si cerca il disegno lungo cinque
assi — <b>peso</b>, <b>proporzioni</b>, <b>graduazione</b>, <b>terminali</b>,
<b>simmetria</b> — perché il primo pettine sbagliava tutti e cinque insieme:
pieno, largo, basso, con le punte alla stessa altezza. Il gradiente non è un asse
a parte: essendo ancorato alla griglia e non al singolo oggetto, attraversa il
disegno da sinistra a destra e le punte lo incontrano a tappe diverse per
costruzione.</p>
<p class="nota">Il <b>lockup viene per primo</b> in ogni riquadro: è lì che il
marchio vive il 90% delle volte, ed è lì che va giudicato. Le misure isolate —
48, 24 e 16 px, in colore, monocromatiche e invertite — vengono dopo.</p>
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
