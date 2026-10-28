"""Development-only execution of the pinned cache and history functions.

Only storage/network/manager boundaries are synthetic. No MoviePilot service is
started or modified; generated JSON is consumed by Go tests without Python.
"""
import ast
import asyncio
import hashlib
import inspect
import json
from functools import wraps
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace as NS

COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
files = {name: subprocess.check_output(["git", "-C", sys.argv[1], "show", f"{COMMIT}:{name}"])
         for name in ["app/core/cache.py", "app/api/endpoints/plugin.py"]}


def extract(path, name, namespace):
    tree = ast.parse(files[path])
    node = next(n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name == name)
    node.decorator_list = []
    module = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), node], type_ignores=[])
    exec(compile(ast.fix_missing_locations(module), path, "exec"), namespace)


class Cache:
    def __init__(self, **kwargs):
        self.data = {}

    def get(self, key, **kwargs):
        return self.data.get(key)

    def set(self, key, value, **kwargs):
        self.data[key] = value


class AsyncCache(Cache):
    async def get(self, key, **kwargs):
        return super().get(key, **kwargs)

    async def set(self, key, value, **kwargs):
        super().set(key, value, **kwargs)


fresh = False
ns = dict(inspect=inspect, wraps=wraps, Cache=Cache, AsyncCache=AsyncCache,
          hashkey=lambda *args: args, is_fresh=lambda: fresh)
extract("app/core/cache.py", "cached", ns)


async def cache_case(asynchronous):
    global fresh
    fresh = False
    calls = 0
    value = {"Owned": {"version": "2.0"}}

    def fetch(repository):
        nonlocal calls
        calls += 1
        return value

    async def async_fetch(repository):
        return fetch(repository)

    cached = ns["cached"](maxsize=128, ttl=1800)(async_fetch if asynchronous else fetch)

    async def read():
        result = cached("owned-source")
        return await result if asynchronous else result

    initial = await read()
    value = None
    fresh = True
    failed = await read()
    fresh = False
    ordinary = await read()
    value = {}
    fresh = True
    empty = await read()
    value = None
    fresh = False
    ordinary_empty = await read()
    return dict(initial=initial, failed_fresh=failed, ordinary_after_failure=ordinary,
                empty_fresh=empty, ordinary_after_empty=ordinary_empty, fetch_calls=calls)


async def history_cases():
    installed = NS(id="Owned", installed=True, plugin_version="1.0", repo_url=None, history={})
    manager = NS(get_local_plugins=lambda: [installed], get_local_repo_plugins=lambda: [],
                 async_get_online_plugins=None)
    async def online(**kwargs):
        return []
    manager.async_get_online_plugins = online
    env = dict(PluginManager=lambda: manager, logger=NS(debug=lambda *args: None))
    extract("app/api/endpoints/plugin.py", "_get_plugin_history_detail", env)
    result = await env["_get_plugin_history_detail"]("Owned", True)
    missing = await env["_get_plugin_history_detail"]("Missing", True)
    return dict(remote_absent_returns_installed=result is installed,
                fallback_version=result.plugin_version, fallback_history=result.history,
                uninstalled_returns_none=missing is None)


async def main():
    return dict(source_commit=COMMIT, source_sha256={name: hashlib.sha256(raw).hexdigest() for name, raw in files.items()},
                synchronous=await cache_case(False), asynchronous=await cache_case(True),
                history=await history_cases(), releases=await release_cases())


async def release_cases():
    refreshed = []
    async def has_cache(repository):
        return True
    async def versions(plugin, repository):
        return [{"version": "1.0"}]
    async def market(manager, plugin, repository, force):
        return NS(plugin_version="2.0", release=True)
    env = dict(Depends=lambda value: None, get_current_active_superuser_async=None,
               PluginManager=lambda: NS(get_local_plugin_version=lambda plugin: "1.0"),
               PluginHelper=lambda: NS(async_has_plugin_release_cache=has_cache,
                                       async_get_plugin_release_versions=versions),
               _get_market_plugin_from_repo=market,
               _schedule_plugin_release_refresh=lambda plugin, repository: refreshed.append([plugin, repository]))
    extract("app/api/endpoints/plugin.py", "plugin_releases", env)
    no_source = await env["plugin_releases"]("Owned", None, repo_url="", force=True)
    forced = await env["plugin_releases"]("Owned", None, repo_url="owned-source", force=True)
    return dict(no_source=no_source, forced_with_existing_cache=forced, scheduled_refresh=refreshed)


Path(__file__).with_name("cache-oracle.json").write_text(json.dumps(asyncio.run(main()), indent=2, ensure_ascii=False) + "\n")
