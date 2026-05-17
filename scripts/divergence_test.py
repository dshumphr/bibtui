#!/usr/bin/env python3
"""
Generate translation-divergence annotations for bibtui.

Encodes every verse across all available translations using a sentence
embedder, scores pairwise cosine divergence, and emits an annotation-group
JSON file with a human-readable blurb for each verse above the cutoff.
"""

import json, math, os, re, sys
from collections import defaultdict
from pathlib import Path

import numpy as np
from sentence_transformers import SentenceTransformer

# ── paths ──────────────────────────────────────────────────────────────────

BIBLES = Path("bibles")
OUTFILE = Path("divergence.json")

# ── book canon ─────────────────────────────────────────────────────────────

BOOKS = [
    ("genesis","Genesis"),("exodus","Exodus"),("leviticus","Leviticus"),
    ("numbers","Numbers"),("deuteronomy","Deuteronomy"),("joshua","Joshua"),
    ("judges","Judges"),("ruth","Ruth"),("1-samuel","1 Samuel"),
    ("2-samuel","2 Samuel"),("1-kings","1 Kings"),("2-kings","2 Kings"),
    ("1-chronicles","1 Chronicles"),("2-chronicles","2 Chronicles"),
    ("ezra","Ezra"),("nehemiah","Nehemiah"),("esther","Esther"),
    ("job","Job"),("psalms","Psalms"),("proverbs","Proverbs"),
    ("ecclesiastes","Ecclesiastes"),("song-of-solomon","Song of Solomon"),
    ("isaiah","Isaiah"),("jeremiah","Jeremiah"),("lamentations","Lamentations"),
    ("ezekiel","Ezekiel"),("daniel","Daniel"),("hosea","Hosea"),
    ("joel","Joel"),("amos","Amos"),("obadiah","Obadiah"),
    ("jonah","Jonah"),("micah","Micah"),("nahum","Nahum"),
    ("habakkuk","Habakkuk"),("zephaniah","Zephaniah"),("haggai","Haggai"),
    ("zechariah","Zechariah"),("malachi","Malachi"),("matthew","Matthew"),
    ("mark","Mark"),("luke","Luke"),("john","John"),("acts","Acts"),
    ("romans","Romans"),("1-corinthians","1 Corinthians"),
    ("2-corinthians","2 Corinthians"),("galatians","Galatians"),
    ("ephesians","Ephesians"),("philippians","Philippians"),
    ("colossians","Colossians"),("1-thessalonians","1 Thessalonians"),
    ("2-thessalonians","2 Thessalonians"),("1-timothy","1 Timothy"),
    ("2-timothy","2 Timothy"),("titus","Titus"),("philemon","Philemon"),
    ("hebrews","Hebrews"),("james","James"),("1-peter","1 Peter"),
    ("2-peter","2 Peter"),("1-john","1 John"),("2-john","2 John"),
    ("3-john","3 John"),("jude","Jude"),("revelation","Revelation"),
]

VERSE_RE = re.compile(r"^\*\*(\d+)\*\* (.+)")
NON_ALPHA = re.compile(r"[^a-z\s']")

STOPWORDS = {
    "a","an","the","and","or","but","in","on","at","to","for","of","with",
    "by","from","is","are","was","were","be","been","being","have","has",
    "had","do","does","did","will","would","shall","should","may","might",
    "can","could","it","its","he","him","his","she","her","they","them",
    "their","we","us","our","you","your","i","me","my","this","that",
    "these","those","who","whom","which","what","not","no","so","if","as",
    "into","also","then","than","all","each","every","own","there","here",
    "where","when","how",
}

ARCHAIC = {
    "ye":"you","thee":"you","thou":"you","thy":"your","thine":"your",
    "hath":"has","doth":"does","shalt":"shall","wilt":"will","art":"are",
    "dost":"do","saith":"says","unto":"to","wherefore":"therefore",
    "whence":"where","hither":"here","thither":"there",
    "brethren":"brothers","howbeit":"however","yea":"yes",
}

# ── helpers ─────────────────────────────────────────────────────────────────

def parse_chapter(translation, slug, ch):
    p = BIBLES / translation / slug / f"{ch}.md"
    if not p.exists():
        return {}
    out = {}
    for line in p.read_text().splitlines():
        m = VERSE_RE.match(line.strip())
        if m:
            out[int(m.group(1))] = m.group(2)
    return out


def cosine_sim(a, b):
    d = np.dot(a, b)
    n = np.linalg.norm(a) * np.linalg.norm(b)
    return float(d / n) if n > 1e-9 else 0.0


def tokenize(text):
    text = NON_ALPHA.sub("", text.lower())
    out = []
    for w in text.split():
        w = ARCHAIC.get(w, w)
        if w.endswith("eth") and len(w) > 4:
            w = w[:-3] + "s"
        if w not in STOPWORDS:
            out.append(w)
    return out


def diff_words(texts_by_tr):
    token_sets = {tr: set(tokenize(t)) for tr, t in texts_by_tr.items()}
    common = set.intersection(*token_sets.values()) if token_sets else set()
    per_tr = {}
    for tr, toks in token_sets.items():
        unique = toks - common
        for other_tr, other_toks in token_sets.items():
            if other_tr != tr:
                unique -= other_toks
        per_tr[tr] = unique
    return common, per_tr


def make_blurb(texts_by_tr, divergence, min_sim, translations):
    _, unique_per_tr = diff_words(texts_by_tr)

    pairs = []
    for tr, words in sorted(unique_per_tr.items()):
        if words:
            top = sorted(words)[:4]
            pairs.append(f"{tr.upper()} uses \"{', '.join(top)}\"")

    if not pairs:
        all_toks = {tr: set(tokenize(t)) for tr, t in texts_by_tr.items()}
        all_words = set()
        for toks in all_toks.values():
            all_words |= toks
        shared = set.intersection(*all_toks.values())
        diff = all_words - shared
        if diff:
            pairs.append(f"translations diverge on: {', '.join(sorted(diff)[:5])}")
        else:
            pairs.append("phrasing and word order differ across translations")

    severity = "Significant" if divergence >= 0.40 else "Notable" if divergence >= 0.30 else "Moderate"
    blurb = f"{severity} translation divergence. {'; '.join(pairs)}."
    return blurb


# ── main ────────────────────────────────────────────────────────────────────

def main():
    translations = sorted(d.name for d in BIBLES.iterdir() if d.is_dir())
    print(f"Translations: {translations}", flush=True)

    print("Loading model...", flush=True)
    model = SentenceTransformer("all-MiniLM-L6-v2")

    # Collect all verses
    print("Parsing verses...", flush=True)
    verses = []  # (slug, name, ch, vnum, {tr: text})
    for slug, name in BOOKS:
        for ch in range(1, 151):
            by_tr = {}
            for tr in translations:
                vs = parse_chapter(tr, slug, ch)
                if vs:
                    by_tr[tr] = vs
            if len(by_tr) < 2:
                continue
            all_vnums = set()
            for vs in by_tr.values():
                all_vnums.update(vs.keys())
            for vn in sorted(all_vnums):
                texts = {tr: by_tr[tr][vn] for tr in by_tr if vn in by_tr[tr]}
                if len(texts) >= 2:
                    verses.append((slug, name, ch, vn, texts))

    print(f"Total verses: {len(verses)}", flush=True)

    # Batch encode
    print("Encoding...", flush=True)
    flat_sents = []
    flat_index = []
    for vi, (slug, name, ch, vn, texts) in enumerate(verses):
        for tr in translations:
            if tr in texts:
                flat_sents.append(texts[tr])
                flat_index.append((vi, tr))

    print(f"Sentences: {len(flat_sents)}", flush=True)
    all_embs = model.encode(flat_sents, batch_size=256, show_progress_bar=True)

    verse_embs = defaultdict(dict)
    for idx, (vi, tr) in enumerate(flat_index):
        verse_embs[vi][tr] = all_embs[idx]

    # Score
    print("Scoring divergence...", flush=True)
    scored = []
    divs = []
    for vi, (slug, name, ch, vn, texts) in enumerate(verses):
        embs = verse_embs[vi]
        trs = [tr for tr in translations if tr in embs]
        sims = []
        for i in range(len(trs)):
            for j in range(i + 1, len(trs)):
                sims.append(cosine_sim(embs[trs[i]], embs[trs[j]]))
        avg_sim = float(np.mean(sims))
        min_sim = float(np.min(sims))
        div = 1.0 - avg_sim
        divs.append(div)
        scored.append((vi, slug, name, ch, vn, texts, div, avg_sim, min_sim))

    divs_arr = np.array(divs)

    # ── Distribution analysis ───────────────────────────────────────────────
    print("\n=== DIVERGENCE DISTRIBUTION ===\n")
    percentiles = [50, 75, 80, 85, 90, 93, 95, 97, 99]
    for p in percentiles:
        val = np.percentile(divs_arr, p)
        count = int(np.sum(divs_arr >= val))
        print(f"  p{p:2d} = {val:.4f}  ({count:5d} verses above)")

    print(f"\n  mean   = {np.mean(divs_arr):.4f}")
    print(f"  median = {np.median(divs_arr):.4f}")
    print(f"  stddev = {np.std(divs_arr):.4f}")
    print(f"  max    = {np.max(divs_arr):.4f}")

    # ── Threshold selection ─────────────────────────────────────────────────
    # Intensity 3: top ~2%   (p98)  — strong divergence
    # Intensity 2: next ~5%  (p93-p98) — moderate
    # Intensity 1: next ~7%  (p86-p93) — subtle
    # Below p86: not annotated
    #
    # This gives roughly 14% of verses annotated, which is meaningful
    # but not overwhelming.

    t3 = float(np.percentile(divs_arr, 98))
    t2 = float(np.percentile(divs_arr, 93))
    t1 = float(np.percentile(divs_arr, 86))

    print(f"\n=== SELECTED THRESHOLDS ===\n")
    n3 = int(np.sum(divs_arr >= t3))
    n2 = int(np.sum((divs_arr >= t2) & (divs_arr < t3)))
    n1 = int(np.sum((divs_arr >= t1) & (divs_arr < t2)))
    print(f"  Intensity 3 (div >= {t3:.4f}): {n3:5d} verses")
    print(f"  Intensity 2 (div >= {t2:.4f}): {n2:5d} verses")
    print(f"  Intensity 1 (div >= {t1:.4f}): {n1:5d} verses")
    print(f"  Total annotated:               {n1+n2+n3:5d} verses ({(n1+n2+n3)/len(verses)*100:.1f}%)")

    # ── Generate annotations ────────────────────────────────────────────────
    print("\nGenerating annotations...", flush=True)
    annotations = []
    for vi, slug, name, ch, vn, texts, div, avg_sim, min_sim in scored:
        if div < t1:
            continue
        if div >= t3:
            intensity = 3
        elif div >= t2:
            intensity = 2
        else:
            intensity = 1

        blurb = make_blurb(texts, div, min_sim, translations)
        annotations.append({
            "ref": {"book": slug, "chapter": ch, "verse": vn},
            "text": blurb,
            "intensity": intensity,
        })

    annotations.sort(key=lambda a: (a["ref"]["book"], a["ref"]["chapter"], a["ref"]["verse"]))

    out = {
        "groups": {
            "divergence": {
                "name": "divergence",
                "color": "#CC6666",
                "annotations": annotations,
            }
        }
    }

    OUTFILE.write_text(json.dumps(out, indent=2))
    print(f"\nWrote {len(annotations)} annotations to {OUTFILE}")

    # ── Samples ─────────────────────────────────────────────────────────────
    scored.sort(key=lambda x: x[6], reverse=True)

    print("\n=== SAMPLE: Top 20 (intensity 3) ===\n")
    for vi, slug, name, ch, vn, texts, div, avg_sim, min_sim in scored[:20]:
        blurb = make_blurb(texts, div, min_sim, translations)
        print(f"  {name} {ch}:{vn}  div={div:.4f}")
        for tr in translations:
            if tr in texts:
                t = texts[tr][:120] + "..." if len(texts[tr]) > 120 else texts[tr]
                print(f"    [{tr}] {t}")
        print(f"    >> {blurb}")
        print()

    print("=== SAMPLE: Intensity 2 boundary ===\n")
    around_t2 = [s for s in scored if abs(s[6] - t2) < 0.005][:10]
    for vi, slug, name, ch, vn, texts, div, avg_sim, min_sim in around_t2:
        blurb = make_blurb(texts, div, min_sim, translations)
        print(f"  {name} {ch}:{vn}  div={div:.4f}")
        for tr in translations:
            if tr in texts:
                t = texts[tr][:120] + "..." if len(texts[tr]) > 120 else texts[tr]
                print(f"    [{tr}] {t}")
        print(f"    >> {blurb}")
        print()

    print("=== SAMPLE: Intensity 1 boundary (near cutoff) ===\n")
    around_t1 = [s for s in scored if abs(s[6] - t1) < 0.003][:10]
    for vi, slug, name, ch, vn, texts, div, avg_sim, min_sim in around_t1:
        blurb = make_blurb(texts, div, min_sim, translations)
        print(f"  {name} {ch}:{vn}  div={div:.4f}")
        for tr in translations:
            if tr in texts:
                t = texts[tr][:120] + "..." if len(texts[tr]) > 120 else texts[tr]
                print(f"    [{tr}] {t}")
        print(f"    >> {blurb}")
        print()


if __name__ == "__main__":
    main()
