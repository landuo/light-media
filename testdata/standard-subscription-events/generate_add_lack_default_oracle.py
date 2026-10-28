"""Execute the fixed missing-lack branch at the existing native UI boundary."""
import ast
import hashlib
import json
from pathlib import Path

source = Path("/Users/accidia/projects/MoviePilot/app/chain/subscribe.py")
method = next(n for n in ast.walk(ast.parse(source.read_text()))
              if isinstance(n, ast.AsyncFunctionDef) and n.name == "async_add")
node = next(n for n in ast.walk(method)
            if isinstance(n, ast.If)
            and ast.unparse(n.test) == "not kwargs.get('lack_episode')")
namespace = {"kwargs": {"total_episode": 8}}
exec(compile(ast.fix_missing_locations(ast.Module(body=[node], type_ignores=[])),
             "fixed-async-add-lack-default", "exec"), namespace)
print(json.dumps({
    "commit": "8a83efeacd3e26b82be495e17d50146e31c96a60",
    "source": f"app/chain/subscribe.py:{node.lineno}-{node.end_lineno}",
    "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
    "kind": "actual fixed async_add missing-lack statement; owned existing native UI input boundary (lack not supplied)",
    "input": {"total_episode": 8},
    "result": namespace["kwargs"],
}, indent=2))
