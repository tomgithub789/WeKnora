#!/usr/bin/env python3
"""Repair missing normal embeddings without reparsing WeKnora documents.

The script is deliberately narrow and insert-first:

* it selects active text chunks that have no source_type=0 embedding;
* it requires each selected chunk to have one 1024-dimensional, zero-valued
  source_type=1 legacy placeholder;
* it asks WeKnora's saved model debug endpoint to embed the exact v0.7.1 index
  input (trimmed document title, newline, trimmed chunk content);
* it inserts all normal embeddings in one guarded PostgreSQL transaction.

It never updates chunks, parse status, Wiki pages, graph data, or legacy rows.
Run without --apply for a read-only invariant check.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import json
import math
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any


DEFAULT_KB_ID = "b8ce52da-c01a-4bfb-99cf-46108fe6f9a5"
DEFAULT_MODEL_ID = "e3b89a8f-2643-4aa2-a1e2-1725d1b3416b"
DEFAULT_API_BASE_URL = "http://127.0.0.1:8080"
UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


@dataclass(frozen=True)
class Target:
    legacy_id: int
    chunk_id: str
    knowledge_id: str
    knowledge_base_id: str
    title: str
    content: str
    is_enabled: bool
    tag_id: str | None
    title_md5: str
    content_md5: str

    @property
    def index_content(self) -> str:
        title = self.title.strip()
        body = self.content.strip()
        return f"{title}\n{body}" if title else body

    @property
    def index_content_md5(self) -> str:
        return hashlib.md5(self.index_content.encode("utf-8")).hexdigest()  # noqa: S324


def run(command: list[str], *, stdin: str | None = None) -> str:
    completed = subprocess.run(
        command,
        input=stdin,
        text=True,
        capture_output=True,
        check=False,
    )
    if completed.returncode != 0:
        detail = completed.stderr.strip() or completed.stdout.strip()
        raise RuntimeError(f"command failed ({completed.returncode}): {detail}")
    return completed.stdout


def psql_scalar(sql: str, postgres_container: str) -> str:
    return run(
        [
            "docker",
            "exec",
            "-i",
            postgres_container,
            "psql",
            "-U",
            "weknora",
            "-d",
            "weknora",
            "-v",
            "ON_ERROR_STOP=1",
            "-At",
        ],
        stdin=sql,
    ).strip()


def load_targets(kb_id: str, postgres_container: str) -> list[Target]:
    if not UUID_RE.fullmatch(kb_id):
        raise ValueError("knowledge-base id is not a UUID")
    sql = f"""
SELECT coalesce(json_agg(json_build_object(
    'legacy_id', e1.id,
    'chunk_id', c.id,
    'knowledge_id', c.knowledge_id,
    'knowledge_base_id', c.knowledge_base_id,
    'title', coalesce(k.title, ''),
    'content', c.content,
    'is_enabled', c.is_enabled,
    'tag_id', c.tag_id,
    'title_md5', md5(coalesce(k.title, '')),
    'content_md5', md5(c.content)
) ORDER BY c.id), '[]'::json)::text
FROM chunks c
JOIN knowledges k ON k.id = c.knowledge_id
LEFT JOIN embeddings e0
  ON e0.source_id = c.id AND e0.source_type = 0
JOIN embeddings e1
  ON e1.source_id = c.id AND e1.source_type = 1
WHERE k.knowledge_base_id = '{kb_id}'
  AND k.deleted_at IS NULL
  AND c.deleted_at IS NULL
  AND c.chunk_type = 'text'
  AND e0.id IS NULL
  AND e1.dimension = 1024
  AND e1.embedding::text ~ '^\\[0(\\.0+)?(,0(\\.0+)?)*\\]$';
"""
    payload = json.loads(psql_scalar(sql, postgres_container))
    targets = [Target(**item) for item in payload]
    for target in targets:
        if not UUID_RE.fullmatch(target.chunk_id):
            raise ValueError("chunk id is not a UUID")
        if not UUID_RE.fullmatch(target.knowledge_id):
            raise ValueError("knowledge id is not a UUID")
        if target.knowledge_base_id != kb_id:
            raise ValueError("target belongs to another knowledge base")
    return targets


def get_api_key() -> str:
    api_key = run(["weknora", "auth", "token"]).strip()
    if not api_key:
        raise RuntimeError("the active WeKnora profile has no credential")
    return api_key


def embed_target(
    target: Target,
    *,
    api_key: str,
    api_base_url: str,
    model_id: str,
    attempts: int = 5,
) -> tuple[Target, list[float]]:
    endpoint = f"{api_base_url.rstrip('/')}/api/v1/models/{model_id}/debug"
    request_body = urllib.parse.urlencode({"input": target.index_content}).encode("utf-8")
    last_error: Exception | None = None
    for attempt in range(attempts):
        try:
            request = urllib.request.Request(
                endpoint,
                data=request_body,
                method="POST",
                headers={
                    "Content-Type": "application/x-www-form-urlencoded",
                    "X-API-Key": api_key,
                },
            )
            with urllib.request.urlopen(request, timeout=90) as response:
                payload = json.load(response)
            result = payload.get("data", {})
            if payload.get("success") is not True or result.get("ok") is not True:
                raise RuntimeError(result.get("error") or "model debug call failed")
            raw_vector = result.get("raw_response")
            if not isinstance(raw_vector, list) or len(raw_vector) != 1024:
                raise RuntimeError("embedding dimension is not 1024")
            vector = [float(value) for value in raw_vector]
            if not all(math.isfinite(value) for value in vector):
                raise RuntimeError("embedding contains a non-finite value")
            norm_sq = sum(value * value for value in vector)
            if norm_sq <= 1e-8:
                raise RuntimeError("embedding is zero-valued")
            return target, vector
        except (OSError, ValueError, RuntimeError, urllib.error.URLError) as exc:
            last_error = exc
            if attempt + 1 < attempts:
                time.sleep(0.5 * (2**attempt))
    raise RuntimeError(f"chunk {target.chunk_id} could not be embedded: {last_error}")


def sql_text(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def base64_sql_text(value: str) -> str:
    import base64

    encoded = base64.b64encode(value.encode("utf-8")).decode("ascii")
    return f"convert_from(decode('{encoded}','base64'),'UTF8')"


def build_insert_sql(
    results: list[tuple[Target, list[float]]], expected: int, kb_id: str
) -> str:
    guard_rows: list[str] = []
    embedding_rows: list[str] = []
    for target, vector in results:
        tag_id = "NULL" if target.tag_id is None else sql_text(target.tag_id)
        enabled = "TRUE" if target.is_enabled else "FALSE"
        guard_rows.append(
            "(" + ",".join(
                [
                    sql_text(target.chunk_id),
                    str(target.legacy_id),
                    sql_text(target.title_md5),
                    sql_text(target.content_md5),
                    sql_text(target.index_content_md5),
                ]
            ) + ")"
        )
        vector_literal = "[" + ",".join(format(value, ".9g") for value in vector) + "]"
        embedding_rows.append(
            "(" + ",".join(
                [
                    "CURRENT_TIMESTAMP",
                    "CURRENT_TIMESTAMP",
                    sql_text(target.chunk_id),
                    "0",
                    sql_text(target.chunk_id),
                    sql_text(target.knowledge_id),
                    sql_text(target.knowledge_base_id),
                    base64_sql_text(target.index_content),
                    "1024",
                    sql_text(vector_literal) + "::halfvec",
                    enabled,
                    tag_id,
                ]
            ) + ")"
        )

    return f"""
BEGIN;
CREATE TEMP TABLE repair_target_guard (
    chunk_id varchar(36) PRIMARY KEY,
    legacy_id integer NOT NULL,
    title_md5 text NOT NULL,
    content_md5 text NOT NULL,
    index_content_md5 text NOT NULL
) ON COMMIT DROP;
INSERT INTO repair_target_guard VALUES
{','.join(guard_rows)};

DO $repair_guard$
DECLARE
    valid_targets integer;
BEGIN
    SELECT count(*) INTO valid_targets
    FROM repair_target_guard g
    JOIN chunks c ON c.id = g.chunk_id
    JOIN knowledges k ON k.id = c.knowledge_id
    JOIN embeddings e1 ON e1.id = g.legacy_id
      AND e1.source_id = c.id AND e1.source_type = 1
      AND e1.dimension = 1024
      AND e1.embedding::text ~ '^\\[0(\\.0+)?(,0(\\.0+)?)*\\]$'
    LEFT JOIN embeddings e0 ON e0.source_id = c.id AND e0.source_type = 0
    WHERE k.knowledge_base_id = '{kb_id}'
      AND k.deleted_at IS NULL
      AND c.deleted_at IS NULL
      AND c.chunk_type = 'text'
      AND e0.id IS NULL
      AND md5(coalesce(k.title, '')) = g.title_md5
      AND md5(c.content) = g.content_md5;
    IF valid_targets <> {expected} THEN
        RAISE EXCEPTION 'repair guard failed: expected {expected} unchanged targets, found %', valid_targets;
    END IF;
END
$repair_guard$;

INSERT INTO embeddings (
    created_at, updated_at, source_id, source_type, chunk_id,
    knowledge_id, knowledge_base_id, content, dimension, embedding,
    is_enabled, tag_id
) VALUES
{','.join(embedding_rows)}
ON CONFLICT (source_id, source_type) DO NOTHING;

DO $repair_verify$
DECLARE
    valid_embeddings integer;
BEGIN
    SELECT count(*) INTO valid_embeddings
    FROM repair_target_guard g
    JOIN embeddings e ON e.source_id = g.chunk_id AND e.source_type = 0
    JOIN chunks c ON c.id = g.chunk_id
    WHERE e.chunk_id = c.id
      AND e.knowledge_id = c.knowledge_id
      AND e.knowledge_base_id = c.knowledge_base_id
      AND e.dimension = 1024
      AND e.embedding IS NOT NULL
      AND e.embedding::text !~ '^\\[0(\\.0+)?(,0(\\.0+)?)*\\]$'
      AND md5(e.content) = g.index_content_md5
      AND e.is_enabled = c.is_enabled
      AND e.tag_id IS NOT DISTINCT FROM c.tag_id;
    IF valid_embeddings <> {expected} THEN
        RAISE EXCEPTION 'repair verification failed: expected {expected} valid embeddings, found %', valid_embeddings;
    END IF;
END
$repair_verify$;
COMMIT;
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true", help="generate and insert embeddings")
    parser.add_argument("--expected", type=int, default=115)
    parser.add_argument("--workers", type=int, default=4)
    parser.add_argument("--kb-id", default=DEFAULT_KB_ID)
    parser.add_argument("--model-id", default=DEFAULT_MODEL_ID)
    parser.add_argument("--api-base-url", default=DEFAULT_API_BASE_URL)
    parser.add_argument("--postgres-container", default="WeKnora-postgres")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if not UUID_RE.fullmatch(args.model_id):
        raise ValueError("model id is not a UUID")
    if args.workers < 1:
        raise ValueError("workers must be positive")
    targets = load_targets(args.kb_id, args.postgres_container)
    affected_documents = len({target.knowledge_id for target in targets})
    print(f"targets={len(targets)} affected_documents={affected_documents}")
    if len(targets) != args.expected:
        raise RuntimeError(f"expected {args.expected} targets, found {len(targets)}")
    if not args.apply:
        print("dry_run=true writes=0")
        return 0

    api_key = get_api_key()
    completed = 0
    results: list[tuple[Target, list[float]]] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as executor:
        futures = {
            executor.submit(
                embed_target,
                target,
                api_key=api_key,
                api_base_url=args.api_base_url,
                model_id=args.model_id,
            ): target
            for target in targets
        }
        for future in concurrent.futures.as_completed(futures):
            results.append(future.result())
            completed += 1
            if completed % 10 == 0 or completed == len(targets):
                print(f"embedded={completed}/{len(targets)}", flush=True)

    results.sort(key=lambda item: item[0].chunk_id)
    sql = build_insert_sql(results, args.expected, args.kb_id)
    psql_scalar(sql, args.postgres_container)
    print(f"inserted_and_verified={len(results)}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # concise operational error, never prints content/token
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
