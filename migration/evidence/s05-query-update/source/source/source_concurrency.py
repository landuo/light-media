"""真实两个普通插件并行更新：只用 SQLAlchemy load 事件协调读时点。"""

import asyncio
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import sys
import threading

from source_driver import NETWORK_ATTEMPTS, error_record, imported_files, json_transport, sql_snapshot
from app.db import Engine, AsyncEngine, ScopedSession
from app.db.models.subscribe import Subscribe
from ordinary_plugin import OwnedSubscriptionsPlugin
from sqlalchemy import event


def run(output_path):
    """在独占实际 SQLite 库中让两个更新均先读同一旧行。"""
    Subscribe.__table__.create(bind=Engine, checkfirst=True)
    Subscribe(id=1, name="Owned initial", keyword="Owned original").create(None)
    barrier = threading.Barrier(2, timeout=15)
    coordination = threading.local()

    def after_load(target, context):
        """仅协调第一次真实 ORM 加载，不替换 DAO 或 SQL 执行。"""
        if getattr(coordination, "wait", False):
            coordination.wait = False
            barrier.wait()

    event.listen(Subscribe, "load", after_load)

    def update_one(patch):
        """使用各线程默认 ScopedSession 调用原版更新。"""
        plugin = OwnedSubscriptionsPlugin()
        plugin.init_plugin()
        coordination.wait = True
        try:
            result = plugin.update(sid=1, payload=patch)
            try:
                returned = result.to_dict()
            except Exception as exc:
                returned = {"serialization_error": error_record(exc)}
            return {"patch": patch, "returned": returned}
        finally:
            ScopedSession.remove()

    try:
        with ThreadPoolExecutor(max_workers=2) as executor:
            outputs = list(executor.map(update_one, [{"name": "Owned concurrent name"},
                                                      {"keyword": "Owned concurrent keyword"}]))
    finally:
        event.remove(Subscribe, "load", after_load)
    plugin = OwnedSubscriptionsPlugin()
    plugin.init_plugin()
    row = plugin.get(sid=1).to_dict()
    assert row["name"] == "Owned concurrent name"
    assert row["keyword"] == "Owned concurrent keyword"
    output = {"boundary": "real default source DAO, SQLAlchemy load event only coordinates first reads",
              "observations": outputs, "fresh_get": row, "database": sql_snapshot(),
              "network_attempts": NETWORK_ATTEMPTS, "imported_files": imported_files()}
    ScopedSession.remove()
    Engine.dispose()
    asyncio.run(AsyncEngine.dispose())
    Path(output_path).write_text(json.dumps(json_transport(output), ensure_ascii=False, indent=2, allow_nan=False) + "\n")


if __name__ == "__main__":
    run(sys.argv[1])
