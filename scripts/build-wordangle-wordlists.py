#!/usr/bin/env python3
'''Download SCOWL and write Wordangle's guess dictionaries.

Writes server/internal/wordangle/allowed-{2..6}.txt plus SCOWL's
copyright notice, which its license requires to travel with the lists.
targets.txt, the possible answers, is hand-maintained and left alone.

The CMS embeds these files and serves them to the public site at
/v1/wordangle/lists/<file>; Scalene keeps no copy. The output is
committed, so production never downloads anything; rerun this only to
refresh the lists:

    python3 scripts/build-wordangle-wordlists.py
'''

from __future__ import annotations

import re
import sys
import tempfile
import time
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / 'server' / 'internal' / 'wordangle'

SCOWL_URL = (
    'https://downloads.sourceforge.net/project/wordlist/'
    'SCOWL/2020.12.07/scowl-2020.12.07.zip'
)
SCOWL_SPELLINGS = frozenset({
    'english', 'american', 'british', 'british_z',
})
SCOWL_SIZE = 80
SCOWL_WORDS_RE = re.compile(
    r'(?:^|/)final/(\w+)-words\.(\d\d)$'
)

USER_AGENT = 'wordangle-wordlist-builder/1.0'
ALLOWED_LENGTHS = (2, 3, 4, 5, 6)
DOWNLOAD_ATTEMPTS = 5


def main() -> None:
    DATA.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory() as tmp:
        tmp_dir = Path(tmp)
        scowl_zip = tmp_dir / 'scowl-2020.12.07.zip'
        download(SCOWL_URL, scowl_zip)

        allowed = {length: set() for length in ALLOWED_LENGTHS}
        for word in read_scowl_words(scowl_zip):
            if len(word) in allowed:
                allowed[len(word)].add(word)

        with zipfile.ZipFile(scowl_zip) as archive:
            (DATA / 'SCOWL-Copyright.txt').write_bytes(
                archive.read('Copyright'),
            )

    for length in ALLOWED_LENGTHS:
        path = DATA / f'allowed-{length}.txt'
        write_words(path, allowed[length])

    for length in ALLOWED_LENGTHS:
        print(f'allowed-{length} {len(allowed[length])}')


def parse_word(line: str) -> str | None:
    word = line.strip().lower()
    if word.isascii() and word.isalpha():
        return word
    return None


def read_scowl_words(zip_path: Path) -> set[str]:
    words: set[str] = set()
    with zipfile.ZipFile(zip_path) as archive:
        for name in archive.namelist():
            match = SCOWL_WORDS_RE.search(name.replace('\\', '/'))
            if match is None:
                continue
            spelling = match.group(1)
            size = int(match.group(2))
            if spelling not in SCOWL_SPELLINGS or size > SCOWL_SIZE:
                continue
            text = archive.read(name).decode('latin-1')
            words.update(words_from_text(text))
    if not words:
        raise RuntimeError(f'no SCOWL words in {zip_path}')
    return words


def words_from_text(text: str) -> set[str]:
    words: set[str] = set()
    for line in text.splitlines():
        word = parse_word(line)
        if word is not None:
            words.add(word)
    return words


def write_words(path: Path, words: set[str]) -> None:
    text = '\n'.join(sorted(words))
    if text:
        text += '\n'
    path.write_text(text, encoding='ascii')


def download(url: str, dest: Path) -> None:
    last_error: Exception | None = None
    for attempt in range(DOWNLOAD_ATTEMPTS):
        try:
            print(f'download {dest.name} ({attempt + 1})', flush=True)
            fetch_to(url, dest)
            return
        except Exception as error:
            last_error = error
            if dest.exists():
                dest.unlink()
            time.sleep(2 ** attempt)
    raise RuntimeError(f'could not download {url}') from last_error


def fetch_to(url: str, dest: Path) -> None:
    request = urllib.request.Request(
        url, headers={'User-Agent': USER_AGENT},
    )
    with urllib.request.urlopen(request, timeout=120) as response:
        dest.write_bytes(response.read())


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(error, file=sys.stderr)
        sys.exit(1)
