"""真实 FastAPI 序列化组件观察；自建 ASGI 路由，不冒充原完整认证入口。"""

import asyncio
import json
from pathlib import Path
import sys

from source_driver import NETWORK_ATTEMPTS, error_record, imported_files, json_transport, sql_snapshot
from app.db import Engine, AsyncEngine, ScopedSession
from app.db.models.subscribe import Subscribe
from app.schemas.subscribe import Subscribe as SubscribeSchema
from ordinary_plugin import OwnedSubscriptionsPlugin
from fastapi import FastAPI


async def run(output_path):
    """观察已提交非有限数的普通响应与原模型响应差异。"""
    Subscribe.__table__.create(bind=Engine, checkfirst=True)
    Subscribe(id=1, name="Owned JSON boundary", type="电影").create(None)
    plugin = OwnedSubscriptionsPlugin()
    plugin.init_plugin()
    app = FastAPI()

    async def update_raw():
        """真实提交后返回 fresh get，不替换 JSON 编码器。"""
        plugin.update(sid=1, payload={"vote": "Infinity"})
        return plugin.get(sid=1).to_dict()

    async def read_raw():
        """返回原 to_dict 的 Python 值。"""
        return plugin.get(sid=1).to_dict()

    async def read_schema():
        """返回真实原 ORM，由真实源 Pydantic schema 处理。"""
        return plugin.get(sid=1)

    async def update_void():
        """普通插件忽略 update 返回，另行返回简单成功 JSON。"""
        plugin.update(sid=1, payload={"vote": "Infinity"})
        return {"success": True}

    app.add_api_route("/owned/update-raw", update_raw, methods=["POST"])
    app.add_api_route("/owned/read-raw", read_raw, methods=["GET"])
    app.add_api_route("/owned/read-schema", read_schema, methods=["GET"], response_model=SubscribeSchema)
    app.add_api_route("/owned/update-void", update_void, methods=["POST"])

    async def request(path, method):
        """内存中调用实际 ASGI 框架，保留响应和传播异常。"""
        messages = []

        async def receive():
            return {"type": "http.request", "body": b"", "more_body": False}

        async def send(message):
            messages.append({key: value.decode("utf-8", "replace") if isinstance(value, bytes) else value
                             for key, value in message.items() if key != "headers"})

        scope = {"type": "http", "asgi": {"version": "3.0"}, "http_version": "1.1",
                 "method": method, "scheme": "http", "path": path, "raw_path": path.encode(),
                 "query_string": b"", "headers": [], "client": ("127.0.0.1", 1),
                 "server": ("owned.invalid", 80), "root_path": ""}
        record = {"path": path, "method": method}
        try:
            await app(scope, receive, send)
        except Exception as exc:
            record["error"] = error_record(exc)
            record["error_text"] = str(exc)
        record["asgi_messages"] = messages
        record["fresh_get"] = plugin.get(sid=1).to_dict()
        record["database"] = sql_snapshot()
        return record

    records = []
    for path, method in (("/owned/read-schema", "GET"), ("/owned/update-raw", "POST"),
                         ("/owned/read-raw", "GET"), ("/owned/read-schema", "GET"),
                         ("/owned/update-void", "POST")):
        records.append(await request(path, method))
    output = {"boundary": "actual FastAPI ASGI/Starlette/Pydantic with owned routes and real source schema/DAO; no source API auth/full router",
              "observations": records, "network_attempts": NETWORK_ATTEMPTS,
              "imported_files": imported_files()}
    ScopedSession.remove()
    Engine.dispose()
    await AsyncEngine.dispose()
    Path(output_path).write_text(json.dumps(json_transport(output), ensure_ascii=False, indent=2, allow_nan=False) + "\n")


if __name__ == "__main__":
    asyncio.run(run(sys.argv[1]))
