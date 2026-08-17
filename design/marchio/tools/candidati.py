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
  <div class="scale">{scale}</div>
  <div class="lockups">{lockups}</div>
  {header}
  {tab}
</section>"""


COSTI = {
    "testa": "il registro, e i 16 px. È il disegno che il proprietario ha già "
             "visto: l'occhio si chiude sotto i 20 px e il tono resta quello del "
             "personaggio, non dello strumento. Sta qui come primo gradino della "
             "scala, per misurare gli altri.",
    "profilo": "la leggibilità alle misure piccole, che con una figura intera è "
               "il prezzo obbligato: coda, zampe e collo valgono un pixel a testa "
               "a 16 px e si impastano. In cambio è il grado con il registro "
               "migliore — la banderuola non è mai stata un personaggio.",
    "tratto": "la solidità. Un tratto aperto è elegante e fragile: su fondo "
              "gradiente perde contrasto, in stampa piccola si chiude, e a 16 px "
              "il vuoto interno della testa si riempie. Regge dove c'è spazio, "
              "non dove non ce n'è.",
    "geometrico": "un po' di gallo. Cerchi e triangoli lo rendono una costruzione "
                  "e non un ritratto: guadagna in registro e in parentela col "
                  "logotipo, e perde la vitalità del disegno a mano. È anche il "
                  "grado in cui il rischio «polleria» sparisce del tutto.",
    "cresta-becco": "la certezza. Senza testa, i due segni vanno letti insieme e "
                    "il becco rischia di diventare una freccia «play». Chi conosce "
                    "il nome vede il gallo; chi arriva freddo vede un segno, e "
                    "bisogna accettare che quella prima lettura non sia gratis.",
    "cresta": "il gallo, letteralmente. Restano tre gobbe che a 16 px non hanno "
              "niente da perdere e in un header non fanno ridere nessuno, ma da "
              "sole possono leggersi come una corona o come delle colline. "
              "Funziona se il nome lo si dice sempre accanto — cioè nel lockup, "
              "che è dove il marchio vive il 90% delle volte.",
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
<h1>Postqron — la gamma di riduzione del gallo</h1>
<p>Il concetto è confermato; qui si cerca il <b>grado di figurazione</b>. Dall'alto
in basso si scende dal disegno al segno: testa, profilo intero, tratto continuo,
costruzione geometrica, cresta con becco, cresta sola. L'ipotesi da verificare è
che l'occhio che si chiude a 16 px e il registro troppo simpatico siano lo stesso
difetto, e che cadano insieme scendendo la scala. Ognuna alle dimensioni in cui
vivrà: 48, 24 e 16 px, in colore, monocromatica su chiaro e invertita su scuro;
poi il lockup, l'header reale del sito e la scheda del browser a 16 px.</p>
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
