#!/usr/bin/env python3
"""
Download KJV, WEB, and BSB Bible translations and organize them into
./bibles/lettercode/book/chapter.md

- KJV, BSB: from scrollmapper/bible_databases GitHub JSON files
- WEB: from bible-api.com (the WEB is their native/default translation)
"""

import json
import os
import re
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE_DIR = os.path.join(os.path.dirname(__file__), "bibles")

# Scrollmapper-format translations
SCROLLMAPPER_TRANSLATIONS = {
    "kjv": "https://raw.githubusercontent.com/scrollmapper/bible_databases/master/formats/json/KJV.json",
    "bsb": "https://raw.githubusercontent.com/scrollmapper/bible_databases/master/formats/json/BSB.json",
}

# Bible-api.com supports WEB as its default translation
BIBLE_API_TRANSLATION = "web"
BIBLE_API_BASE = "https://bible-api.com"

# Standard 66-book Bible book names (used for WEB fetching)
BIBLE_BOOKS = [
    ("Genesis", 50), ("Exodus", 40), ("Leviticus", 27), ("Numbers", 36),
    ("Deuteronomy", 34), ("Joshua", 24), ("Judges", 21), ("Ruth", 4),
    ("1 Samuel", 31), ("2 Samuel", 24), ("1 Kings", 22), ("2 Kings", 25),
    ("1 Chronicles", 29), ("2 Chronicles", 36), ("Ezra", 10), ("Nehemiah", 13),
    ("Esther", 10), ("Job", 42), ("Psalms", 150), ("Proverbs", 31),
    ("Ecclesiastes", 12), ("Song of Solomon", 8), ("Isaiah", 66),
    ("Jeremiah", 52), ("Lamentations", 5), ("Ezekiel", 48), ("Daniel", 12),
    ("Hosea", 14), ("Joel", 3), ("Amos", 9), ("Obadiah", 1), ("Jonah", 4),
    ("Micah", 7), ("Nahum", 3), ("Habakkuk", 3), ("Zephaniah", 3),
    ("Haggai", 2), ("Zechariah", 14), ("Malachi", 4),
    ("Matthew", 28), ("Mark", 16), ("Luke", 24), ("John", 21),
    ("Acts", 28), ("Romans", 16), ("1 Corinthians", 16), ("2 Corinthians", 13),
    ("Galatians", 6), ("Ephesians", 6), ("Philippians", 4), ("Colossians", 4),
    ("1 Thessalonians", 5), ("2 Thessalonians", 3), ("1 Timothy", 6),
    ("2 Timothy", 4), ("Titus", 3), ("Philemon", 1), ("Hebrews", 13),
    ("James", 5), ("1 Peter", 5), ("2 Peter", 3), ("1 John", 5),
    ("2 John", 1), ("3 John", 1), ("Jude", 1), ("Revelation", 22),
]


def slugify(name: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")


def write_chapter(path: str, book_name: str, chapter_num: int, verses: list):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    lines = [f"# {book_name} {chapter_num}\n"]
    for v in verses:
        verse_num = v["verse"]
        text = v["text"].strip()
        lines.append(f"**{verse_num}** {text}\n")
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(lines))


def fetch_json(url: str, retries: int = 3) -> dict:
    for attempt in range(retries):
        try:
            with urllib.request.urlopen(url, timeout=30) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except Exception as e:
            if attempt < retries - 1:
                time.sleep(1 + attempt)
            else:
                raise e


def process_scrollmapper(code: str, url: str):
    print(f"\nProcessing {code.upper()} (scrollmapper) ...")
    data = fetch_json(url)
    books = data["books"]
    total_files = 0
    for book in books:
        book_name = book["name"]
        book_slug = slugify(book_name)
        for chapter in book["chapters"]:
            chapter_num = chapter["chapter"]
            verses = chapter["verses"]
            path = os.path.join(BASE_DIR, code, book_slug, f"{chapter_num}.md")
            write_chapter(path, book_name, chapter_num, verses)
            total_files += 1
    print(f"  Done — wrote {total_files} chapter files for {code.upper()}.")


def fetch_web_chapter(book_name: str, chapter_num: int) -> tuple:
    """Fetch a single chapter from bible-api.com and return (book_name, chapter_num, verses)."""
    # Bible-api.com URL format: /book+chapter?translation=web
    book_url_part = book_name.replace(" ", "+")
    url = f"{BIBLE_API_BASE}/{book_url_part}+{chapter_num}?translation=web"
    data = fetch_json(url)
    verses = [{"verse": v["verse"], "text": v["text"]} for v in data["verses"]]
    return (book_name, chapter_num, verses)


def process_web():
    print(f"\nProcessing WEB (bible-api.com) with concurrent fetching ...")
    tasks = []
    for book_name, num_chapters in BIBLE_BOOKS:
        for ch in range(1, num_chapters + 1):
            tasks.append((book_name, ch))

    total = len(tasks)
    completed = 0
    failed = []

    with ThreadPoolExecutor(max_workers=15) as executor:
        future_to_task = {executor.submit(fetch_web_chapter, bn, ch): (bn, ch) for bn, ch in tasks}
        for future in as_completed(future_to_task):
            task = future_to_task[future]
            try:
                book_name, chapter_num, verses = future.result()
                book_slug = slugify(book_name)
                path = os.path.join(BASE_DIR, "web", book_slug, f"{chapter_num}.md")
                write_chapter(path, book_name, chapter_num, verses)
                completed += 1
                if completed % 100 == 0:
                    print(f"  WEB: {completed}/{total} chapters done ...")
            except Exception as e:
                failed.append(task)
                print(f"  ERROR fetching {task}: {e}")

    if failed:
        print(f"  WARNING: {len(failed)} chapters failed. Retrying ...")
        for book_name, chapter_num in failed:
            try:
                _, _, verses = fetch_web_chapter(book_name, chapter_num)
                book_slug = slugify(book_name)
                path = os.path.join(BASE_DIR, "web", book_slug, f"{chapter_num}.md")
                write_chapter(path, book_name, chapter_num, verses)
                completed += 1
            except Exception as e:
                print(f"  FAILED permanently: {book_name} {chapter_num}: {e}")

    print(f"  Done — wrote {completed} chapter files for WEB.")


if __name__ == "__main__":
    # KJV and BSB are already downloaded (KJV done, BSB pending)
    # Uncomment to re-run:
    # for code, url in SCROLLMAPPER_TRANSLATIONS.items():
    #     process_scrollmapper(code, url)

    # Only run BSB (KJV already done from previous run)
    process_scrollmapper("bsb", SCROLLMAPPER_TRANSLATIONS["bsb"])

    # Fetch WEB
    process_web()

    print("\nAll translations downloaded and organized.")
