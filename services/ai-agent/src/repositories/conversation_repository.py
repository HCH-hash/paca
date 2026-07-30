"""Database access layer for agent conversations and events."""

from __future__ import annotations

import json

from ..core.db import get_pool
from ..models.conversation_status import ConversationStatus


async def update_conversation_status(
    conversation_id: str,
    status: ConversationStatus,
    error_message: str | None = None,
) -> None:
    pool = await get_pool()
    if error_message is not None:
        await pool.execute(
            (
                "UPDATE agent_conversations"
                " SET status = $1, error_message = $2,"
                " updated_at = now() WHERE id = $3"
            ),
            status,
            error_message,
            conversation_id,
        )
    else:
        await pool.execute(
            "UPDATE agent_conversations SET status = $1, updated_at = now() WHERE id = $2",
            status,
            conversation_id,
        )


async def fail_if_not_terminal(conversation_id: str, error_message: str) -> bool:
    """Atomically mark a conversation FAILED unless it has already reached a
    terminal status. Returns True iff this call performed the transition.

    Used by acp_dispatch.py's watchdog to fail a turn that never got a
    turn_status back from the bridge — the WHERE clause makes this race-safe
    against a legitimate terminal status arriving concurrently (the watchdog
    then simply loses the race and does nothing).
    """
    pool = await get_pool()
    result = await pool.execute(
        """
        UPDATE agent_conversations
        SET status = $1, error_message = $2, updated_at = now()
        WHERE id = $3 AND status NOT IN ('finished', 'failed', 'stopped')
        """,
        ConversationStatus.FAILED,
        error_message,
        conversation_id,
    )
    return result == "UPDATE 1"


async def fail_orphaned_conversations(older_than_minutes: int) -> int:
    """Fail conversations stuck non-terminal with no progress for a while — the
    last-resort backstop for a turn orphaned by a worker/bridge/ai-agent restart (its
    in-memory watchdog/task is gone, so nothing else would ever move it off
    'running'/'queued'). Returns how many were failed.

    Race-safe: the WHERE guards on BOTH a non-terminal status and the staleness window
    (updated_at is bumped whenever the status is written), so a turn that is genuinely
    live — just dispatched, or actively progressing — is never touched.
    """
    pool = await get_pool()
    result = await pool.execute(
        """
        UPDATE agent_conversations
        SET status = 'failed',
            error_message = 'Recovered automatically: the previous turn was interrupted'
                            ' (a restart lost it). Send another message to continue —'
                            ' your conversation history is kept.',
            updated_at = now()
        WHERE status IN ('running', 'queued')
          AND updated_at < now() - make_interval(mins => $1)
        """,
        older_than_minutes,
    )
    try:
        return int(str(result).split()[-1])
    except (ValueError, IndexError):
        return 0


async def get_conversation_agent_type(conversation_id: str) -> tuple[str, str] | None:
    """Return (agent_id, agent_type) for a conversation's owning agent.

    Used by worker._handle_control to decide whether a stop/pause control
    message should signal the in-process polling loop (LLM agents) or forward
    through the ACP bridge dispatch channel (see agent/acp_bridge.py) instead.
    """
    pool = await get_pool()
    row = await pool.fetchrow(
        """
        SELECT a.id AS agent_id, a.agent_type
        FROM agent_conversations c
        JOIN agents a ON a.id = c.agent_id
        WHERE c.id = $1
        """,
        conversation_id,
    )
    if row is None:
        return None
    return str(row["agent_id"]), row["agent_type"]


async def get_next_event_index(conversation_id: str) -> int:
    """Return the next unused event_index for a conversation.

    event_index is unique per conversation_id (see the
    uq_agent_conversation_events_index constraint) and spans the
    conversation's entire lifetime, not just the current turn — a resumed
    chat conversation's next turn must continue numbering from where the
    previous turn left off. Starting back at 0 would collide with indices
    already used by earlier turns, and insert_conversation_event's
    `ON CONFLICT DO NOTHING` would then silently drop those new events.
    """
    pool = await get_pool()
    row = await pool.fetchrow(
        "SELECT COALESCE(MAX(event_index), -1) + 1 AS next_index"
        " FROM agent_conversation_events WHERE conversation_id = $1",
        conversation_id,
    )
    return row["next_index"] if row else 0


async def get_seen_event_ids(conversation_id: str) -> set[str]:
    """Return the SDK event ids already persisted for a conversation.

    Used to seed a fresh turn's in-memory dedup set (see executor._SeenEvents)
    so that reconcile() — which re-walks the *entire* remote SDK event
    history, including earlier turns, not just the current one — does not
    re-persist already-stored events from previous turns under new
    event_index values. Without this, every resumed chat turn duplicates the
    whole prior conversation history.
    """
    pool = await get_pool()
    rows = await pool.fetch(
        "SELECT payload->>'id' AS sdk_id FROM agent_conversation_events WHERE conversation_id = $1",
        conversation_id,
    )
    return {row["sdk_id"] for row in rows if row["sdk_id"] is not None}


def _find_text(v: object, depth: int = 0) -> str:
    """Recursively pull the human-readable text out of an event payload,
    whatever shape it arrives in (string, list of parts, or an object carrying
    the text on a common field). Mirrors the frontend's toText() so the replayed
    transcript reads the same as what the user saw."""
    if v is None or depth > 6:
        return ""
    if isinstance(v, str):
        return v.strip()
    if isinstance(v, list):
        parts = [_find_text(x, depth + 1) for x in v]
        return "\n".join(p for p in parts if p)
    if isinstance(v, dict):
        for k in ("text", "content", "message", "llm_message"):
            if k in v and not isinstance(v[k], (bool, int, float)):
                t = _find_text(v[k], depth + 1)
                if t:
                    return t
        return ""
    return ""


async def get_conversation_transcript(
    conversation_id: str, max_chars: int = 12000, omit_last_user: str | None = None
) -> str:
    """Return the conversation's prior messages as a plain text transcript,
    oldest first, so a COLD-resumed sandbox/session can be given back its full
    context (the DB is the source of truth — see get_next_event_index).

    Only message-bearing events (user + agent messages) are included; tool
    bookkeeping is skipped to keep the replay compact. Capped to the most recent
    ``max_chars`` characters so a very long chat can't blow up the prompt."""
    pool = await get_pool()
    rows = await pool.fetch(
        "SELECT event_type, event_source, payload::text AS payload"
        " FROM agent_conversation_events WHERE conversation_id = $1"
        " ORDER BY event_index ASC",
        conversation_id,
    )
    lines: list[str] = []
    for row in rows:
        etype = (row["event_type"] or "")
        source = (row["event_source"] or "agent")
        if source != "user" and "message" not in etype.lower():
            continue  # skip tool/thought/bookkeeping events
        try:
            payload = json.loads(row["payload"]) if row["payload"] else {}
        except Exception:
            continue
        text = _find_text(payload)
        if not text:
            continue
        role = "User" if source == "user" else "Assistant"
        lines.append(f"{role}: {text}")
    # Drop a trailing user line that is the CURRENT message (persisted by the API
    # before dispatch) so it isn't shown twice — once as history, once as the turn.
    if omit_last_user is not None and lines:
        want = f"User: {omit_last_user.strip()}"
        if lines[-1].strip() == want.strip():
            lines.pop()
    if not lines:
        return ""
    joined = "\n\n".join(lines)
    if len(joined) > max_chars:
        joined = "…(earlier messages trimmed)…\n\n" + joined[-max_chars:]
    return joined


async def insert_conversation_event(
    conversation_id: str,
    event_type: str,
    event_source: str,
    event_index: int,
    payload: str,
) -> None:
    pool = await get_pool()
    await pool.execute(
        """
        INSERT INTO agent_conversation_events
            (id, conversation_id, event_type, event_source, event_index, payload, created_at)
        VALUES
            (gen_random_uuid(), $1, $2, $3, $4, $5::jsonb, now())
        ON CONFLICT DO NOTHING
        """,
        conversation_id,
        event_type,
        event_source,
        event_index,
        payload,
    )
