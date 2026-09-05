"""Stateless embedding service shared by article indexing and search queries."""

from __future__ import annotations

import logging
import os
from contextlib import asynccontextmanager
from typing import Literal

import numpy as np
from fastapi import FastAPI, HTTPException
from fastembed import TextEmbedding
from pydantic import BaseModel, Field

MODEL_NAME = os.getenv("EMBED_MODEL", "BAAI/bge-small-en-v1.5")

# BGE expects this prefix on queries, but not documents.
QUERY_PREFIX = os.getenv(
    "EMBED_QUERY_PREFIX",
    "Represent this sentence for searching relevant passages: ",
)

# Guards against a single request pulling the whole corpus through the model.
MAX_BATCH = int(os.getenv("EMBED_MAX_BATCH", "128"))
MAX_CHARS = int(os.getenv("EMBED_MAX_CHARS", "5000"))

logger = logging.getLogger("embeddings")

_model: TextEmbedding | None = None
_dimensions: int = 0


@asynccontextmanager
async def lifespan(_app: FastAPI):
    # Load before accepting requests so health reflects model readiness.
    global _model, _dimensions
    logger.info("loading embedding model %s", MODEL_NAME)
    model = TextEmbedding(model_name=MODEL_NAME)
    probe = next(iter(model.embed(["dimension probe"])))
    _model, _dimensions = model, int(probe.shape[0])
    logger.info("model ready: %s (%d dimensions)", MODEL_NAME, _dimensions)
    yield
    _model, _dimensions = None, 0


app = FastAPI(title="Triangle CMS embeddings", lifespan=lifespan)


class EmbedRequest(BaseModel):
    texts: list[str] = Field(min_length=1)
    kind: Literal["query", "document"] = "document"


class EmbedResponse(BaseModel):
    model: str
    dimensions: int
    vectors: list[list[float]]


@app.get("/health")
def health() -> dict[str, object]:
    if _model is None:
        raise HTTPException(status_code=503, detail="model not loaded")
    return {"status": "ok", "model": MODEL_NAME, "dimensions": _dimensions}


@app.post("/embed", response_model=EmbedResponse)
def embed(request: EmbedRequest) -> EmbedResponse:
    if _model is None:
        raise HTTPException(status_code=503, detail="model not loaded")
    if len(request.texts) > MAX_BATCH:
        raise HTTPException(status_code=413, detail=f"batch exceeds {MAX_BATCH}")

    prefix = QUERY_PREFIX if request.kind == "query" else ""
    texts = [prefix + text[:MAX_CHARS] for text in request.texts]

    vectors = np.asarray(list(_model.embed(texts)), dtype=np.float32)

    # Unit vectors make MariaDB's Euclidean ranking agree with cosine similarity.
    norms = np.linalg.norm(vectors, axis=1, keepdims=True)
    vectors = vectors / np.clip(norms, 1e-12, None)

    return EmbedResponse(
        model=MODEL_NAME,
        dimensions=int(vectors.shape[1]),
        vectors=vectors.tolist(),
    )
