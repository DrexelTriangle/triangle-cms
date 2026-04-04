#!/usr/bin/env python3
"""Generate MariaDB vector embedding bootstrap SQL from ETL JSON output."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input-json", required=True, help="Path to article_output.json")
    parser.add_argument("--out-sql", required=True, help="Output SQL file path")
    parser.add_argument(
        "--model",
        default="sentence-transformers/paraphrase-MiniLM-L3-v2",
        help="SentenceTransformer model name (small/fast by default)",
    )
    parser.add_argument(
        "--batch-size",
        type=int,
        default=64,
        help="Embedding batch size",
    )
    parser.add_argument(
        "--max-chars",
        type=int,
        default=5000,
        help="Maximum characters per document before embedding",
    )
    return parser.parse_args()


def strip_html(text: str) -> str:
    cleaned = re.sub(r"<[^>]+>", " ", text)
    cleaned = re.sub(r"\s+", " ", cleaned)
    return cleaned.strip()


def normalize_articles(payload: Any) -> list[dict[str, Any]]:
    if isinstance(payload, dict):
        candidates = list(payload.values())
    elif isinstance(payload, list):
        candidates = payload
    else:
        return []

    normalized: list[dict[str, Any]] = []
    for row in candidates:
        if not isinstance(row, dict):
            continue
        article_id = row.get("id")
        if article_id is None:
            continue
        try:
            article_id = int(article_id)
        except (TypeError, ValueError):
            continue

        title = str(row.get("title") or "").strip()
        description = str(row.get("description") or "").strip()
        text = str(row.get("text") or "").strip()
        blob = "\n\n".join(part for part in (title, description, strip_html(text)) if part)
        if not blob:
            continue

        normalized.append({"id": article_id, "text": blob})

    normalized.sort(key=lambda x: x["id"])
    return normalized


def vec_to_text(values: list[float]) -> str:
    return "[" + ",".join(f"{v:.8f}" for v in values) + "]"


def main() -> int:
    args = parse_args()

    try:
        from sentence_transformers import SentenceTransformer
    except Exception:
        print(
            "Missing dependency: sentence-transformers. Install with:\n"
            "  pip install sentence-transformers",
            file=sys.stderr,
        )
        return 1

    input_path = Path(args.input_json)
    out_path = Path(args.out_sql)

    with input_path.open("r", encoding="utf-8") as handle:
        payload = json.load(handle)

    articles = normalize_articles(payload)
    out_path.parent.mkdir(parents=True, exist_ok=True)

    if not articles:
        with out_path.open("w", encoding="utf-8") as handle:
            handle.write("DROP TABLE IF EXISTS article_embeddings;\n")
            handle.write("-- No articles to embed.\n")
        return 0

    model = SentenceTransformer(args.model, device="cpu")
    corpus = [row["text"][: args.max_chars] for row in articles]
    vectors = model.encode(
        corpus,
        batch_size=args.batch_size,
        convert_to_numpy=True,
        normalize_embeddings=False,
        show_progress_bar=True,
    )
    dim = int(vectors.shape[1])

    with out_path.open("w", encoding="utf-8") as handle:
        handle.write("DROP TABLE IF EXISTS article_embeddings;\n")
        handle.write(
            f"CREATE TABLE article_embeddings (\n"
            f"  article_id BIGINT PRIMARY KEY,\n"
            f"  embedding VECTOR({dim}) NOT NULL,\n"
            f"  VECTOR INDEX (embedding) DISTANCE=euclidean\n"
            f");\n"
        )
        for row, vec in zip(articles, vectors, strict=True):
            vector_text = vec_to_text(vec.tolist())
            handle.write(
                "INSERT INTO article_embeddings (article_id, embedding) VALUES "
                f"({row['id']}, VEC_FromText('{vector_text}'));\n"
            )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
