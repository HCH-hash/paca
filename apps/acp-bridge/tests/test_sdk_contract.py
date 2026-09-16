"""The private openhands-sdk API this daemon depends on, asserted against the
real pinned SDK.

None of this is public API, and pyproject pins `openhands-sdk==1.47.0` for
exactly that reason. The failure mode these guard against is silent: if one of
these handles disappears or is renamed in a later version, `runner.py` keeps
importing, keeps passing every test that uses the fake SDK, and simply stops
doing its job on the box — the process tree is never stopped (the leak comes
back), or a resume that did not take is never noticed (the chat loses its
context). Assert them here so a version bump breaks the build instead.
"""

import inspect

from openhands.sdk import LocalConversation
from openhands.sdk.agent import ACPAgent


def test_acpagent_still_exposes_the_adapter_process_handle():
    """`runner._teardown` reaches `conversation.agent._process` to walk and stop
    the whole ACP process tree. The SDK's own close() only terminates the
    adapter, leaving `claude` and its MCP servers running — that leak is the
    reason this daemon exists."""
    assert "_process" in ACPAgent.__private_attributes__


def test_acpagent_still_accepts_an_explicit_resume_session_id():
    """`runner._build_conversation` sets this to make the SDK call session/load
    (for the Claude adapter: `claude --resume <id>`) instead of session/new."""
    assert "acp_resume_session_id" in ACPAgent.model_fields


def test_acpagent_still_records_the_live_session_id_in_agent_state():
    """`runner._session_id_of` reads `state.agent_state["acp_session_id"]` —
    both to remember the session for the next cold start and, after a resume, to
    tell whether the resume actually took."""
    assert '"acp_session_id"' in inspect.getsource(ACPAgent.init_state)


def test_a_rejected_session_load_still_does_not_raise():
    """The reason `_run_resumed_turn` compares session ids rather than waiting
    for an exception: the SDK catches the protocol-level rejection and quietly
    starts a fresh, empty session. If a later version re-raises instead, the
    comparison stays correct but the failure arm in `_run_cold_start` becomes
    the live path again — worth knowing about."""
    source = inspect.getsource(ACPAgent._start_acp_server)
    assert "load_session" in source
    assert "except ACPRequestError" in source
    assert "starting a fresh session" in source


def test_conversation_still_exposes_the_lazy_init_arun_runs():
    """`runner._start_session` runs this before sending the message, so the
    session id is readable while the turn can still carry the transcript. It is
    what `arun()` itself awaits first, and is idempotent, so the turn's own
    `arun()` skips it."""
    assert callable(LocalConversation._ensure_agent_ready)
    assert "_ensure_agent_ready" in inspect.getsource(LocalConversation.arun)
