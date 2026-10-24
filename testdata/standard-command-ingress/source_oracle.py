#!/usr/bin/env python3
"""Run fixed original Python method bodies, not a translated business model.

This owned oracle isolates only web text parsing, the MessageChain slash-command
branch, and Command registry/dispatch. Authentication dependencies and event
transport are captured stubs; they do not prove host authentication or SDK IPC.
The separate actual SDK HTTP tests provide those gates when Go is authorized.
"""
from __future__ import annotations

import ast
import asyncio
import copy
import enum
import hashlib
import inspect
import json
from pathlib import Path
import re
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
REF = HERE / "reference"


def original_class(filename, class_name, names):
    tree = ast.parse((REF / filename).read_text())
    cls = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == class_name)
    methods = [copy.deepcopy(node) for node in cls.body if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names]
    assert {node.name for node in methods} == set(names)
    for node in methods:
        node.decorator_list = [d for d in node.decorator_list if isinstance(d, ast.Name) and d.id in {"staticmethod", "classmethod"}]
    return ast.ClassDef(name=class_name, bases=[], keywords=[], body=methods, decorator_list=[])


def original_function(filename, name):
    tree = ast.parse((REF / filename).read_text())
    node = copy.deepcopy(next(node for node in tree.body if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == name))
    node.decorator_list = []
    return node


def enum_values(class_name, names):
    tree = ast.parse((REF / "schema-types.py").read_text())
    cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == class_name)
    values = {}
    for node in cls.body:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name) and target.id in names:
                    values[target.id] = ast.literal_eval(node.value)
    assert set(values) == set(names)
    return enum.Enum(class_name, values)


EventType = enum_values("EventType", {"CommandExcute", "PluginAction"})
MessageChannel = enum_values("MessageChannel", {"Web"})


def normalized(value):
    if isinstance(value, enum.Enum):
        return value.value
    if isinstance(value, dict):
        return {k: normalized(v) for k, v in value.items()}
    if isinstance(value, (tuple, list)):
        return [normalized(v) for v in value]
    return value


class Events:
    def __init__(self):
        self.command = None
        self.events = []

    def send_event(self, etype, data):
        self.events.append({"type": normalized(etype), "data": normalized(copy.deepcopy(data))})
        if etype == EventType.CommandExcute:
            self.command.command_event(SimpleNamespace(event_data=data))


events = Events()
errors = []
namespace = {
    "copy": copy,
    "re": re,
    "ObjectUtils": SimpleNamespace(arguments=lambda method: len(inspect.signature(method).parameters)),
    "eventmanager": events,
    "EventType": EventType,
    "MessageChannel": MessageChannel,
    "logger": SimpleNamespace(info=lambda *args, **kwargs: None, error=lambda *args, **kwargs: errors.append(args)),
    "_finish_command_processing_status": lambda *args, **kwargs: None,
    "Depends": lambda *args, **kwargs: None,
    "get_current_active_superuser": lambda: None,
    "schemas": SimpleNamespace(Response=lambda **kwargs: kwargs),
    "Notification": lambda **kwargs: kwargs,
}
nodes = [ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)]
nodes.append(original_class("command.py", "Command", ["__build_plugin_commands", "__run_command", "get", "execute", "send_plugin_event", "command_event"]))
nodes.append(original_class("chain-message.py", "MessageChain", ["_handle_message_core", "_strip_no_ai_prefix", "_has_ai_prefix"]))
nodes.append(original_function("api-message.py", "web_message"))
module = ast.fix_missing_locations(ast.Module(body=nodes, type_ignores=[]))
exec(compile(module, "fixed-original-command-bodies", "exec"), namespace)
Command = namespace["Command"]
Chain = namespace["MessageChain"]
chain = Chain()
Chain._ai_prefix = "/ai"
Chain._no_ai_prefix = "/noai"
chain.eventmanager = events
chain._handle_plugin_input_interaction = lambda **kwargs: False
chain._handle_ai_message = lambda **kwargs: False
chain.post_message = lambda message: None
# Web text command scope only: the product's history, media, attachment, and AI
# branches are not synthesized or claimed by this isolated method-body oracle.
chain.handle_message = lambda **kwargs: chain._handle_message_core(**{k: v for k, v in kwargs.items() if k != "images"})
namespace["MessageChain"] = lambda: chain


class Request:
    def __init__(self, payload, content_type="application/json"):
        self.headers = {"content-type": content_type}
        self.payload = payload

    async def json(self):
        return self.payload


cases = [
    {"id": "declared-event-not-action-method", "text": "/owned \x1c甲 \x85乙\n丙", "data": {"action": "not_a_method", "nested": {"values": ["kept", False, None]}, "big": 9007199254740993}},
    {"id": "no-action", "text": "/owned", "data": {"marker": "no-action", "arg_str": "preset"}},
    {"id": "null-data", "text": "/owned", "data": None},
    {"id": "hidden-still-executable", "text": "/owned", "show": False, "data": {}},
    {"id": "case-sensitive-command", "text": "/OWNED", "data": {}},
    {"id": "unknown-command", "text": "/missing", "data": {}},
    {"id": "explicit-noai", "text": " /NOAI： /owned  second\n", "data": {}},
    {"id": "query-text-fallback", "text": None, "payload": {}, "query": "/owned query argument", "data": {}},
    {"id": "body-overrides-query", "text": "/owned body", "query": "/missing", "data": {}},
    {"id": "client-identity-not-trusted", "text": "/owned", "extra": {"source": "forged", "user": "forged", "channel": "飞书", "event": "forged", "data": {"action": "forged"}}, "data": {"action": "kept"}},
    {"id": "explicit-ai-is-not-command", "text": "/AI owned", "data": {}},
]
outputs = []
for case in cases:
    events.events = []
    command = Command()
    declarations = [{"cmd": "/owned", "event": EventType.PluginAction, "data": copy.deepcopy(case["data"]), "pid": "Owned", "show": case.get("show", True)}]
    before = normalized(copy.deepcopy(declarations))
    command.pluginmanager = SimpleNamespace(get_plugin_commands=lambda: declarations)
    command.messagehelper = SimpleNamespace(put=lambda **kwargs: errors.append(kwargs))
    command._commands = command._Command__build_plugin_commands()
    events.command = command
    payload = case.get("payload", {"text": case["text"], **case.get("extra", {})})
    result = asyncio.run(namespace["web_message"](Request(payload), text=case.get("query"), current_user=SimpleNamespace(name="owned-admin")))
    assert result == {"success": True}
    assert normalized(declarations) == before, "original command declaration mutated"
    assert not errors, errors
    outputs.append({"id": case["id"], "input": normalized(case), "events": copy.deepcopy(events.events), "declaration_unchanged": True})

assert EventType.CommandExcute.value == "command.excute"
assert outputs[0]["events"][1] == {"type": "plugin.action", "data": {"action": "not_a_method", "nested": {"values": ["kept", False, None]}, "big": 9007199254740993, "channel": "Web", "source": "owned-admin", "user": "owned-admin", "arg_str": "甲 乙 丙"}}
assert len(outputs[4]["events"]) == 1 and len(outputs[5]["events"]) == 1
assert outputs[1]["events"][1]["data"]["arg_str"] == "preset"
assert outputs[-1]["events"] == []
report = {
    "source_repository": "https://github.com/jxxghp/MoviePilot",
    "source_commit": "8a83efeacd3e26b82be495e17d50146e31c96a60",
    "method": "AST extracted original web_message, MessageChain command branch, and Command method bodies; owned captures only",
    "not_proof_of": ["host authentication", "Go compilation", "SDK IPC", "full MessageChain", "external chat integrations"],
    "sources_sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(REF.glob("*.py"))},
    "cases": outputs,
}
(HERE / "source-oracle.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"ok": True, "cases": len(outputs), "command_event_value": EventType.CommandExcute.value, "plugin_event_value": EventType.PluginAction.value}, ensure_ascii=False))
