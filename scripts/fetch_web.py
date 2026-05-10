#!/usr/bin/env python3
"""
Fetch WEB translation from TehShrike/world-english-bible GitHub JSON files.
Writes to ./bibles/web/bookslug/chapter.md
"""

import json
import os
import re
import urllib.request
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE_DIR = os.path.join(os.path.dirname(__file__), "bibles", "web")
RAW_BASE = "https://raw.githubusercontent.com/TehShrike/world-english-bible/master/json"

BOOKS = [
    ("genesis",        "Genesis"),
    ("exodus",         "Exodus"),
    ("leviticus",      "Leviticus"),
    ("numbers",        "Numbers"),
    ("deuteronomy",    "Deuteronomy"),
    ("joshua",         "Joshua"),
    ("judges",         "Judges"),
    ("ruth",           "Ruth"),
    ("1samuel",        "1 Samuel"),
    ("2samuel",        "2 Samuel"),
    ("1kings",         "1 Kings"),
    ("2kings",         "2 Kings"),
    ("1chronicles",    "1 Chronicles"),
    ("2chronicles",    "2 Chronicles"),
    ("ezra",           "Ezra"),
    ("nehemiah",       "Nehemiah"),
    ("esther",         "Esther"),
    ("job",            "Job"),
    ("psalms",         "Psalms"),
    ("proverbs",       "Proverbs"),
    ("ecclesiastes",   "Ecclesiastes"),
    ("songofsolomon",  "Song of Solomon"),
    ("isaiah",         "Isaiah"),
    ("jeremiah",       "Jeremiah"),
    ("lamentations",   "Lamentations"),
    ("ezekiel",        "Ezekiel"),
    ("daniel",         "Daniel"),
    ("hosea",          "Hosea"),
    ("joel",           "Joel"),
    ("amos",           "Amos"),
    ("obadiah",        "Obadiah"),
    ("jonah",          "Jonah"),
    ("micah",          "Micah"),
    ("nahum",          "Nahum"),
    ("habakkuk",       "Habakkuk"),
    ("zephaniah",      "Zephaniah"),
    ("haggai",         "Haggai"),
    ("zechariah",      "Zechariah"),
    ("malachi",        "Malachi"),
    ("matthew",        "Matthew"),
    ("mark",           "Mark"),
    ("luke",           "Luke"),
    ("john",           "John"),
    ("acts",           "Acts"),
    ("romans",         "Romans"),
    ("1corinthians",   "1 Corinthians"),
    ("2corinthians",   "2 Corinthians"),
    ("galatians",      "Galatians"),
    ("ephesians",      "Ephesians"),
    ("philippians",    "Philippians"),
    ("colossians",     "Colossians"),
    ("1thessalonians", "1 Thessalonians"),
    ("2thessalonians", "2 Thessalonians"),
    ("1timothy",       "1 Timothy"),
    ("2timothy",       "2 Timothy"),
    ("titus",          "Titus"),
    ("philemon",       "Philemon"),
    ("hebrews",        "Hebrews"),
    ("james",          "James"),
    ("1peter",         "1 Peter"),
    ("2peter",         "2 Peter"),
    ("1john",          "1 John"),
    ("2john",          "2 John"),
    ("3john",          "3 John"),
    ("jude",           "Jude"),
    ("revelation",     "Revelation"),
]


def slugify(name: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")


def fetch_json(url: str) -> list:
    with urllib.request.urlopen(url, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def process_book(json_key: str, display_name: str) -> int:
    url = f"{RAW_BASE}/{json_key}.json"
    data = fetch_json(url)

    # Group text by (chapterNumber, verseNumber), preserving order of first appearance
    chapter_verses = defaultdict(lambda: defaultdict(list))
    chapter_order = []
    verse_order = defaultdict(list)

    for entry in data:
        t = entry.get("type", "")
        if t in ("paragraph text", "line text"):
            ch = entry["chapterNumber"]
            vn = entry["verseNumber"]
            val = entry["value"].strip()
            if not val:
                continue
            if ch not in chapter_order:
                chapter_order.append(ch)
            if vn not in verse_order[ch]:
                verse_order[ch].append(vn)
            chapter_verses[ch][vn].append(val)

    book_slug = slugify(display_name)
    files_written = 0

    for ch in chapter_order:
        lines = [f"# {display_name} {ch}\n"]
        for vn in verse_order[ch]:
            text = " ".join(chapter_verses[ch][vn])
            lines.append(f"**{vn}** {text}\n")
        content = "\n".join(lines)

        path = os.path.join(BASE_DIR, book_slug, f"{ch}.md")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        files_written += 1

    return files_written


def main():
    total = 0
    errors = []

    with ThreadPoolExecutor(max_workers=10) as executor:
        futures = {executor.submit(process_book, jk, dn): (jk, dn) for jk, dn in BOOKS}
        for future in as_completed(futures):
            jk, dn = futures[future]
            try:
                n = future.result()
                total += n
                print(f"  {dn}: {n} chapters")
            except Exception as e:
                errors.append((dn, e))
                print(f"  ERROR {dn}: {e}")

    print(f"\nDone — {total} chapter files written.")
    if errors:
        print(f"ERRORS ({len(errors)}):")
        for name, err in errors:
            print(f"  {name}: {err}")


if __name__ == "__main__":
    main()
