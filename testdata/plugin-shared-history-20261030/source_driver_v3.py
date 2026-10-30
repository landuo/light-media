"""Fresh source observations: real history Oper/ORM, isolated SQLite, no network."""
import asyncio
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import sqlite3
import sys
import time

network_attempts = []
def offline(event, args):
    if event in {"socket.connect", "socket.connect_ex", "socket.getaddrinfo", "socket.sendto"}:
        network_attempts.append(event)
        raise RuntimeError("offline history source observation disallows network")
sys.addaudithook(offline)

from app.db import Engine, AsyncEngine, ScopedSession
from app.db.models.downloadhistory import DownloadHistory
from app.db.models.transferhistory import TransferHistory
from app.db.models.systemconfig import SystemConfig
from app.db.downloadhistory_oper import DownloadHistoryOper
from app.db.transferhistory_oper import TransferHistoryOper
from app.core.context import MediaInfo
from app.core.meta import MetaBase
from app.core.metainfo import MetaInfoPath
from app.schemas import FileItem, TransferInfo
from app.schemas.types import MediaType


def serialize(value):
    if value is None:
        return None
    return value.to_dict()

def error(exc):
    result = {"class": type(exc).__name__, "module": type(exc).__module__}
    if getattr(exc, "orig", None):
        result["driver_class"] = type(exc.orig).__name__
        result["sqlite_errorname"] = getattr(exc.orig, "sqlite_errorname", None)
    return result

def snapshot():
    result = {}
    with sqlite3.connect(Path(os.environ["CONFIG_DIR"]) / "user.db") as db:
        for model in [DownloadHistory, TransferHistory]:
            cols = [c.name for c in model.__table__.columns]
            selected = ",".join('"'+name+'"' for name in cols)
            result[model.__tablename__] = [dict(zip(cols, row)) for row in db.execute(
                'SELECT '+selected+' FROM '+model.__tablename__+' ORDER BY id')]
    return result


def run(plan_path, output_path):
    plan = json.loads(Path(plan_path).read_text())
    SystemConfig.__table__.create(bind=Engine, checkfirst=True)
    for model in [DownloadHistory, TransferHistory]:
        model.__table__.create(bind=Engine, checkfirst=True)
        for row in plan.get("seed", {}).get(model.__tablename__, []):
            model(**row).create(None)
    opers = {"download": DownloadHistoryOper(), "transfer": TransferHistoryOper()}
    observations = []
    for operation in plan["operations"]:
        record = {"case": operation["case"], "family": operation["family"],
                  "method": operation["method"], "arguments": operation.get("arguments", {})}
        arguments = dict(record["arguments"])
        if "producer" in operation:
            p = operation["producer"]
            meta_values = dict(p.get("meta", {}))
            if "type" in meta_values:
                meta_values["type"] = MediaType(meta_values["type"])
            arguments["meta"] = MetaInfoPath(Path(p["meta_path"])) if "meta_path" in p else MetaBase(title="Owned")
            for key, value in meta_values.items():
                setattr(arguments["meta"], key, value)
            arguments["fileitem"] = FileItem(**p["fileitem"])
            if "media" in p:
                media_values = dict(p["media"])
                media_values["type"] = MediaType(media_values["type"])
                arguments["mediainfo"] = MediaInfo(**media_values)
            if "transferinfo" in p:
                arguments["transferinfo"] = TransferInfo(**p["transferinfo"])
        record["before_local_date"] = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime())
        try:
            returned = getattr(opers[operation["family"]], operation["method"])(**arguments)
            record["returned"] = serialize(returned)
        except Exception as exc:
            record["error"] = error(exc)
        record["after_local_date"] = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime())
        record["database"] = snapshot()
        observations.append(record)
    imported = {}
    for module in tuple(sys.modules.values()):
        name = getattr(module, "__file__", None)
        if name and Path(name).is_file():
            p = Path(name).resolve()
            imported[str(p)] = hashlib.sha256(p.read_bytes()).hexdigest()
    output = {"scope": "actual source history Oper/ORM with real isolated SQLite; no Manager or source file-transfer chain execution",
              "python": platform.python_version(), "sqlite": sqlite3.sqlite_version,
              "dependencies": {name: importlib.metadata.version(name) for name in ["SQLAlchemy", "pydantic", "pydantic-settings"]},
              "timezone": os.environ["TZ"], "network_attempts": network_attempts,
              "schema": {model.__tablename__: [{"name": c.name, "type": str(c.type), "nullable": c.nullable} for c in model.__table__.columns] for model in [DownloadHistory, TransferHistory]},
              "observations": observations, "imported_files": imported}
    ScopedSession.remove()
    Engine.dispose()
    asyncio.run(AsyncEngine.dispose())
    Path(output_path).write_text(json.dumps(output, ensure_ascii=False, indent=2)+"\n")

if __name__ == "__main__":
    run(*sys.argv[1:])
