"""Runs local OpenHands ACP conversations and reports events back over the bridge.

Each conversation is a plain, fully local `openhands.sdk.Conversation` backed
by `ACPAgent` — the SDK's own default execution mode (same as its quickstart
example: `Conversation(agent=agent, workspace="./my-project")`). ACPAgent
spawns the chosen coding CLI (Claude Code / Codex / Gemini CLI / a custom ACP
server) as a genuine local subprocess against `workspace`, using whatever
auth, MCP servers, and skills are already configured in the user's own local
environment — this daemon does not manage or forward any of that. That also
means the CLI's own native tools (bash, git, gh, etc.) run with the user's own
real credentials, so it can clone/push/open PRs exactly as if the user were
driving it themselves — something a Paca-hosted sandbox can never do, since
ACPAgent doesn't accept custom tools.

Conversations are driven via `conversation.arun()` (the SDK's async run
loop), scheduled as a plain `asyncio.Task` on this daemon's own event loop,
rather than `conversation.run()` on a dedicated thread. `run()`/`pause()`
only take effect *between* whole agent steps and can't cancel one already in
flight — for `ACPAgent` a single step is the whole ACP turn (every tool call
the coding CLI makes before replying), so pressing "stop" mid-turn wouldn't
actually stop anything until that turn finished on its own. `arun()` tracks
the run as a cancellable `asyncio.Task`, so `conversation.interrupt()` can
cancel it immediately (mid-tool-call) and the SDK sends the coding CLI a
real ACP `session/cancel` in response — see `LocalConversation.interrupt()`
and `ACPAgent.astep()`'s `CancelledError` handling. It also sidesteps a
cross-thread state-lock deadlock the SDK documents for the sync `run()` path
(OpenHands SDK issues #3348/#3350): here, `interrupt()` never blocks waiting
on the conversation's state lock, so it's safe to call directly from this
event loop's own thread.

SESSION LIFETIME. Nothing in the protocol ever ends a conversation: the server
only ever sends start_turn / stop_turn / pause_turn, and every card wake mints
a brand-new conversation_id. Left alone, this daemon therefore accumulates one
live Conversation — and with it one ACP adapter, one `claude` process and its
MCP servers, ~500 MB — per wake, for the lifetime of the unit. So a turn that
ends arms a single idle timer for its conversation (PACA_SESSION_IDLE_MINUTES,
default 10); the next turn cancels it, and if none comes the whole process tree
is torn down and the Conversation closed.

Closing is not amnesia: the ACP session id is written to
`~/.local/state/paca-acp-bridge/<agent id>.json` after every turn, and a later
turn for the same conversation_id rebuilds the agent with
`acp_resume_session_id`, which makes the SDK call `session/load` — i.e.
`claude --resume` — so the chat comes back with its context. When that
`session/load` does not take (the CLI pruned the transcript, the adapter was
upgraded, the workspace moved), the SDK does not fail the turn — it silently
starts a fresh, empty session — so the resume is checked, once, by comparing
the session id it ended up on against the one that was asked for, before the
message is sent; a resume that did not take falls back to replaying the
transcript into that same turn. There is no sweeper and no watchdog here: one
timer per conversation, armed where a turn ends and cancelled where the next
one starts.
"""

from __future__ import annotations

import asyncio
import contextlib
import dataclasses
import json
import logging
import os
import signal
import tempfile
import time
from collections.abc import Awaitable, Callable
from pathlib import Path
from typing import Any

from openhands.sdk import Conversation
from openhands.sdk.agent import ACPAgent
from openhands.sdk.settings.acp_providers import get_acp_provider

logger = logging.getLogger(__name__)

SendFn = Callable[[dict[str, Any]], Awaitable[None]]
# (delay_seconds, callback) -> something with .cancel(); asyncio.TimerHandle in
# production, a fake clock in the tests.
CallLater = Callable[[float, Callable[[], None]], Any]

# How long a conversation may sit with no turn before its process tree is torn
# down. 0 closes it the moment the turn ends.
DEFAULT_IDLE_MINUTES = 10.0
IDLE_MINUTES_ENV = "PACA_SESSION_IDLE_MINUTES"
# How long the adapter (and the CLI and MCP servers under it) get to exit on
# their own after stdin is closed, before the survivors are killed.
TEARDOWN_GRACE_SECONDS = 10.0
_TEARDOWN_POLL_SECONDS = 0.1
# On top of the grace period, how much longer a start_turn will wait for an
# already-running idle close before giving up on it and starting a fresh
# session anyway. The wait has to be bounded because the second half of a
# teardown is the SDK's own `conversation.close()`, and that is only *partly*
# bounded: `ACPAgent._shutdown_runtime` gives `conn.close` and the process wait
# 5s each, but `self._executor.close()` and the file-credential release have no
# timeout at all. This wait happens inline in the bridge's websocket read loop,
# so an unbounded one stops the unit dispatching every *other* conversation's
# turns too, not just this one's.
_CLOSE_WAIT_EXTRA_SECONDS = 15.0
# Bound on the remembered conversation_id -> ACP session id map. Every card wake
# mints a new conversation_id and never comes back, so without a bound the state
# file would grow forever; chats (which do come back) reuse one id and stay at
# the recent end of the map.
_MAX_REMEMBERED_SESSIONS = 200
_KILL_SIGNAL = getattr(signal, "SIGKILL", signal.SIGTERM)


@dataclasses.dataclass
class _ConversationHandle:
    conversation: Conversation
    task: asyncio.Task[None]


def resolve_acp_command(acp_provider: str | None, acp_command: list[str]) -> list[str]:
    """Resolve the command to launch the ACP server.

    Built-in providers (claude-code/codex/gemini-cli) use the OpenHands SDK's
    own default command for that provider; "custom" (or an unrecognized
    provider) requires an explicit acp_command from the agent's config.
    """
    if acp_provider and acp_provider != "custom":
        provider = get_acp_provider(acp_provider)
        if provider is not None:
            return list(provider.default_command)
    if not acp_command:
        raise ValueError(
            f"No default command for acp_provider={acp_provider!r}; "
            "a custom acp_command is required"
        )
    return acp_command


def idle_seconds() -> float:
    """Seconds of idleness after which a conversation is closed.

    Read per turn (not once at startup) so changing the unit's
    PACA_SESSION_IDLE_MINUTES and restarting is the only thing needed to retune
    it — and so a bad value can never wedge the daemon at import time.
    """
    raw = os.environ.get(IDLE_MINUTES_ENV)
    if raw is None or not raw.strip():
        return DEFAULT_IDLE_MINUTES * 60.0
    try:
        minutes = float(raw)
    except ValueError:
        logger.warning(
            "Ignoring %s=%r (not a number); using %s minutes",
            IDLE_MINUTES_ENV,
            raw,
            DEFAULT_IDLE_MINUTES,
        )
        return DEFAULT_IDLE_MINUTES * 60.0
    if minutes < 0:
        logger.warning(
            "Ignoring %s=%r (negative); using %s minutes",
            IDLE_MINUTES_ENV,
            raw,
            DEFAULT_IDLE_MINUTES,
        )
        return DEFAULT_IDLE_MINUTES * 60.0
    return minutes * 60.0


def _read_proc_stat(pid: int) -> tuple[int, str, str] | None:
    """`(ppid, start_time, state)` for *pid* from /proc, or None if unreadable.

    `start_time` is field 22 of /proc/<pid>/stat: together with the pid it
    identifies a process for as long as the box is up, which is what makes it
    safe to SIGKILL a pid we noted seconds earlier — a pid the kernel has since
    recycled has a different start time and is left alone.
    """
    try:
        with open(f"/proc/{pid}/stat", encoding="utf-8", errors="replace") as fh:
            data = fh.read()
    except OSError:
        return None
    # The comm field is parenthesised and may itself contain spaces and
    # parentheses, so everything before the LAST ')' has to be skipped.
    close_paren = data.rfind(")")
    if close_paren == -1:
        return None
    fields = data[close_paren + 1 :].split()
    if len(fields) < 20:
        return None
    try:
        ppid = int(fields[1])
    except ValueError:
        return None
    return ppid, fields[19], fields[0]


def _process_tree(root_pid: int) -> dict[int, str]:
    """Snapshot `pid -> start_time` for *root_pid* and every descendant.

    Taken BEFORE anything is signalled: once the adapter dies its children are
    reparented to init and stop being findable from the root pid, and those
    children (`claude`, the MCP servers) are most of the leaked memory.
    """
    root = _read_proc_stat(root_pid)
    if root is None:
        # No /proc (not Linux), or the process is already gone. Fall back to the
        # root pid alone with an empty start token, meaning "identity unverifiable".
        return {root_pid: ""}
    table: dict[int, tuple[int, str, str]] = {}
    try:
        entries = os.listdir("/proc")
    except OSError:
        entries = []
    for name in entries:
        if not name.isdigit():
            continue
        stat = _read_proc_stat(int(name))
        if stat is not None:
            table[int(name)] = stat
    children: dict[int, list[int]] = {}
    for pid, (ppid, _start, _state) in table.items():
        children.setdefault(ppid, []).append(pid)
    tree = {root_pid: root[1]}
    queue = [root_pid]
    while queue:
        current = queue.pop()
        for child in children.get(current, []):
            if child in tree:
                continue
            tree[child] = table[child][1]
            queue.append(child)
    return tree


def _still_alive(tree: dict[int, str]) -> list[int]:
    """The members of *tree* that are still running, ascending."""
    alive: list[int] = []
    for pid, start in sorted(tree.items()):
        stat = _read_proc_stat(pid)
        if stat is None:
            # Unverifiable entries (no /proc) fall back to a null signal; a
            # readable /proc that no longer has the pid means it is gone.
            if not start and _signal_alive(pid):
                alive.append(pid)
            continue
        if start and stat[1] != start:
            continue  # the kernel recycled this pid for something else
        if stat[2] == "Z":
            continue  # exited, just not reaped yet
        alive.append(pid)
    return alive


def _signal_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return True


def _close_stdin(process: Any) -> None:
    """Close the ACP adapter's stdin — the graceful "we're done" for a stdio
    JSON-RPC server. The adapter exits, and `claude` and its MCP servers exit
    with it, each flushing its own state (the transcript a later `--resume`
    needs) instead of being shot.

    The pipe was opened on the SDK's own portal loop, in another thread, so the
    close is handed back to that loop when it is still running.
    """
    stdin = getattr(process, "stdin", None)
    if stdin is None:
        return
    try:
        current = asyncio.get_running_loop()
    except RuntimeError:
        current = None
    owner = getattr(getattr(stdin, "transport", None), "_loop", None)
    try:
        if owner is not None and owner is not current and owner.is_running():
            owner.call_soon_threadsafe(stdin.close)
        else:
            stdin.close()
    except Exception:
        logger.debug("Could not close the ACP adapter's stdin", exc_info=True)


async def shutdown_process_tree(
    process: Any,
    *,
    grace_seconds: float = TEARDOWN_GRACE_SECONDS,
    poll_seconds: float = _TEARDOWN_POLL_SECONDS,
) -> None:
    """Stop the ACP adapter and everything under it, gracefully if possible.

    The SDK's own `close()` only `terminate()`s the adapter, which leaves
    `claude` and the MCP servers it started orphaned and running — that is the
    leak. Here the whole tree is noted first, asked to exit via stdin, and only
    the members of THAT tree that are still alive after the grace period are
    killed.
    """
    pid = getattr(process, "pid", None)
    if not isinstance(pid, int):
        return
    tree = _process_tree(pid)
    _close_stdin(process)
    loop = asyncio.get_running_loop()
    deadline = loop.time() + max(grace_seconds, 0.0)
    while True:
        # Re-scan so children started since the snapshot are included too; the
        # snapshot wins on conflicts because it holds the pre-teardown identity.
        alive = _still_alive({**_process_tree(pid), **tree})
        if not alive:
            logger.debug("ACP process tree %s exited on its own", pid)
            return
        if loop.time() >= deadline:
            break
        await asyncio.sleep(poll_seconds)
    logger.warning(
        "ACP process tree %s did not exit within %.0fs; killing %s",
        pid,
        grace_seconds,
        alive,
    )
    for victim in reversed(alive):
        try:
            os.kill(victim, _KILL_SIGNAL)
        except OSError:
            continue


class ConversationRunner:
    """Owns one local `Conversation` per active conversation_id.

    Mirrors services/ai-agent's executor.py chat_sandboxes pattern (reuse a
    live conversation across turns of the same chat), but entirely
    in-process — there's no sandbox to start/stop here.
    """

    def __init__(
        self,
        workspace: str,
        send: SendFn,
        agent_id: str | None = None,
        state_path: str | os.PathLike[str] | None = None,
        call_later: CallLater | None = None,
        teardown_grace_seconds: float = TEARDOWN_GRACE_SECONDS,
    ) -> None:
        self.workspace = workspace
        self._send = send
        self._agent_id = agent_id or os.environ.get("PACA_ACP_AGENT_ID") or ""
        self._explicit_state_path = Path(state_path) if state_path is not None else None
        self._call_later = call_later
        self._teardown_grace_seconds = teardown_grace_seconds
        # Captured lazily in start_turn() (always awaited from inside the
        # running loop) rather than here — __init__ runs before
        # asyncio.run() starts the loop that will actually be running when
        # ACPAgent's own background ("portal") thread needs to schedule
        # coroutines back onto it (e.g. mid-turn streaming events).
        self._loop: asyncio.AbstractEventLoop | None = None
        self._conversations: dict[str, _ConversationHandle] = {}
        # One idle timer per conversation, and the close it eventually starts.
        self._timers: dict[str, Any] = {}
        self._closing: dict[str, asyncio.Task[None]] = {}
        self._sessions: dict[str, dict[str, Any]] | None = None

    async def start_turn(self, data: dict[str, Any]) -> None:
        self._loop = asyncio.get_running_loop()
        conversation_id = data["conversation_id"]
        project_id = data["project_id"]
        message = data.get("message", "")
        history = data.get("history", "")

        # This conversation is wanted again: call off the idle countdown armed
        # when its last turn ended, and — if that countdown already fired — wait
        # for the teardown to finish before deciding warm vs. cold, so we never
        # start a turn against a Conversation whose subprocesses are going away.
        self._cancel_idle_timer(conversation_id)
        await self._await_close(conversation_id)

        existing = self._conversations.get(conversation_id)
        if existing is not None:
            if not existing.task.done():
                # A previous turn's task is still driving this same
                # Conversation object via .send_message()/.arun() — starting
                # a second one on top of it would call into the SDK
                # concurrently, which Conversation isn't built to handle.
                # Reject explicitly rather than risking corrupted state, so
                # the caller gets an immediate failure instead of waiting
                # out acp_dispatch.py's watchdog timeout.
                logger.warning(
                    "Ignoring start_turn for conversation %s: a previous turn is still running",
                    conversation_id,
                )
                await self._report_status(
                    conversation_id,
                    project_id,
                    "failed",
                    "A previous turn for this conversation is still running; please retry.",
                )
                return
            # Resume — reply on the conversation object already running from
            # an earlier turn in this same chat session.
            existing.task = asyncio.create_task(
                self._run_conversation(existing.conversation, conversation_id, project_id, message)
            )
            return

        try:
            command = resolve_acp_command(data.get("acp_provider"), data.get("acp_command") or [])
        except Exception as exc:
            logger.error("Cannot start conversation %s: %s", conversation_id, exc)
            await self._report_status(conversation_id, project_id, "failed", str(exc))
            return

        resume_session_id = self._saved_session_id(conversation_id)
        conversation = self._build_conversation(
            command, conversation_id, project_id, resume_session_id
        )
        task = asyncio.create_task(
            self._run_cold_start(
                conversation=conversation,
                conversation_id=conversation_id,
                project_id=project_id,
                command=command,
                message=message,
                history=history,
                resume_session_id=resume_session_id,
            )
        )
        self._conversations[conversation_id] = _ConversationHandle(
            conversation=conversation, task=task
        )

    def interrupt(self, conversation_id: str | None) -> None:
        """Handle a stop_turn/pause_turn message — both just interrupt the
        in-flight turn; there's no sandbox lifecycle to additionally tear
        down (unlike the cloud path's full stop vs. pause distinction).

        Safe to call directly here, synchronously, on the event-loop thread:
        while a turn is actually in flight, `conversation.interrupt()`
        cancels the tracked `arun()` task via a non-blocking
        `call_soon_threadsafe`, so it can't stall this loop the way the sync
        `run()` path's `pause()` fallback could (see module docstring). If
        the tracked task has already finished by the time this runs (a race
        with the turn wrapping up on its own), `interrupt()` falls back to a
        synchronous `pause()` that does briefly acquire the state lock — a
        much smaller window than the sync-`run()` deadlock this replaces,
        since nothing here holds that lock for the duration of a whole turn.
        """
        if not conversation_id:
            return
        handle = self._conversations.get(conversation_id)
        if handle is None:
            return
        try:
            handle.conversation.interrupt()
        except Exception:
            logger.exception("Failed to interrupt conversation %s", conversation_id)

    # ----------------------------------------------------------------- turns

    def _build_conversation(
        self,
        command: list[str],
        conversation_id: str,
        project_id: str,
        resume_session_id: str | None,
    ) -> Conversation:
        agent_kwargs: dict[str, Any] = {"acp_command": command}
        if resume_session_id:
            # The SDK calls session/load with this instead of session/new, which
            # for the Claude adapter is `claude --resume <id>`: the CLI reloads
            # the transcript it wrote before the idle close.
            agent_kwargs["acp_resume_session_id"] = resume_session_id
            logger.info(
                "Cold start for conversation %s: resuming its ACP session",
                conversation_id,
            )
        agent = ACPAgent(**agent_kwargs)
        return Conversation(
            agent=agent,
            workspace=self.workspace,
            callbacks=[self._make_event_callback(conversation_id, project_id)],
        )

    @staticmethod
    def _first_message(message: str, history: str) -> str:
        # COLD start with no ACP session to resume: first turn ever, or the
        # daemon restarted and lost both the in-memory session and the saved id.
        # If the server sent the prior transcript, replay it as restored context
        # so the agent remembers the whole conversation instead of starting
        # blank. Warm resumes and session/load resumes skip this — the live (or
        # reloaded) session already holds the context. A session/load that was
        # rejected does NOT skip it: the SDK silently gives us a fresh, empty
        # session instead, which is a cold start in everything but name (see
        # _run_resumed_turn).
        if not history:
            return message
        return (
            "## Restored conversation context\n"
            "This chat session was restarted, so here is what was already said. "
            "Use it as your memory of the conversation; do not greet the user as if "
            "this were a new chat.\n\n"
            f"{history}\n\n"
            "---\n\n"
            f"{message}"
        )

    async def _run_cold_start(
        self,
        *,
        conversation: Conversation,
        conversation_id: str,
        project_id: str,
        command: list[str],
        message: str,
        history: str,
        resume_session_id: str | None,
    ) -> None:
        try:
            if resume_session_id is None:
                error = await self._run_turn(
                    conversation, conversation_id, self._first_message(message, history)
                )
            else:
                error = await self._run_resumed_turn(
                    conversation=conversation,
                    conversation_id=conversation_id,
                    message=message,
                    history=history,
                    resume_session_id=resume_session_id,
                )
                if error is not None:
                    # A resume that actually *raised* — a transport failure, a
                    # missing CLI binary, a crashed adapter — so there is no
                    # working connection to carry on with. (A merely rejected
                    # session/load does not come out here; the SDK swallows it,
                    # which _run_resumed_turn handles in-place instead.) Forget
                    # the saved id — keeping it would fail the same way on every
                    # later turn — and start this conversation over, once, from
                    # scratch.
                    logger.warning(
                        "Resuming the ACP session of conversation %s failed (%s); "
                        "forgetting the saved session id and starting a fresh session",
                        conversation_id,
                        error,
                    )
                    self._forget_session_id(conversation_id)
                    await self._teardown(conversation_id, conversation)
                    conversation = self._build_conversation(
                        command, conversation_id, project_id, None
                    )
                    handle = self._conversations.get(conversation_id)
                    if handle is not None:
                        handle.conversation = conversation
                    error = await self._run_turn(
                        conversation, conversation_id, self._first_message(message, history)
                    )
            await self._finish_turn(conversation_id, project_id, error)
        finally:
            self._arm_idle_timer(conversation_id)

    async def _run_resumed_turn(
        self,
        *,
        conversation: Conversation,
        conversation_id: str,
        message: str,
        history: str,
        resume_session_id: str,
    ) -> Exception | None:
        """Drive the first turn of a conversation built to resume a saved ACP
        session, checking that the resume actually took.

        Starting the session is split out of the turn because a *rejected*
        `session/load` does not fail: `ACPAgent._start_acp_server` catches the
        protocol error, logs "starting a fresh session", and falls through to
        `session/new` (openhands-sdk 1.47.0 acp_agent.py:3124-3158). Nothing
        raises, so the failure arm in `_run_cold_start` never fires — and
        because this is the resume branch it would have sent the bare message,
        skipping the history replay too. The chat would answer from a blank
        agent, report "finished", and then save the new empty session's id.
        That is reachable here without anything being broken: a chat idle past
        the coding CLI's own transcript retention, a CLI upgrade, or a
        server-side cwd mismatch (which the SDK deliberately leaves to
        `session/load` to reject, acp_agent.py:2922-2932).

        The id the SDK ended up on is the only signal there is, and it is
        readable as soon as `init_state` has run (acp_agent.py:2338) — i.e.
        before the message is sent, while this turn can still carry the
        transcript itself. No rebuild is needed in that case: the fresh session
        the SDK minted is a working one, exactly what a cold start would have
        produced, and `_run_turn` saves its id on the way out.
        """
        error = await self._start_session(conversation, conversation_id)
        if error is not None:
            return error
        if self._session_id_of(conversation) == resume_session_id:
            # session/load took: the reloaded session already holds the
            # context, so the transcript is not replayed on top of it.
            return await self._run_turn(conversation, conversation_id, message)
        logger.warning(
            "Conversation %s did not resume its saved ACP session; the coding CLI "
            "started a fresh, empty one instead. Replaying the transcript into this "
            "turn so the chat keeps its context",
            conversation_id,
        )
        return await self._run_turn(
            conversation, conversation_id, self._first_message(message, history)
        )

    @staticmethod
    async def _start_session(conversation: Conversation, conversation_id: str) -> Exception | None:
        """Run the SDK's lazy init — spawn the ACP server and do session/load
        or session/new — without sending anything yet.

        This is exactly what `arun()` does first
        (`local_conversation.py:2119`), off the loop for the same reason:
        `init_state` blocks (an ACP agent resolves credentials synchronously).
        It is idempotent and thread-safe, so the `arun()` inside the turn that
        follows sees it already done and skips it.
        """
        try:
            await asyncio.to_thread(conversation._ensure_agent_ready)
        except Exception as exc:
            logger.exception("Conversation %s could not start its ACP session", conversation_id)
            return exc
        return None

    @staticmethod
    def _session_id_of(conversation: Conversation) -> str | None:
        """The ACP session id this conversation is actually on, as the SDK
        records it in `state.agent_state` (acp_agent.py:2338)."""
        try:
            agent_state = conversation.state.agent_state
        except Exception:
            logger.debug("Conversation has no agent state", exc_info=True)
            return None
        if not isinstance(agent_state, dict):
            return None
        session_id = agent_state.get("acp_session_id")
        return session_id if isinstance(session_id, str) and session_id else None

    async def _run_conversation(
        self, conversation: Conversation, conversation_id: str, project_id: str, message: str
    ) -> None:
        try:
            error = await self._run_turn(conversation, conversation_id, message)
            await self._finish_turn(conversation_id, project_id, error)
        finally:
            # In a finally, so a turn cancelled by stop_turn/pause_turn arms the
            # timer too: an interrupted conversation is idle like any other.
            self._arm_idle_timer(conversation_id)

    async def _run_turn(
        self, conversation: Conversation, conversation_id: str, message: str
    ) -> Exception | None:
        """Drive one turn. Returns the failure instead of reporting it, so a
        failed resume can be retried on a fresh session before anything is
        reported to the server."""
        try:
            conversation.send_message(message)
            await conversation.arun()
        except Exception as exc:
            logger.exception("Conversation %s failed", conversation_id)
            return exc
        finally:
            self._remember_session_id(conversation_id, conversation)
        return None

    async def _finish_turn(
        self, conversation_id: str, project_id: str, error: Exception | None
    ) -> None:
        if error is None:
            await self._report_status_safely(conversation_id, project_id, "finished")
        else:
            await self._report_status_safely(conversation_id, project_id, "failed", str(error))

    # ------------------------------------------------------------ idle close

    def _schedule(self, delay: float, callback: Callable[[], None]) -> Any:
        if self._call_later is not None:
            return self._call_later(delay, callback)
        loop = self._loop
        if loop is None:
            raise RuntimeError("no event loop captured yet")
        return loop.call_later(delay, callback)

    def _arm_idle_timer(self, conversation_id: str) -> None:
        """One timer per conversation, armed where its turn ended."""
        if self._loop is None or conversation_id not in self._conversations:
            return
        self._cancel_idle_timer(conversation_id)
        delay = idle_seconds()
        logger.debug(
            "Conversation %s is idle; closing it in %.0fs unless another turn arrives",
            conversation_id,
            delay,
        )
        self._timers[conversation_id] = self._schedule(
            delay, lambda: self._on_idle(conversation_id)
        )

    def _cancel_idle_timer(self, conversation_id: str) -> None:
        timer = self._timers.pop(conversation_id, None)
        if timer is not None:
            timer.cancel()

    def _on_idle(self, conversation_id: str) -> None:
        self._timers.pop(conversation_id, None)
        handle = self._conversations.get(conversation_id)
        loop = self._loop
        if handle is None or loop is None:
            return
        if not handle.task.done():
            # A turn is running after all; it arms a fresh timer when it ends.
            return
        self._conversations.pop(conversation_id, None)
        self._closing[conversation_id] = loop.create_task(
            self._close_idle(conversation_id, handle.conversation)
        )

    async def _close_idle(self, conversation_id: str, conversation: Conversation) -> None:
        try:
            logger.info("Closing idle conversation %s", conversation_id)
            await self._teardown(conversation_id, conversation)
        finally:
            if self._closing.get(conversation_id) is asyncio.current_task():
                self._closing.pop(conversation_id, None)

    async def _teardown(self, conversation_id: str, conversation: Conversation) -> None:
        """Stop the ACP process tree, then close the Conversation.

        Both halves are isolated: a conversation whose close raises must not
        leave its bookkeeping behind, nor stop any other conversation's timer
        from closing its own session later.
        """
        process = getattr(getattr(conversation, "agent", None), "_process", None)
        if process is not None:
            try:
                await shutdown_process_tree(process, grace_seconds=self._teardown_grace_seconds)
            except Exception:
                logger.exception(
                    "Failed to stop the ACP process tree of conversation %s", conversation_id
                )
        else:
            # `_process` is the SDK's own PrivateAttr (acp_agent.py:1870) and the
            # only handle on the adapter it spawned; without it the tree walk —
            # the whole point of this teardown — cannot run, and close() alone
            # leaves `claude` and its MCP servers behind. Say so: "the leak fix
            # did nothing" must be visible in journalctl, not silent. (A turn
            # that never got as far as spawning the adapter lands here too, and
            # legitimately has nothing to stop.)
            logger.warning(
                "Conversation %s has no ACP process handle; closing it without "
                "stopping its process tree, so any coding CLI and MCP servers it "
                "started may be left running",
                conversation_id,
            )
        try:
            # close() is a blocking SDK call (it waits on the portal loop and on
            # the subprocess), so it runs off this event loop.
            await asyncio.to_thread(conversation.close)
        except Exception:
            logger.exception("Failed to close conversation %s", conversation_id)

    async def _await_close(self, conversation_id: str) -> None:
        closing = self._closing.get(conversation_id)
        if closing is None:
            return
        timeout = self._teardown_grace_seconds + _CLOSE_WAIT_EXTRA_SECONDS
        logger.info(
            "Waiting up to %.0fs for the idle close of conversation %s before starting "
            "its next turn",
            timeout,
            conversation_id,
        )
        # Shielded: if this start_turn is cancelled — or this wait gives up —
        # the teardown still finishes rather than being abandoned half-done.
        # Bounded: see _CLOSE_WAIT_EXTRA_SECONDS. Giving up costs this
        # conversation nothing (its next turn is a cold start either way, and
        # the old process tree is a different tree); waiting forever costs
        # every other conversation on this unit its turn.
        try:
            await asyncio.wait_for(asyncio.shield(closing), timeout=timeout)
        except TimeoutError:
            logger.warning(
                "The idle close of conversation %s is still running after %.0fs; "
                "starting its next turn on a fresh session rather than blocking "
                "this bridge any longer",
                conversation_id,
                timeout,
            )
            if self._closing.get(conversation_id) is closing:
                self._closing.pop(conversation_id, None)
        except Exception:
            # A teardown that raised has already logged it; either way the
            # conversation is gone and the next turn is a cold start.
            logger.debug("The idle close of conversation %s failed", conversation_id, exc_info=True)

    # --------------------------------------------------------- session state

    def _state_path(self) -> Path | None:
        if self._explicit_state_path is not None:
            return self._explicit_state_path
        if not self._agent_id:
            return None
        base = os.environ.get("XDG_STATE_HOME")
        if not base:
            base = os.path.join(os.path.expanduser("~"), ".local", "state")
        safe = "".join(c if c.isalnum() or c in "-_." else "_" for c in self._agent_id)
        return Path(base) / "paca-acp-bridge" / f"{safe}.json"

    def _load_sessions(self) -> dict[str, dict[str, Any]]:
        if self._sessions is not None:
            return self._sessions
        self._sessions = {}
        path = self._state_path()
        if path is None:
            logger.warning(
                "No agent id, so ACP session ids cannot be remembered; "
                "a closed conversation will start a fresh session"
            )
            return self._sessions
        try:
            raw = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            return self._sessions
        except Exception:
            logger.warning("Could not read %s; no saved sessions", path, exc_info=True)
            return self._sessions
        sessions = raw.get("sessions") if isinstance(raw, dict) else None
        if not isinstance(sessions, dict):
            return self._sessions
        for conversation_id, entry in sessions.items():
            if isinstance(entry, dict) and isinstance(entry.get("acp_session_id"), str):
                self._sessions[str(conversation_id)] = dict(entry)
        logger.info("Loaded %d saved ACP session id(s) from %s", len(self._sessions), path)
        return self._sessions

    def _write_sessions(self) -> None:
        path = self._state_path()
        if path is None or self._sessions is None:
            return
        sessions = self._sessions
        if len(sessions) > _MAX_REMEMBERED_SESSIONS:
            ordered = sorted(
                sessions.items(),
                key=lambda kv: kv[1].get("updated_at", 0.0),
                reverse=True,
            )
            sessions = dict(ordered[:_MAX_REMEMBERED_SESSIONS])
            self._sessions = sessions
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            # mkstemp creates the file 0600, and the rename carries that mode
            # onto the state file — the ACP session id is a resume credential.
            fd, tmp = tempfile.mkstemp(dir=str(path.parent), prefix=f"{path.name}.", suffix=".tmp")
            try:
                with os.fdopen(fd, "w", encoding="utf-8") as fh:
                    json.dump({"version": 1, "sessions": sessions}, fh)
                    fh.flush()
                    os.fsync(fh.fileno())
                os.replace(tmp, path)
            except Exception:
                with contextlib.suppress(OSError):
                    os.unlink(tmp)
                raise
        except Exception:
            logger.warning("Could not save ACP session ids to %s", path, exc_info=True)

    def _saved_session_id(self, conversation_id: str) -> str | None:
        entry = self._load_sessions().get(conversation_id)
        if entry is None:
            return None
        session_id = entry.get("acp_session_id")
        return session_id if isinstance(session_id, str) and session_id else None

    def _remember_session_id(self, conversation_id: str, conversation: Conversation) -> None:
        """Save this conversation's ACP session id after the turn.

        The SDK writes it into `state.agent_state` as soon as the session is
        created, so the first turn already has one; saving after every turn also
        picks up the new id whenever the SDK had to fall back to session/new.
        """
        session_id = self._session_id_of(conversation)
        if session_id is None:
            logger.debug("Conversation %s has no ACP session id", conversation_id)
            return
        sessions = self._load_sessions()
        known = sessions.get(conversation_id, {}).get("acp_session_id")
        sessions[conversation_id] = {
            "acp_session_id": session_id,
            "updated_at": time.time(),
        }
        if known != session_id:
            logger.info("Remembering the ACP session of conversation %s", conversation_id)
        self._write_sessions()

    def _forget_session_id(self, conversation_id: str) -> None:
        if self._load_sessions().pop(conversation_id, None) is not None:
            self._write_sessions()

    # ------------------------------------------------------------ reporting

    async def _report_status_safely(
        self, conversation_id: str, project_id: str, status: str, error_message: str | None = None
    ) -> None:
        # A failure to *report* status (e.g. the bridge's outbox raising
        # during shutdown) must not be conflated with the conversation
        # itself failing — isolated here so it can only ever produce a log
        # line, never a misleading "failed" status for a turn that actually
        # ran fine (or a second, unhandled exception out of the task).
        try:
            await self._report_status(conversation_id, project_id, status, error_message)
        except Exception:
            logger.warning(
                "Failed to report status for conversation %s", conversation_id, exc_info=True
            )

    def _make_event_callback(self, conversation_id: str, project_id: str) -> Callable[[Any], None]:
        def callback(event: Any) -> None:
            event_type = type(event).__name__
            try:
                payload = event.model_dump_json() if hasattr(event, "model_dump_json") else "{}"
                message = {
                    "type": "event",
                    "conversation_id": conversation_id,
                    "project_id": project_id,
                    "event_type": event_type,
                    "event_source": str(getattr(event, "source", "agent")),
                    "payload": payload,
                }
                try:
                    called_from_our_loop = asyncio.get_running_loop() is self._loop
                except RuntimeError:
                    called_from_our_loop = False
                if called_from_our_loop:
                    # Most on_event calls now happen this way: arun() runs as
                    # a task directly on this daemon's own loop, and things
                    # like finalizing a turn or emitting InterruptEvent on
                    # cancellation call back into this callback synchronously
                    # from that same task. Blocking here with
                    # run_coroutine_threadsafe(...).result() would deadlock —
                    # the loop can't run the coroutine it just scheduled
                    # while its own thread is stuck waiting on it (always
                    # timing out after the full 10s). Just schedule it — but
                    # still log if _send ends up failing, same as the
                    # cross-thread branch below, rather than letting it
                    # become a silent unretrieved-exception warning.
                    def _log_send_failure(
                        task: asyncio.Task[None],
                        _event_type: str = event_type,
                        _conversation_id: str = conversation_id,
                    ) -> None:
                        if task.cancelled():
                            return
                        exc = task.exception()
                        if exc is not None:
                            logger.warning(
                                "Failed to report event %s for conversation %s",
                                _event_type,
                                _conversation_id,
                                exc_info=exc,
                            )

                    asyncio.get_running_loop().create_task(self._send(message)).add_done_callback(
                        _log_send_failure
                    )
                else:
                    # Mid-turn ACP streaming updates fire from ACPAgent's own
                    # background ("portal") thread, so this hop really is
                    # cross-thread there, and safe to wait on.
                    future = asyncio.run_coroutine_threadsafe(self._send(message), self._loop)
                    future.result(timeout=10)
            except Exception:
                logger.warning(
                    "Failed to report event %s for conversation %s",
                    event_type,
                    conversation_id,
                    exc_info=True,
                )

        return callback

    async def _report_status(
        self, conversation_id: str, project_id: str, status: str, error_message: str | None = None
    ) -> None:
        message: dict[str, Any] = {
            "type": "turn_status",
            "conversation_id": conversation_id,
            "project_id": project_id,
            "status": status,
        }
        if error_message:
            message["error_message"] = error_message
        await self._send(message)
