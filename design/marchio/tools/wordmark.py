#!/usr/bin/env python3
"""
Estrae il logotipo «Postqron» dagli outline di Quicksand come tracciato SVG.

Un logotipo non è testo composto: è un disegno. Convertirlo in tracciati toglie
la dipendenza dal font (l'SVG referenziato da `src`, la favicon e la card social
vivono in documenti isolati, senza @font-face) e permette le correzioni manuali
che un font non può conoscere — crenature proprie, discendente della q allungata.

Uso:  python3 wordmark.py --weight 600 --tracking -8
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

FONT = Path(__file__).resolve().parents[3] / "apps/web/assets/fonts/quicksand-latin.woff2"


def kerning(font: TTFont) -> dict[tuple[str, str], int]:
    """Coppie di crenatura dichiarate dal font, appiattite da GPOS."""
    pairs: dict[tuple[str, str], int] = {}
    gpos = font.get("GPOS")
    if gpos is None:
        return pairs
    for lookup in gpos.table.LookupList.Lookup:
        for sub in lookup.SubTable:
            if getattr(sub, "LookupType", lookup.LookupType) != 2:
                continue
            if sub.Format == 1:
                for first, set_ in zip(sub.Coverage.glyphs, sub.PairSet):
                    for record in set_.PairValueRecord:
                        value = getattr(record.Value1, "XAdvance", 0) or 0
                        if value:
                            pairs[(first, record.SecondGlyph)] = value
            elif sub.Format == 2:
                klass1 = sub.ClassDef1.classDefs
                klass2 = sub.ClassDef2.classDefs
                for first in sub.Coverage.glyphs:
                    c1 = klass1.get(first, 0)
                    for second, c2 in klass2.items():
                        record = sub.Class1Record[c1].Class2Record[c2]
                        value = getattr(record.Value1, "XAdvance", 0) or 0
                        if value:
                            pairs[(first, second)] = value
    return pairs


def wordmark(text: str, weight: float, tracking: float, upem_scale: float = 1.0) -> dict:
    font = TTFont(FONT)
    font = instancer.instantiateVariableFont(font, {"wght": weight}, inplace=True)
    upem = font["head"].unitsPerEm
    cmap = font.getBestCmap()
    glyphset = font.getGlyphSet()
    hmtx = font["hmtx"]
    kern = kerning(font)

    names = [cmap[ord(ch)] for ch in text]
    pen_out = SVGPathPen(glyphset, ntos=lambda v: f"{round(v, 2):g}")
    glifi = []
    x = 0.0
    for index, name in enumerate(names):
        if index:
            x += kern.get((names[index - 1], name), 0)
            x += tracking
        # y invertita: nel font cresce verso l'alto, in SVG verso il basso.
        pen = TransformPen(pen_out, (upem_scale, 0, 0, -upem_scale, x * upem_scale, 0))
        glyphset[name].draw(pen)
        bounds = BoundsPen(glyphset)
        glyphset[name].draw(bounds)
        glifi.append({
            "char": text[index],
            "x": round(x * upem_scale, 2),
            "advance": hmtx[name][0] * upem_scale,
            "bbox": [round(v * upem_scale, 2) for v in bounds.bounds] if bounds.bounds else None,
        })
        x += hmtx[name][0]

    metrics = font["OS/2"]
    return {
        "d": pen_out.getCommands(),
        "glifi": glifi,
        "advance": round(x * upem_scale, 2),
        "upem": upem,
        "capHeight": metrics.sCapHeight,
        "xHeight": metrics.sxHeight,
        "ascender": font["hhea"].ascent,
        "descender": font["hhea"].descent,
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--text", default="Postqron")
    parser.add_argument("--weight", type=float, default=600)
    parser.add_argument("--tracking", type=float, default=0)
    parser.add_argument("--scale", type=float, default=1.0)
    args = parser.parse_args()
    json.dump(wordmark(args.text, args.weight, args.tracking, args.scale), sys.stdout, indent=2)
    print()
