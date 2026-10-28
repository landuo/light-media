#!/usr/bin/env python3
"""Execute fixed original EventManager target methods with an owned executor.

This verifies source filtering, top-level copy/removal, and admission-time versus
execution-time enabled checks. The executor and running objects are captured
stubs, not evidence of Go IPC, authentication, or real plugin behavior.
"""
from __future__ import annotations
import ast
import copy
import enum
import hashlib
import inspect
import json
from pathlib import Path
from types import SimpleNamespace, ModuleType
import uuid
import sys
import time

HERE = Path(__file__).resolve().parent
REF = Path(sys.argv[1]) if len(sys.argv) > 1 else HERE.parent / "standard-command-ingress/reference"
source = (REF / "event.py").read_bytes()
blob = hashlib.sha1(b"blob " + str(len(source)).encode() + b"\0" + source).hexdigest()
assert blob == "9186902a9f42f1021dea7cf5e15f15775d236deb"
types_source = (REF / "schema-types.py").read_bytes()
assert hashlib.sha1(b"blob " + str(len(types_source)).encode() + b"\0" + types_source).hexdigest() == "fafd27cd5fd44d075ae541d55089a87d32e83bbc"


def methods(class_name, names):
    cls = next(n for n in ast.parse(source).body if isinstance(n, ast.ClassDef) and n.name == class_name)
    selected = [copy.deepcopy(n) for n in cls.body if isinstance(n, ast.FunctionDef) and n.name in names]
    assert {n.name for n in selected} == set(names)
    return ast.ClassDef(name=class_name, bases=[], keywords=[], body=selected, decorator_list=[])


enum_nodes = [copy.deepcopy(next(n for n in ast.parse(types_source).body if isinstance(n, ast.ClassDef) and n.name == name)) for name in ("EventType", "ChainEventType")]
body = [ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *enum_nodes,
        methods("Event", ["__init__"]),
        methods("EventManager", ["__dispatch_broadcast_event", "__dispatch_chain_event", "__invoke_handler_by_type_sync", "__should_dispatch_to_target_plugin", "__parse_handler_names", "__get_handler_identifier", "__get_class_from_callable", "__is_handler_enabled", "__safe_invoke_handler"])]
ns = {"Enum": enum.Enum, "uuid": uuid, "inspect": inspect, "time": time, "DEFAULT_EVENT_PRIORITY": 10,
      "logger": SimpleNamespace(debug=lambda *a, **k: None)}
exec(compile(ast.fix_missing_locations(ast.Module(body=body, type_ignores=[])), "pinned-event-target", "exec"), ns)
Event, Manager, EventType = ns["Event"], ns["EventManager"], ns["EventType"]


class Queue:
    def __init__(self): self.calls = []
    def submit(self, fn, *args): self.calls.append((fn, args))
    def drain(self):
        for fn, args in self.calls: fn(*args)
        self.calls.clear()


def make_handler(name):
    def event(self, ev): pass
    event.__qualname__ = name + ".event"
    cls = type(name, (), {"event": event})
    return cls().event


def prepare():
    manager = Manager()
    manager._EventManager__executor = Queue()
    manager._EventManager__disabled_handlers = set()
    manager._EventManager__disabled_classes = set()
    manager._EventManager__broadcast_subscribers = {}
    manager.seen = []
    def invoke(handler, event): manager.seen.append((handler.__self__.__class__.__name__, event))
    manager._EventManager__invoke_handler_by_type_sync = invoke
    handlers = [make_handler(n) for n in ("OwnedEventTargetFixture", "OwnedEventTargetFixturetwin", "OwnedEventTargetFixtureother")]
    for kind in (EventType.MessageAction, EventType.PluginAction):
        manager._EventManager__broadcast_subscribers[kind] = {manager._EventManager__get_handler_identifier(h): h for h in handlers}
    return manager, handlers


cases = []
raw_values = [None, 'null', 'false', '0', '-0', '0.0', '""', '[]', '{}', '1e-10000',
              '"OwnedEventTargetFixture"', '"OwnedEventTargetFixturetwin"', '"UnknownOwnedTarget"',
              '"ownedeventtargetfixture"', 'true', '9007199254740993', '0.01',
              '["OwnedEventTargetFixture"]', '{"id":"OwnedEventTargetFixture"}', '1e400', '-1e400']
for raw in raw_values:
    for kind in (EventType.MessageAction, EventType.PluginAction):
        manager, handlers = prepare()
        caller = {"big": 9007199254740993, "nested": {"__mp_target_plugin_id": "keep", "value": 9007199254740995}, "false": False, "null": None}
        if raw is not None: caller["__mp_target_plugin_id"] = json.loads(raw)
        before = copy.deepcopy(caller)
        manager._EventManager__dispatch_broadcast_event(Event(kind, caller))
        manager._EventManager__executor.drain()
        assert caller == before
        for _, delivered in manager.seen:
            assert delivered.event_data is not caller
            assert "__mp_target_plugin_id" not in delivered.event_data
            assert delivered.event_data["nested"] is caller["nested"]
            assert delivered.event_data["big"] == 9007199254740993
        names = [name for name, _ in manager.seen]
        if kind == EventType.PluginAction or raw is None or not json.loads(raw):
            assert names == ["OwnedEventTargetFixture", "OwnedEventTargetFixturetwin", "OwnedEventTargetFixtureother"]
        else:
            expected = str(json.loads(raw))
            assert names == ([expected] if expected in ["OwnedEventTargetFixture", "OwnedEventTargetFixturetwin", "OwnedEventTargetFixtureother"] else [])
        cases.append({"event": kind.value, "target_json": raw, "recipients": names, "caller_unchanged": True, "nested_reference_shared_in_python": True})

# A source handler identity mismatch is rejected rather than trusting class name
# alone. Go uses only its manager-bound loaded object and registered SDK method.
manager, handlers = prepare()
manager._EventManager__broadcast_subscribers[EventType.MessageAction] = {"invalid.binding": handlers[0]}
manager._EventManager__dispatch_broadcast_event(Event(EventType.MessageAction, {"__mp_target_plugin_id": "OwnedEventTargetFixture"}))
manager._EventManager__executor.drain()
assert manager.seen == []

# Source enabled is manager state, checked just before invocation, not get_state
# called for every event.
manager, handlers = prepare()
manager._EventManager__dispatch_broadcast_event(Event(EventType.PluginAction, {"marker": "queued"}))
manager._EventManager__disabled_handlers.add(manager._EventManager__get_handler_identifier(handlers[0]))
manager._EventManager__executor.drain()
assert [n for n, _ in manager.seen] == ["OwnedEventTargetFixturetwin", "OwnedEventTargetFixtureother"]

# The synchronous chain first snapshots enabled handlers, then its safe wrapper
# rechecks each handler. A handler enabled only after the snapshot stays excluded.
manager, handlers = prepare()
chain_kind = ns["ChainEventType"].PluginDataReset
manager._EventManager__chain_subscribers = {
    chain_kind: {manager._EventManager__get_handler_identifier(h): (i, h) for i, h in enumerate(handlers)}
}
manager._EventManager__log_event_lifecycle = lambda *args: None
manager._EventManager__disabled_handlers.add(manager._EventManager__get_handler_identifier(handlers[2]))
def invoke_chain(handler, event):
    manager.seen.append((handler.__self__.__class__.__name__, event.event_data))
    if handler == handlers[0]:
        manager._EventManager__disabled_handlers.add(manager._EventManager__get_handler_identifier(handlers[1]))
        manager._EventManager__disabled_handlers.discard(manager._EventManager__get_handler_identifier(handlers[2]))
        event.event_data = None
manager._EventManager__invoke_handler_by_type_sync = invoke_chain
chain = Event(chain_kind, False)
assert chain.event_data == {}
assert manager._EventManager__dispatch_chain_event(chain) is True
assert manager.seen == [("OwnedEventTargetFixture", {})]
assert chain.event_data is None

constructor_cases = []
for raw in ('null', 'false', '0', '-0.0', '1e-10000', '""', '[]', '{}', '9007199254740993123456789', '[false]'):
    event = Event(EventType.PluginAction, json.loads(raw))
    constructor_cases.append({"input_json": raw, "data": event.event_data})

# A submitted callback resolves the current loaded class at execution time.
# The old Python callable supplies the class/method names, never old authority.
manager, handlers = prepare()
del manager.__dict__["_EventManager__invoke_handler_by_type_sync"]
observed = []
def replacement_event(self, event): observed.append("replacement")
replacement = type("OwnedEventTargetFixture", (), {"event": replacement_event})()
plugin_manager = SimpleNamespace(get_plugin_ids=lambda: ["OwnedEventTargetFixture"], running_plugins={"OwnedEventTargetFixture": replacement})
plugin_module = ModuleType("app.core.plugin")
plugin_module.PluginManager = lambda: plugin_manager
module_module = ModuleType("app.core.module")
module_module.ModuleManager = lambda: SimpleNamespace(get_module_ids=lambda: [])
sys.modules["app.core.plugin"] = plugin_module
sys.modules["app.core.module"] = module_module
manager._EventManager__dispatch_broadcast_event(Event(EventType.MessageAction, {"__mp_target_plugin_id": "OwnedEventTargetFixture"}))
manager._EventManager__executor.drain()
assert observed == ["replacement"]
plugin_manager.running_plugins.clear()
manager._EventManager__dispatch_broadcast_event(Event(EventType.MessageAction, {"__mp_target_plugin_id": "OwnedEventTargetFixture"}))
manager._EventManager__executor.drain()
assert observed == ["replacement"]

print(json.dumps({"source_commit": "8a83efeacd3e26b82be495e17d50146e31c96a60", "event_blob": blob,
                  "cases": cases, "invalid_binding_rejected": True, "disable_after_submission_skips_at_execution": True,
                  "chain_enabled_snapshot_rechecked_before_each_handler": True, "chain_later_falsey_mutation_preserved": True,
                  "constructor_cases": constructor_cases,
                  "queued_callback_resolves_current_class": True, "unloaded_callback_is_noop": True,
                  "scope": "Pinned Python method execution with owned scheduler stubs; not a Go or IPC test."}, indent=2, ensure_ascii=False))
