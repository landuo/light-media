#!/usr/bin/env python3
"""Execute pinned original endpoint and actual Pydantic image normalization."""
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
from pydantic import BaseModel

ROOT = Path(__file__).resolve().parent
REF = ROOT / "reference"
endpoint_tree = ast.parse((REF / "api-message.py").read_text())
endpoint = copy.deepcopy(next(n for n in endpoint_tree.body if isinstance(n, ast.AsyncFunctionDef) and n.name == "web_message"))
endpoint.decorator_list = []
schema_tree = ast.parse((REF / "schema-message.py").read_text())
outer = next(n for n in schema_tree.body if isinstance(n, ast.ClassDef) and n.name == "CommingMessage")
image_class = copy.deepcopy(next(n for n in outer.body if isinstance(n, ast.ClassDef) and n.name == "MessageImage"))
captures = []
source_events = []
errors = []
types_tree = ast.parse((REF / "schema-types.py").read_text())
event_type = next(n for n in types_tree.body if isinstance(n, ast.ClassDef) and n.name == "EventType")
event_values = {n.targets[0].id: ast.literal_eval(n.value) for n in event_type.body if isinstance(n, ast.Assign) and isinstance(n.targets[0], ast.Name) and n.targets[0].id in {"CommandExcute", "PluginAction"}}
EventType = enum.Enum("EventType", event_values)

def extracted_class(filename, name, method_names):
    cls = next(n for n in ast.parse((REF / filename).read_text()).body if isinstance(n, ast.ClassDef) and n.name == name)
    methods = [copy.deepcopy(n) for n in cls.body if isinstance(n, ast.FunctionDef) and n.name in method_names]
    assert len(methods) == len(method_names)
    for method in methods:
        method.decorator_list = [d for d in method.decorator_list if isinstance(d, ast.Name) and d.id in {"classmethod", "staticmethod"}]
    return ast.ClassDef(name=name, bases=[], keywords=[], body=methods, decorator_list=[])

def source_send_event(event, data):
    source_events.append({"type": event.value, "data": copy.deepcopy(data)})
    if event == EventType.CommandExcute:
        command.command_event(SimpleNamespace(event_data=data))

namespace = {
    "BaseModel": BaseModel,
    "MessageChain": lambda: SimpleNamespace(handle_message=lambda **kwargs: captures.append(copy.deepcopy(kwargs))),
    "MessageChannel": SimpleNamespace(Web="Web"),
    "Depends": lambda *args, **kwargs: None,
    "get_current_active_superuser": lambda: None,
    "schemas": SimpleNamespace(Response=lambda **kwargs: kwargs),
    "copy": copy,
    "re": re,
    "EventType": EventType,
    "eventmanager": SimpleNamespace(send_event=source_send_event),
    "ObjectUtils": SimpleNamespace(arguments=lambda method: len(inspect.signature(method).parameters)),
    "logger": SimpleNamespace(info=lambda *args, **kwargs: None, error=lambda *args, **kwargs: errors.append(args)),
    "_finish_command_processing_status": lambda *args, **kwargs: None,
}
command_class = extracted_class("command.py", "Command", ["__build_plugin_commands", "__run_command", "get", "execute", "send_plugin_event", "command_event"])
chain_class = extracted_class("chain-message.py", "MessageChain", ["_handle_message_core", "_strip_no_ai_prefix", "_has_ai_prefix", "_is_agent_message"])
module = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), image_class, command_class, chain_class, endpoint], type_ignores=[])
exec(compile(ast.fix_missing_locations(module), "actual-media-reference-bodies", "exec"), namespace)
Chain = namespace["MessageChain"]
Chain._ai_prefix = "/ai"
Chain._no_ai_prefix = "/noai"
chain = Chain()
chain.eventmanager = namespace["eventmanager"]
chain._handle_plugin_input_interaction = lambda **kwargs: False
namespace["MessageChain"] = lambda: SimpleNamespace(handle_message=lambda **kwargs: captures.append(copy.deepcopy(kwargs)))
command = namespace["Command"]()
command.pluginmanager = SimpleNamespace(get_plugin_commands=lambda: [{"cmd": "/owned_command", "event": EventType.PluginAction, "data": {"action": "not_a_method"}}])
command.messagehelper = SimpleNamespace(put=lambda **kwargs: errors.append(kwargs))
command._commands = command._Command__build_plugin_commands()
MessageImage = namespace["MessageImage"]
# Resolve only the actual annotations used by the original model.
MessageImage.model_rebuild(_types_namespace={"Optional": __import__("typing").Optional})

class Request:
    headers = {"content-type": "application/json"}
    def __init__(self, payload): self.payload = payload
    async def json(self): return self.payload

cases = [
    ("absent", {}, False),
    ("image-null", {"image": None}, False),
    ("images-null", {"images": None}, False),
    ("image-empty-array", {"image": []}, False),
    ("images-empty-array", {"images": []}, False),
    ("image-empty-string", {"image": ""}, False),
    ("images-empty-string-retains-ref", {"images": ""}, True),
    ("both-empty", {"image": "", "images": []}, False),
    ("both-null", {"image": None, "images": None}, False),
    ("image-empty-object", {"image": {}}, False),
    ("images-empty-object", {"images": {}}, False),
    ("image-false", {"image": False}, False),
    ("images-false", {"images": False}, False),
    ("image-zero", {"image": 0}, False),
    ("images-zero", {"images": 0}, False),
    ("images-empty-ref-object", {"images": {"ref": ""}}, False),
    ("images-list-empty-ref", {"images": [""]}, True),
    ("images-list-only-ignored", {"images": [None, False, 0, {}, []]}, False),
    ("image-unsupported-list-value", {"image": ["owned://image"]}, False),
    ("image-single-ref", {"image": "owned://image"}, True),
    ("images-single-ref", {"images": "owned://image"}, True),
    ("images-list-ref", {"images": ["owned://image"]}, True),
    ("image-object-ref", {"image": {"ref": "owned://image"}}, True),
    ("images-object-url", {"images": {"url": "owned://image"}}, True),
    ("empty-image-preserves-images", {"image": "", "images": ["owned://image"]}, True),
    ("truthy-image-appended", {"image": "owned://second", "images": ["owned://first"]}, True),
    ("truthy-image-replaces-string", {"image": "owned://image", "images": ""}, True),
    ("image-null-images-empty-string", {"image": None, "images": ""}, True),
    ("fallback-url-after-empty-ref", {"images": {"ref": "", "url": "owned://image"}}, True),
    ("images-object-image-url", {"images": {"image_url": "owned://image"}}, True),
    ("images-object-file-url", {"images": {"file_url": "owned://image"}}, True),
]
outputs = []
for name, payload, expected in cases:
    captures.clear()
    source_events.clear()
    response = asyncio.run(namespace["web_message"](Request({"text": "/owned_command", **payload}), current_user=SimpleNamespace(name="owned-admin")))
    assert response == {"success": True} and len(captures) == 1
    forwarded = captures[0]["images"]
    normalized = MessageImage.normalize_list(forwarded)
    has_media = bool(normalized)
    assert has_media == expected, (name, forwarded, normalized)
    assert not chain._is_agent_message(userid="owned-admin", text="/owned_command", images=normalized)
    assert chain._handle_message_core(channel="Web", source="owned-admin", userid="owned-admin", username="owned-admin", text="/owned_command", images=normalized) is False
    assert [event["type"] for event in source_events] == ["command.excute", "plugin.action"]
    assert all("image" not in event["data"] and "images" not in event["data"] for event in source_events)
    assert not errors, errors
    outputs.append({"id": name, "payload": payload, "image_truthy": bool(payload.get("image")), "images_truthy": bool(payload.get("images")), "endpoint_images": forwarded, "normalized_images": [v.model_dump() for v in normalized] if normalized else None, "has_normalized_images": has_media, "is_agent_message": False, "events": copy.deepcopy(source_events)})

source_events.clear()
chain._handle_message_core(channel="Web", source="owned-admin", userid="owned-admin", username="owned-admin", text="/not_registered", images=MessageImage.normalize_list(["owned://image"]))
assert [event["type"] for event in source_events] == ["command.excute"]

report = {
    "source_repository": "https://github.com/jxxghp/MoviePilot",
    "source_commit": "8a83efeacd3e26b82be495e17d50146e31c96a60",
    "method": "Original AST web_message, MessageImage under installed Pydantic, _is_agent_message, _handle_message_core and Command registry/dispatch method bodies; no translated normalization or command model",
    "boundary": "The original MessageChain routes slash commands before media/AI handling even with normalized image objects. The restored bridge consumes only text and trusted identity, never fetches or executes media, and still explicitly rejects image-only/non-command/AI requests.",
    "unknown_slash_with_image_events": copy.deepcopy(source_events),
    "sources_sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(REF.glob("*.py"))},
    "cases": outputs,
}
(ROOT / "media-oracle.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"ok": True, "cases": len(outputs), "image_empty_string": False, "images_empty_string": True}))
