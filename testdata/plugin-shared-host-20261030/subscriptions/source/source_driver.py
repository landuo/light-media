"""正常导入固定原程序并记录真实 DAO、SQLite 与冷启动观察。"""

import asyncio
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import platform
import sqlite3
import sys


NETWORK_ATTEMPTS = []


def network_guard(event, args):
    """拒绝此离线观察进程的 Python 网络外连并保留事件类型。"""
    if event in {"socket.connect", "socket.connect_ex", "socket.getaddrinfo", "socket.sendto"}:
        NETWORK_ATTEMPTS.append(event)
        raise RuntimeError("offline source observation disallows network")


sys.addaudithook(network_guard)

from app.db import Engine, AsyncEngine, ScopedSession  # noqa: E402
from app.db.models.subscribe import Subscribe  # noqa: E402
from ordinary_plugin import OwnedSubscriptionsPlugin  # noqa: E402
from sqlalchemy import inspect  # noqa: E402


def serialize(value):
    """使用原版 ORM to_dict，仅统一 JSON 可传递结果。"""
    if value is None:
        return None
    if isinstance(value, list):
        return [row.to_dict() for row in value]
    return value.to_dict()


def error_record(exc):
    """记录异常分类，避免把运行路径和 SQL 参数误作为协议期待。"""
    result = {"class": type(exc).__name__, "module": type(exc).__module__}
    if getattr(exc, "orig", None) is not None:
        result["driver_class"] = type(exc.orig).__name__
        result["sqlite_errorname"] = getattr(exc.orig, "sqlite_errorname", None)
    return result


def sql_snapshot():
    """独立记录真实 SQLite 存储类型，不用于代替 DAO 结果。"""
    with sqlite3.connect(Path(os.environ["CONFIG_DIR"]) / "user.db") as connection:
        columns = [column.name for column in Subscribe.__table__.columns]
        names = ",".join('"' + name + '"' for name in columns)
        type_names = ",".join('typeof("' + name + '")' for name in columns)
        values = connection.execute(f"SELECT {names} FROM subscribe ORDER BY id").fetchall()
        types = connection.execute(f"SELECT {type_names} FROM subscribe ORDER BY id").fetchall()
    return [{"values": dict(zip(columns, row)), "sqlite_types": dict(zip(columns, kind))}
            for row, kind in zip(values, types)]


def imported_files():
    """绑定本次实际加载的源与依赖文件。"""
    result = {}
    for module in tuple(sys.modules.values()):
        file_name = getattr(module, "__file__", None)
        if file_name and Path(file_name).is_file():
            path = Path(file_name).resolve()
            result[str(path)] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def json_transport(value):
    """仅将 JSON 无法表示的 Python 非有限浮点标记化，绝不静默丢失。"""
    if isinstance(value, float) and not math.isfinite(value):
        return {"$source_python_float": repr(value)}
    if isinstance(value, dict):
        return {key: json_transport(item) for key, item in value.items()}
    if isinstance(value, list):
        return [json_transport(item) for item in value]
    return value


def run(plan_path, output_path):
    """执行外部输入，不由目标实现计算任何预期。"""
    plan = json.loads(Path(plan_path).read_text())
    Subscribe.__table__.create(bind=Engine, checkfirst=True)
    for seed in plan.get("seed", []):
        Subscribe(**seed).create(None)
    initial_database = sql_snapshot()
    plugin = OwnedSubscriptionsPlugin()
    plugin.init_plugin()
    observations = []
    for operation in plan["operations"]:
        record = {"case": operation["case"], "method": operation["method"],
                  "arguments": operation.get("arguments", {})}
        # SQL 仅构造自建故障/遗留数据；被测读写仍全部通过真实插件 DAO。
        if "fixture_sql" in operation:
            with sqlite3.connect(Path(os.environ["CONFIG_DIR"]) / "user.db") as connection:
                connection.executescript(operation["fixture_sql"])
        try:
            returned = getattr(plugin, operation["method"])(**record["arguments"])
            if operation["method"] == "update" and returned is not None:
                state = inspect(returned)
                record["returned_orm"] = {
                    "class": type(returned).__name__, "detached": state.detached,
                    "expired_columns": sorted(state.expired_attributes),
                }
                for key in operation.get("observe_unknown_attributes", []):
                    record.setdefault("returned_unknown_attributes", {})[key] = getattr(returned, key, None)
            try:
                record["returned"] = serialize(returned)
            except Exception as exc:
                record["return_serialization_error"] = error_record(exc)
        except Exception as exc:
            record["error"] = error_record(exc)
        if operation["method"] == "update":
            sid = operation.get("fresh_sid", record["arguments"]["sid"])
            try:
                record["fresh_get"] = serialize(plugin.get(sid=sid))
            except Exception as exc:
                record["fresh_get_error"] = error_record(exc)
        record["database"] = sql_snapshot()
        observations.append(record)
    output = {
        "scope": "owned ordinary plugin -> default real SubscribeOper -> source SQLAlchemy/SQLite; no Manager/Base/startup",
        "python": platform.python_version(), "sqlite": sqlite3.sqlite_version,
        "sqlalchemy": importlib.metadata.version("SQLAlchemy"),
        "nonfinite_encoding": {"$source_python_float": "repr(value); observational tag, not original DAO JSON"},
        "initial_database": initial_database,
        "schema": [{"name": c.name, "type": str(c.type), "nullable": c.nullable} for c in Subscribe.__table__.columns],
        "observations": observations, "network_attempts": NETWORK_ATTEMPTS,
        "imported_files": imported_files(),
    }
    ScopedSession.remove()
    Engine.dispose()
    asyncio.run(AsyncEngine.dispose())
    Path(output_path).write_text(json.dumps(json_transport(output), ensure_ascii=False, indent=2, allow_nan=False) + "\n")


if __name__ == "__main__":
    run(*sys.argv[1:])
