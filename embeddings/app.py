"""Embedding sidecar for the CMS.

The CMS needs vectors in two places that must agree: article bodies, embedded in
the background as they are published, and search queries, embedded on the
request path. A distance between vectors from two different models is
meaningless, so both go through this one service and it reports which model
produced them.

It is deliberately stateless. Nothing here is a source of truth (the vectors
live in MariaDB) so it needs no volume, no backup, and no reconciliation. If
it restarts, or is missing entirely, the CMS degrades to lexical search.
"""

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

# BGE is an asymmetric retrieval model: it was trained with short queries
# prefixed and documents bare. Embedding a query without this prefix quietly
# costs a chunk of retrieval quality: it still returns vectors, just worse
# ones, which is the kind of bug that never surfaces as an error.
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
    # Loading at startup rather than on first request means the container is
    # unhealthy while the model downloads, instead of appearing ready and then
    # timing out the first search that arrives.
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
    # "query" applies the BGE prefix, "document" does not. The caller has to say
    # which, because the service cannot tell a short article from a long query.
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

    # Normalize explicitly rather than trusting the model wrapper's default.
    # MariaDB ranks these with euclidean distance, which only agrees with cosine
    # similarity on unit vectors. The previous ETL skipped this, so magnitude
    # leaked into every "related articles" ranking.
    norms = np.linalg.norm(vectors, axis=1, keepdims=True)
    vectors = vectors / np.clip(norms, 1e-12, None)

    return EmbedResponse(
        model=MODEL_NAME,
        dimensions=int(vectors.shape[1]),
        vectors=vectors.tolist(),
    )
