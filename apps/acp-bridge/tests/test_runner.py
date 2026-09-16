"""Tests for command resolution and message-driven behavior of ConversationRunner.

Exercises the daemon's own logic without spawning a real ACP CLI subprocess —
`start_turn`/`interrupt` are tested against a fake conversation object rather
than a real `openhands.sdk.Conversation`.
"""

import asyncio
import json
import logging
import os
import stat
import sys
import threading

import pytest

from paca_acp_bridge import runner as runner_module
from paca_acp_bridge.runner import ConversationRunner, resolve_acp_command


def test_resolve_builtin_provider_uses_sdk_default_command():
    command = resolve_acp_command("claude-code", [])
    assert command[:2] == ["npx", "-y"]
    # The package is matched anywhere after the npx flags, not at a fixed index:
    # openhands-sdk 1.47.0 inserts --prefer-offline before it.
    assert any("claude-agent-acp" in part for part in command[2:])


def test_resolve_custom_provider_uses_explicit_command():
    command = resolve_acp_command("custom", ["my-server", "--flag"])
    assert command == ["my-server", "--flag"]


def test_resolve_custom_provider_without_command_raises():
    with pytest.raises(ValueError):
        resolve_acp_command("custom", [])


def test_resolve_unknown_provider_falls_back_to_explicit_command():
    command = resolve_acp_command("not-a-real-provider", ["fallback-cmd"])
    assert command == ["fallback-cmd"]


async def test_interrupt_calls_conversation_interrupt_for_known_conversation():
    sent = []

    async def send(message):
        sent.append(message)

    runner = ConversationRunner(workspace="/tmp", send=send)

    class _FakeConversation:
        def __init__(self):
            self.interrupted = False

        def interrupt(self):
            self.interrupted = True

    fake_conv = _FakeConversation()
    from paca_acp_bridge.runner import _ConversationHandle

    completed_task = asyncio.get_running_loop().create_future()
    completed_task.set_result(None)
    runner._conversations["conv-1"] = _ConversationHandle(
        conversation=fake_conv,
        task=completed_task,  # type: ignore[arg-type]
    )

    runner.interrupt("conv-1")

    assert fake_conv.interrupted is True


async def test_interrupt_is_a_no_op_for_unknown_conversation():
    async def send(message):
        pass

    runner = ConversationRunner(workspace="/tmp", send=send)

    # Should not raise even though "conv-missing" was never started.
    runner.interrupt("conv-missing")
    runner.interrupt(None)


async def test_start_turn_reports_failure_for_unresolvable_custom_provider():
    sent = []

    async def send(message):
        sent.append(message)

    runner = ConversationRunner(workspace="/tmp", send=send)

    await runner.start_turn(
        {
            "conversation_id": "conv-1",
            "project_id": "proj-1",
            "message": "hi",
            "acp_provider": "custom",
            "acp_command": [],
        }
    )

    assert len(sent) == 1
    assert sent[0]["type"] == "turn_status"
    assert sent[0]["status"] == "failed"
    assert "conv-1" not in runner._conversations


async def test_start_turn_rejects_resume_while_previous_turn_still_running():
    sent = []

    async def send(message):
        sent.append(message)

    runner = ConversationRunner(workspace="/tmp", send=send)

    class _FakeConversation:
        pass

    class _FakeRunningTask:
        def done(self):
            return False

    from paca_acp_bridge.runner import _ConversationHandle

    fake_conv = _FakeConversation()
    runner._conversations["conv-1"] = _ConversationHandle(
        conversation=fake_conv,
        task=_FakeRunningTask(),  # type: ignore[arg-type]
    )

    await runner.start_turn(
        {
            "conversation_id": "conv-1",
            "project_id": "proj-1",
            "message": "a follow-up message",
        }
    )

    assert len(sent) == 1
    assert sent[0]["type"] == "turn_status"
    assert sent[0]["status"] == "failed"
    # The still-running conversation's handle must be left untouched — no
    # second task should have been started against it.
    assert runner._conversations["conv-1"].conversation is fake_conv


async def test_start_turn_resume_drives_conversation_via_arun_and_reports_finished():
    """Locks in the arun()-based (not run()-on-a-thread) execution path:
    conversation.interrupt() can only cancel a turn immediately if the turn
    is actually tracked as an asyncio Task via arun() — see runner.py's
    module docstring for why the old thread+run() model couldn't do this."""
    sent = []

    async def send(message):
        sent.append(message)

    runner = ConversationRunner(workspace="/tmp", send=send)

    class _FakeConversation:
        def __init__(self):
            self.sent_messages: list[str] = []
            self.ran = False

        def send_message(self, message):
            self.sent_messages.append(message)

        async def arun(self):
            self.ran = True

    fake_conv = _FakeConversation()
    from paca_acp_bridge.runner import _ConversationHandle

    done_task = asyncio.get_running_loop().create_future()
    done_task.set_result(None)
    runner._conversations["conv-1"] = _ConversationHandle(
        conversation=fake_conv,
        task=done_task,  # type: ignore[arg-type]
    )

    await runner.start_turn(
        {"conversation_id": "conv-1", "project_id": "proj-1", "message": "a follow-up message"}
    )
    await runner._conversations["conv-1"].task

    assert fake_conv.sent_messages == ["a follow-up message"]
    assert fake_conv.ran is True
    assert sent == [
        {
            "type": "turn_status",
            "conversation_id": "conv-1",
            "project_id": "proj-1",
            "status": "finished",
        }
    ]


class _FakeEvent:
    def model_dump_json(self):
        return "{}"


async def test_event_callback_schedules_without_blocking_when_called_on_loop():
    """arun()'s own on_event calls (finalizing a turn, emitting InterruptEvent
    on cancellation) happen synchronously on this daemon's own event-loop
    thread. Blocking there with run_coroutine_threadsafe(...).result() would
    make the loop wait on a coroutine it can only run once this call
    returns — a guaranteed 10s self-deadlock. Calling the callback directly
    (as arun() does) must return immediately instead."""
    sent = []

    async def send(message):
        sent.append(message)

    runner = ConversationRunner(workspace="/tmp", send=send)
    runner._loop = asyncio.get_running_loop()
    callback = runner._make_event_callback("conv-1", "proj-1")

    callback(_FakeEvent())  # must not block waiting for _send to run

    await asyncio.sleep(0)  # let the scheduled task actually run

    assert len(sent) == 1
    assert sent[0]["conversation_id"] == "conv-1"
    assert sent[0]["event_type"] == "_FakeEvent"


async def test_event_callback_uses_threadsafe_dispatch_when_called_off_loop():
    """ACPAgent streams some mid-turn updates from its own background
    ("portal") thread — a genuinely different thread than this daemon's
    loop — where the blocking run_coroutine_threadsafe(...).result() dispatch
    is still correct and necessary."""
    sent = []

    async def send(message):
        sent.append(message)

    runner = ConversationRunner(workspace="/tmp", send=send)
    runner._loop = asyncio.get_running_loop()
    callback = runner._make_event_callback("conv-1", "proj-1")

    thread = threading.Thread(target=callback, args=(_FakeEvent(),))
    thread.start()
    for _ in range(200):
        if not thread.is_alive():
            break
        await asyncio.sleep(0.01)

    assert not thread.is_alive()
    assert len(sent) == 1
    assert sent[0]["conversation_id"] == "conv-1"


async def test_run_conversation_survives_status_report_failure_after_success():
    """A failure in the outbound _send/report path (e.g. the bridge's outbox
    raising during shutdown) must not be conflated with the conversation
    itself failing — see _report_status_safely in runner.py. Locks in that
    a successful arun() only ever attempts one status report, even if that
    report itself blows up."""
    calls = []

    async def send(message):
        calls.append(message)
        raise RuntimeError("outbox closed")

    runner = ConversationRunner(workspace="/tmp", send=send)

    class _FakeConversation:
        def send_message(self, message):
            pass

        async def arun(self):
            pass

    # Must not raise, even though `send` always raises.
    await runner._run_conversation(_FakeConversation(), "conv-1", "proj-1", "hi")

    assert len(calls) == 1
    assert calls[0]["status"] == "finished"


async def test_event_callback_logs_when_send_fails_on_loop(caplog):
    """The same-loop dispatch branch schedules _send as a fire-and-forget
    task; a failure there must still surface as a logged warning instead of
    silently becoming an unretrieved-task-exception."""

    async def send(message):
        raise RuntimeError("boom")

    runner = ConversationRunner(workspace="/tmp", send=send)
    runner._loop = asyncio.get_running_loop()
    callback = runner._make_event_callback("conv-1", "proj-1")

    with caplog.at_level(logging.WARNING, logger="paca_acp_bridge.runner"):
        callback(_FakeEvent())
        # Two yields: one for the scheduled _send task to run and raise, a
        # second for its add_done_callback (scheduled once the task becomes
        # done) to actually fire.
        await asyncio.sleep(0)
        await asyncio.sleep(0)

    assert any("Failed to report event" in record.getMessage() for record in caplog.records)


# --------------------------------------------------------------------------- #
# Idle close + resume
#
# Nothing in the protocol ever ends a conversation, so without the idle timer
# every card wake leaves a live ACP adapter + claude + MCP servers behind for
# the lifetime of the unit. These lock in that a finished conversation is torn
# down after N minutes, that a running one never is, and that the chat comes
# back with its context because the ACP session id was saved and replayed.
# --------------------------------------------------------------------------- #


class _FakeTimer:
    def __init__(self, delay, callback):
        self.delay = delay
        self.callback = callback
        self.cancelled = False
        self.fired = False

    def cancel(self):
        self.cancelled = True

    def fire(self):
        self.fired = True
        self.callback()


class _FakeClock:
    """The injected clock: timers fire when the test says so, not after 10
    real minutes."""

    def __init__(self):
        self.timers = []

    def call_later(self, delay, callback):
        timer = _FakeTimer(delay, callback)
        self.timers.append(timer)
        return timer

    def advance(self, seconds):
        for timer in list(self.timers):
            if timer.cancelled or timer.fired:
                continue
            if timer.delay <= seconds:
                timer.fire()


class _FakeAgent:
    def __init__(self, **kwargs):
        self.kwargs = kwargs
        self._process = None


class _FakeState:
    def __init__(self, session_id):
        self.agent_state = {"acp_session_id": session_id}


class _FakeConversation:
    def __init__(self, sdk, agent, session_id):
        self.sdk = sdk
        self.agent = agent
        self.state = _FakeState(session_id)
        self.messages = []
        self.closed = 0
        self.hold = None

    def send_message(self, message):
        self.messages.append(message)

    async def arun(self):
        if self.hold is not None:
            await self.hold.wait()
        if self.sdk.fail_all:
            raise RuntimeError("the turn blew up")
        if self.sdk.fail_resume and self.agent.kwargs.get("acp_resume_session_id"):
            raise RuntimeError("session/load rejected")

    def close(self):
        self.closed += 1
        if self.sdk.close_gate is not None:
            self.sdk.close_gate.wait(10)
        if self.sdk.close_error:
            raise RuntimeError("close blew up")


class _FakeSdk:
    """Stands in for ACPAgent/Conversation so the tests never spawn a CLI."""

    def __init__(self, *, session_ids=None, fail_resume=False, fail_all=False,
                 close_error=False, close_gate=None):
        self.agents = []
        self.conversations = []
        self._session_ids = list(session_ids or [])
        self.fail_resume = fail_resume
        self.fail_all = fail_all
        self.close_error = close_error
        self.close_gate = close_gate

    def _agent(self, **kwargs):
        agent = _FakeAgent(**kwargs)
        self.agents.append(agent)
        return agent

    def _conversation(self, agent=None, workspace=None, callbacks=None, **kwargs):
        if self._session_ids:
            session_id = self._session_ids.pop(0)
        else:
            session_id = f"sess-{len(self.conversations) + 1}"
        conversation = _FakeConversation(self, agent, session_id)
        self.conversations.append(conversation)
        return conversation

    def install(self, monkeypatch):
        monkeypatch.setattr(runner_module, "ACPAgent", self._agent)
        monkeypatch.setattr(runner_module, "Conversation", self._conversation)


def _sender(sent):
    async def send(message):
        sent.append(message)

    return send


def _turn(conversation_id, message="hi", history=""):
    return {
        "conversation_id": conversation_id,
        "project_id": "proj-1",
        "message": message,
        "history": history,
        "acp_provider": "custom",
        "acp_command": ["fake-acp"],
    }


def _make_runner(tmp_path, sent, clock, agent_id="agent-1"):
    return ConversationRunner(
        workspace="/tmp",
        send=_sender(sent),
        agent_id=agent_id,
        state_path=tmp_path / f"{agent_id}.json",
        call_later=clock.call_later,
    )


async def _run_turn_to_completion(runner, data):
    await runner.start_turn(data)
    await runner._conversations[data["conversation_id"]].task


async def _drain_closes(runner):
    for task in list(runner._closing.values()):
        await task


async def test_idle_timer_closes_a_finished_conversation_after_n_minutes(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    sdk = _FakeSdk()
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))
    conversation = sdk.conversations[0]

    # Exactly one timer, armed for the default 10 minutes, and nothing closed yet.
    assert len(clock.timers) == 1
    assert clock.timers[0].delay == 600.0
    assert conversation.closed == 0

    clock.advance(600.0)
    await _drain_closes(runner)

    assert conversation.closed == 1
    assert "conv-1" not in runner._conversations
    assert runner._closing == {}


async def test_idle_minutes_comes_from_the_environment(tmp_path, monkeypatch):
    monkeypatch.setenv("PACA_SESSION_IDLE_MINUTES", "2")
    sdk = _FakeSdk()
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))

    assert clock.timers[0].delay == 120.0


async def test_idle_minutes_zero_closes_right_at_the_end_of_the_turn(tmp_path, monkeypatch):
    monkeypatch.setenv("PACA_SESSION_IDLE_MINUTES", "0")
    sdk = _FakeSdk()
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))
    assert clock.timers[0].delay == 0.0

    clock.advance(0.0)
    await _drain_closes(runner)

    assert sdk.conversations[0].closed == 1


async def test_a_bad_idle_minutes_value_falls_back_to_the_default(monkeypatch):
    monkeypatch.setenv("PACA_SESSION_IDLE_MINUTES", "not-a-number")
    assert runner_module.idle_seconds() == 600.0
    monkeypatch.setenv("PACA_SESSION_IDLE_MINUTES", "-5")
    assert runner_module.idle_seconds() == 600.0


async def test_idle_timer_never_closes_a_conversation_whose_turn_is_running(
    tmp_path, monkeypatch
):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    sdk = _FakeSdk()
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))
    conversation = sdk.conversations[0]
    stale_timer = clock.timers[0]

    # A second turn arrives and is still running.
    conversation.hold = asyncio.Event()
    await runner.start_turn(_turn("conv-1", "second"))
    assert stale_timer.cancelled is True

    # Even if that already-cancelled timer fires anyway (it raced the cancel),
    # a conversation with a live turn is never torn down under it.
    stale_timer.fire()
    assert conversation.closed == 0
    assert "conv-1" in runner._conversations
    assert runner._closing == {}

    conversation.hold.set()
    await runner._conversations["conv-1"].task
    # The finished turn arms a fresh timer instead.
    assert [t for t in clock.timers if not t.cancelled and not t.fired]


async def test_next_turn_after_an_idle_close_resumes_the_saved_acp_session(
    tmp_path, monkeypatch
):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    sdk = _FakeSdk(session_ids=["sess-a", "sess-a"])
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1", "first", history="U: earlier"))
    # A first, unresumable start replays the history it was handed.
    assert "acp_resume_session_id" not in sdk.agents[0].kwargs
    assert "Restored conversation context" in sdk.conversations[0].messages[0]

    clock.advance(600.0)
    await _drain_closes(runner)
    assert sdk.conversations[0].closed == 1

    await _run_turn_to_completion(runner, _turn("conv-1", "second", history="U: earlier"))

    assert len(sdk.agents) == 2
    assert sdk.agents[1].kwargs["acp_resume_session_id"] == "sess-a"
    # session/load brings the context back, so the transcript is not replayed
    # into the prompt on top of it.
    assert sdk.conversations[1].messages == ["second"]
    assert [m["status"] for m in sent if m["type"] == "turn_status"] == [
        "finished",
        "finished",
    ]


async def test_a_restarted_runner_resumes_from_the_state_file(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    sdk = _FakeSdk(session_ids=["sess-from-disk"])
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []

    first = _make_runner(tmp_path, sent, clock)
    await _run_turn_to_completion(first, _turn("conv-1"))

    state = json.loads((tmp_path / "agent-1.json").read_text(encoding="utf-8"))
    assert state["sessions"]["conv-1"]["acp_session_id"] == "sess-from-disk"

    # A brand-new runner — the daemon restarted, nothing in memory.
    second = _make_runner(tmp_path, [], _FakeClock())
    await _run_turn_to_completion(second, _turn("conv-1", "after the restart"))

    assert sdk.agents[1].kwargs["acp_resume_session_id"] == "sess-from-disk"


async def test_the_state_file_is_private_to_its_owner(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    sdk = _FakeSdk()
    sdk.install(monkeypatch)
    runner = _make_runner(tmp_path, [], _FakeClock())

    await _run_turn_to_completion(runner, _turn("conv-1"))

    path = tmp_path / "agent-1.json"
    assert path.exists()
    if os.name == "posix":
        assert stat.S_IMODE(path.stat().st_mode) == 0o600


async def test_start_turn_waits_for_an_in_flight_close(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    gate = threading.Event()
    sdk = _FakeSdk(close_gate=gate)
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))
    clock.advance(600.0)
    close_task = runner._closing["conv-1"]

    pending = asyncio.create_task(runner.start_turn(_turn("conv-1", "second")))
    for _ in range(20):
        await asyncio.sleep(0.01)
    # Still waiting on the teardown — no second conversation has been built.
    assert not pending.done()
    assert len(sdk.conversations) == 1

    gate.set()
    await pending
    await runner._conversations["conv-1"].task

    assert close_task.done()
    assert len(sdk.conversations) == 2


async def test_a_failing_close_does_not_stop_later_idle_timers(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    sdk = _FakeSdk(close_error=True)
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))
    await _run_turn_to_completion(runner, _turn("conv-2"))
    first_timer, second_timer = clock.timers[0], clock.timers[1]

    first_timer.fire()
    await _drain_closes(runner)
    assert sdk.conversations[0].closed == 1
    assert runner._closing == {}

    # The next conversation's timer still fires and still closes it.
    second_timer.fire()
    await _drain_closes(runner)
    assert sdk.conversations[1].closed == 1
    assert runner._conversations == {}


async def test_a_failed_resume_starts_a_fresh_session_once(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    (tmp_path / "agent-1.json").write_text(
        json.dumps(
            {"version": 1, "sessions": {"conv-1": {"acp_session_id": "gone", "updated_at": 1}}}
        ),
        encoding="utf-8",
    )
    sdk = _FakeSdk(fail_resume=True, session_ids=["gone", "fresh-sess"])
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1", "hello", history="U: earlier"))

    # Tried the saved id once, then exactly one fresh session — not a retry loop.
    assert len(sdk.agents) == 2
    assert sdk.agents[0].kwargs["acp_resume_session_id"] == "gone"
    assert "acp_resume_session_id" not in sdk.agents[1].kwargs
    # The conversation that could not resume was torn down, not left running.
    assert sdk.conversations[0].closed == 1
    # Falling back means falling back to the history-replay path.
    assert "Restored conversation context" in sdk.conversations[1].messages[0]
    assert [m["status"] for m in sent if m["type"] == "turn_status"] == ["finished"]

    saved = json.loads((tmp_path / "agent-1.json").read_text(encoding="utf-8"))
    assert saved["sessions"]["conv-1"]["acp_session_id"] == "fresh-sess"


async def test_a_fresh_start_that_also_fails_is_reported_not_retried(tmp_path, monkeypatch):
    monkeypatch.delenv("PACA_SESSION_IDLE_MINUTES", raising=False)
    (tmp_path / "agent-1.json").write_text(
        json.dumps(
            {"version": 1, "sessions": {"conv-1": {"acp_session_id": "gone", "updated_at": 1}}}
        ),
        encoding="utf-8",
    )
    sdk = _FakeSdk(fail_all=True)
    sdk.install(monkeypatch)
    clock, sent = _FakeClock(), []
    runner = _make_runner(tmp_path, sent, clock)

    await _run_turn_to_completion(runner, _turn("conv-1"))

    assert len(sdk.agents) == 2
    statuses = [m["status"] for m in sent if m["type"] == "turn_status"]
    assert statuses == ["failed"]


@pytest.mark.skipif(not os.path.isdir("/proc"), reason="the process-tree walk needs /proc")
async def test_teardown_removes_a_whole_three_level_process_tree():
    """The SDK's own close() only terminates the ACP adapter, leaving `claude`
    and its MCP servers running - that is the leak this replaces. Here the whole
    tree must be gone, including the grandchild that gets reparented away the
    moment its parent dies."""
    leaf = "import time; time.sleep(600)"
    mid = (
        "import subprocess, sys, time; "
        f"subprocess.Popen([sys.executable, '-c', {leaf!r}]); "
        "time.sleep(600)"
    )
    root = (
        "import subprocess, sys; "
        f"subprocess.Popen([sys.executable, '-c', {mid!r}]); "
        "sys.stdin.read()"
    )
    process = await asyncio.create_subprocess_exec(
        sys.executable, "-c", root, stdin=asyncio.subprocess.PIPE
    )
    try:
        tree = {}
        for _ in range(200):
            tree = runner_module._process_tree(process.pid)
            if len(tree) >= 3:
                break
            await asyncio.sleep(0.05)
        assert len(tree) >= 3, f"tree never grew to three levels: {tree}"

        await runner_module.shutdown_process_tree(
            process, grace_seconds=1.0, poll_seconds=0.05
        )
        await process.wait()

        for _ in range(100):
            if not runner_module._still_alive(tree):
                break
            await asyncio.sleep(0.05)
        assert runner_module._still_alive(tree) == []
    finally:
        if process.returncode is None:
            process.kill()
            await process.wait()
