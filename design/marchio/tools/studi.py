#!/usr/bin/env python3
"""
Banco di prova: genera `design/marchio/lab/studi.html`.

Serve a guardare gli studi di simbolo alle dimensioni in cui vivranno davvero —
16, 24, 48 px — in colore, monocromatico e invertito. I difetti che contano
(forme che si fondono, contrasti che spariscono, letture involontarie) non si
vedono nel codice.
"""
from __future__ import annotations

from pathlib import Path

from wordmark import wordmark

LAB = Path(__file__).resolve().parents[1] / "lab"

GRAD = """<linearGradient id="g-{id}" x1="0" y1="1" x2="1" y2="0">
  <stop offset="0" stop-color="#0fb4e5"/><stop offset="1" stop-color="#743fe5"/>
</linearGradient>"""

# --------------------------------------------------------------------- studi
# Ogni studio è markup SVG su griglia 32×32. `{paint}` viene sostituito dalla
# vernice: gradiente, nero pieno o bianco pieno.

STUDI: dict[str, tuple[str, str]] = {
    "a1": (
        "Indent — barre scalate",
        """<g fill="{paint}">
  <rect x="3" y="4" width="26" height="5" rx="2.5"/>
  <rect x="10" y="13.5" width="19" height="5" rx="2.5"/>
  <rect x="10" y="23" width="13" height="5" rx="2.5"/>
</g>""",
    ),
    "a2": (
        "Indent — trattino e valore",
        """<g fill="{paint}">
  <rect x="3" y="4" width="26" height="5" rx="2.5"/>
  <rect x="10" y="13.5" width="5" height="5" rx="2.5"/>
  <rect x="18" y="13.5" width="11" height="5" rx="2.5"/>
  <rect x="10" y="23" width="5" height="5" rx="2.5"/>
  <rect x="18" y="23" width="7" height="5" rx="2.5"/>
</g>""",
    ),
    "b1": (
        "q — occhiello tondo",
        """<g transform="translate(2.5 0)" fill="{paint}">
  <path d="M13.5 4.5a10.5 10.5 0 1 0 0 21 10.5 10.5 0 1 0 0-21Zm0 5a5.5 5.5 0 1 1 0 11 5.5 5.5 0 1 1 0-11Z"/>
  <rect x="19" y="4.5" width="5" height="25.5" rx="2.5"/>
</g>""",
    ),
    "b2": (
        "q — occhiello a blocco",
        """<g transform="translate(2.5 0)" fill="{paint}">
  <path d="M8 4h11a5 5 0 0 1 5 5v11a5 5 0 0 1-5 5H8a5 5 0 0 1-5-5V9a5 5 0 0 1 5-5Zm0 5a0 0 0 0 0 0 0v11a0 0 0 0 0 0 0h11a0 0 0 0 0 0 0V9a0 0 0 0 0 0 0Z"/>
  <rect x="19" y="4" width="5" height="26" rx="2.5"/>
</g>""",
    ),
    "b3": (
        "q — occhiello a blocco (anello)",
        """<g transform="translate(2.5 0)">
  <rect x="5.5" y="6.5" width="16" height="16" rx="5.5"
        fill="none" stroke="{paint}" stroke-width="5"/>
  <rect x="19" y="4" width="5" height="26" rx="2.5" fill="{paint}"/>
</g>""",
    ),
    "b4": (
        "q — la coda continua",
        """<g transform="translate(1 0)">
  <rect x="4.5" y="4.5" width="16" height="16" rx="5.5"
        fill="none" stroke="{paint}" stroke-width="5"/>
  <path d="M20 4.5v18.5a5 5 0 0 0 5 5h2.5" fill="none" stroke="{paint}"
        stroke-width="5" stroke-linecap="round"/>
</g>""",
    ),
    "c1": (
        "Tessera — indent in negativo",
        """<rect width="32" height="32" rx="8" fill="{paint}"/>
<g fill="{knock}">
  <rect x="7" y="8" width="18" height="4" rx="2"/>
  <rect x="12" y="15" width="13" height="4" rx="2"/>
  <rect x="12" y="22" width="8" height="4" rx="2"/>
</g>""",
    ),
    "c2": (
        "Tessera — q in negativo",
        """<rect width="32" height="32" rx="8" fill="{paint}"/>
<g fill="{knock}">
  <rect x="8.5" y="7.5" width="12" height="12" rx="4" fill="none"
        stroke="{knock}" stroke-width="4"/>
  <rect x="18.5" y="6" width="4" height="20" rx="2"/>
</g>""",
    ),
    "d1": (
        "Pq — monogramma rotazionale",
        """<g fill="none" stroke="{paint}" stroke-width="5" stroke-linecap="round">
  <path d="M9 28V6.5h3a5.5 5.5 0 0 1 0 11H9"/>
  <path d="M23 4v21.5h-3a5.5 5.5 0 0 1 0-11h3"/>
</g>""",
    ),
    "d2": (
        "Ramo — la revisione che rientra",
        """<g fill="none" stroke="{paint}" stroke-width="5" stroke-linecap="round"
   stroke-linejoin="round">
  <path d="M8 4v24"/>
  <path d="M8 11h9a7 7 0 0 1 0 14H8"/>
</g>""",
    ),

    "e1": (
        "q — blocco con righe di configurazione",
        """<g transform="translate(1.5 0)">
  <rect x="3" y="3" width="18" height="20" rx="6" fill="{paint}"/>
  <rect x="21" y="3" width="5" height="27" rx="2.5" fill="{paint}"/>
  <g fill="{knock}">
    <rect x="7" y="8" width="10" height="3.5" rx="1.75"/>
    <rect x="7" y="14.5" width="6.5" height="3.5" rx="1.75"/>
  </g>
</g>""",
    ),
    "e2": (
        "q — controforma a gradino",
        """<g transform="translate(2.5 0)">
  <path fill="{paint}" fill-rule="evenodd"
        d="M9 4h6a6 6 0 0 1 6 6v9a6 6 0 0 1-6 6H9a6 6 0 0 1-6-6v-9a6 6 0 0 1 6-6Zm-1 5.5h4.5V15H8Z"/>
  <rect x="19" y="4" width="5" height="26" rx="2.5" fill="{paint}"/>
</g>""",
    ),
    "e3": (
        "Asterisco — il jolly di cron",
        """<g stroke="{paint}" stroke-width="5" stroke-linecap="round">
  <path d="M16 16V5"/><path d="M16 16l10.5 3.4"/><path d="M16 16l-6.5 8.9"/>
  <path d="M16 16l6.5 8.9"/><path d="M16 16l-10.5 3.4"/>
</g>""",
    ),
    "e4": (
        "Guida di indentazione — rotaia e righe",
        """<g fill="{paint}">
  <rect x="3" y="4" width="5" height="24" rx="2.5"/>
  <rect x="12" y="5" width="17" height="5" rx="2.5"/>
  <rect x="12" y="13.5" width="11" height="5" rx="2.5"/>
  <rect x="12" y="22" width="14" height="5" rx="2.5"/>
</g>""",
    ),
    "e6": (
        "Versioni — due blocchi sfalsati",
        """<g transform="translate(1 0)">
  <rect x="8.5" y="2.5" width="16" height="16" rx="5" fill="none"
        stroke="{paint}" stroke-width="4"/>
  <rect x="2" y="10" width="17" height="16" rx="5" fill="{paint}"/>
</g>""",
    ),
    "e7": (
        "Tessera — chevron in negativo",
        """<rect width="32" height="32" rx="8" fill="{paint}"/>
<path d="M12 10l6 6-6 6" fill="none" stroke="{knock}" stroke-width="4.5"
      stroke-linecap="round" stroke-linejoin="round"/>""",
    ),
    "e8": (
        "q — occhiello tondo, controforma quadra",
        """<g transform="translate(2.5 0)">
  <path fill="{paint}" fill-rule="evenodd"
        d="M13.5 4.5a10.5 10.5 0 1 1 0 21 10.5 10.5 0 0 1 0-21Zm-5 8a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2h-6a2 2 0 0 1-2-2Z"/>
  <rect x="19" y="4.5" width="5" height="25.5" rx="2.5" fill="{paint}"/>
</g>""",
    ),
    "e9": (
        "pq — legatura speculare",
        """<g fill="none" stroke="{paint}" stroke-width="5" stroke-linecap="round">
  <path d="M5.5 28V9a5 5 0 0 1 5-5 5 5 0 0 1 5 5 5 5 0 0 1-5 5"/>
  <path d="M26.5 28V9a5 5 0 0 0-5-5 5 5 0 0 0-5 5 5 5 0 0 0 5 5"/>
</g>""",
    ),
    "e10": (
        "Gradino — l'indentazione in un tratto",
        """<path d="M5 6h8v10h8v10h6" fill="none" stroke="{paint}" stroke-width="5"
      stroke-linecap="round" stroke-linejoin="round"/>""",
    ),
}

SIZES = (48, 24, 16)


def symbol(key: str, markup: str, paint: str, knock: str) -> str:
    body = markup.format(paint=paint, knock=knock)
    return f'<svg viewBox="0 0 32 32" role="presentation"><defs>{GRAD.format(id=key)}</defs>{body}</svg>'


def riga(key: str, titolo: str, markup: str) -> str:
    celle = []
    for fondo, paint, knock in (
        ("chiaro", f"url(#g-{key})", "#fff"),
        ("chiaro", "#1e3056", "#fff"),
        ("scuro", "#fff", "#1e3056"),
    ):
        scale = "".join(
            f'<div class="px"><div style="width:{s}px">{symbol(key, markup, paint, knock)}</div>'
            f"<span>{s}</span></div>"
            for s in SIZES
        )
        celle.append(f'<div class="cella {fondo}">{scale}</div>')
    return f'<section class="studio"><h3>{titolo} <code>{key}</code></h3>{"".join(celle)}</section>'


def logotipi() -> str:
    blocchi = []
    for peso in (500, 600, 700):
        for tracking in (0, -12, -24):
            data = wordmark("Postqron", peso, tracking)
            altezza = 700  # cap height
            blocchi.append(
                f'<div class="logo"><svg viewBox="0 -{altezza} {data["advance"]} {altezza + 220}"'
                f' height="46"><path d="{data["d"]}" fill="#1e3056"/></svg>'
                f"<span>wght {peso:.0f} · tracking {tracking}</span></div>"
            )
    return "".join(blocchi)


HTML = """<!doctype html>
<html lang="it"><head><meta charset="utf-8"><title>Postqron — studi di marchio</title>
<style>
  body {{ margin:0; padding:40px; background:#f7fafd; color:#1e3056;
         font:14px/1.5 -apple-system, system-ui, sans-serif; }}
  h2 {{ margin:48px 0 16px; font-size:15px; letter-spacing:.08em; text-transform:uppercase;
        color:#6f8ba4; font-weight:600; }}
  h3 {{ margin:0 0 10px; font-size:14px; font-weight:600; }}
  code {{ color:#6f8ba4; font-weight:400; }}
  .studio {{ display:grid; grid-template-columns:repeat(3, max-content);
             gap:0 12px; align-items:start; margin-bottom:18px; }}
  .studio h3 {{ grid-column:1 / -1; }}
  .cella {{ display:flex; gap:18px; align-items:flex-end; padding:14px 18px;
            border-radius:10px; background:#fff; border:1px solid #e2ebff; }}
  .cella.scuro {{ background:#111a2e; border-color:#111a2e; color:#8ea6c4; }}
  .px {{ display:flex; flex-direction:column; align-items:center; gap:6px; }}
  .px span {{ font-size:10px; color:#8ea6c4; }}
  .cella svg {{ display:block; width:100%; height:auto; }}
  .logo {{ display:flex; flex-direction:column; gap:6px; margin-bottom:18px; }}
  .logo svg {{ display:block; width:auto; height:46px; }}
  .logo span {{ font-size:10px; color:#8ea6c4; }}
  .logotipi {{ display:grid; grid-template-columns:repeat(3, max-content); gap:20px 40px; }}
</style></head><body>
<h2>Simboli — 48 / 24 / 16 px · colore, monocromatico, invertito</h2>
{studi}
<h2>Logotipo — peso e crenatura</h2>
<div class="logotipi">{logotipi}</div>
</body></html>
"""


def main() -> None:
    LAB.mkdir(parents=True, exist_ok=True)
    studi = "".join(riga(k, t, m) for k, (t, m) in STUDI.items())
    (LAB / "studi.html").write_text(HTML.format(studi=studi, logotipi=logotipi()), "utf-8")
    print(LAB / "studi.html")


if __name__ == "__main__":
    main()
