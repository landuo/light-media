import ast, hashlib, io, json, pathlib, sys, types, typing

base = pathlib.Path(__file__).parent
# Test-only oracle dependency root: exact installed 0.19.1 wheel, not runtime Python.
sys.path.insert(0, sys.argv[1])
import ruamel.yaml
from ruamel.yaml import CommentedMap

assert ruamel.yaml.__version__ == "0.19.1"
helper_source = base / "installed-category-helper.py"
plugin_source = base / "installed-categoryeditor.py"
assert hashlib.sha256(helper_source.read_bytes()).hexdigest() == "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2"
assert hashlib.sha256(plugin_source.read_bytes()).hexdigest() == "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27"

initial = "movie:\n  缓存电影:\n    original_language: 'zh'\n  其它电影:\ntv:\n  缓存剧集:\n"
cases = [
    ("blank", ""), ("comments", "# cleared policy\n"), ("null", "null\n"),
    ("empty_map", "{}\n"), ("empty_sequence", "[]\n"), ("false", "false\n"),
    ("zero", "0\n"), ("empty_string", "''\n"),
    ("parser_failure", "movie:\n  [ordinary syntax error\n"),
    ("duplicate_key", "movie: {}\nmovie: {}\n"),
    ("multiple_documents", "movie: {}\n---\ntv: {}\n"),
]

def method(source, name, extra):
    tree = ast.parse(source.read_text())
    cls = next(n for n in tree.body if isinstance(n, ast.ClassDef))
    node = next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == name)
    node.decorator_list = []
    namespace = dict(extra)
    exec(compile(ast.Module(body=[node], type_ignores=[]), str(source), "exec"), namespace)
    return namespace[name]

class MemoryPath:
    def exists(self): return True

def fixture(text):
    context = {"text": text, "warnings": [], "order": []}
    logger = types.SimpleNamespace(warn=lambda *_: context["warnings"].append("parser_warning"), info=lambda *_: None)
    def memory_open(*args, **kwargs): return io.StringIO(context["text"])
    init = method(helper_source, "init", {"ruamel": ruamel, "open": memory_open, "logger": logger})
    get = method(helper_source, "get_category", {"Union": typing.Union, "CommentedMap": CommentedMap})
    state = types.SimpleNamespace(_category_path=MemoryPath(), _categorys={}, _movie_categorys={}, _tv_categorys={})
    def reload():
        context["order"].append("reload")
        init(state)
    state.init = reload
    state.init()
    return context, state, get

results = []
for name, text in cases:
    context, state, get = fixture(initial)
    context["text"] = text
    state.init()
    warm = [get(state._movie_categorys, {"original_language": "zh"}), get(state._tv_categorys, {"origin_country": ["CN"]})]
    cold_context, cold, cold_get = fixture(text)
    cold_values = [cold_get(cold._movie_categorys, {"original_language": "zh"}), cold_get(cold._tv_categorys, {"origin_country": ["CN"]})]
    assert warm == ["缓存电影", "缓存剧集"], (name, warm)
    assert cold_values == ["", ""], (name, cold_values)
    results.append({"case": name, "warm": warm, "cold": cold_values, "warm_parser_warning_count": len(context["warnings"]), "cold_parser_warning_count": len(cold_context["warnings"])})

orders = []
for fail_notice in [False, True]:
    context, helper, get = fixture(initial)
    context["order"] = []
    class UserPath:
        def write_text(self, value, **kwargs):
            context["order"].append("write")
            context["text"] = value
    def notice(*args, **kwargs):
        context["order"].append("notice")
        if fail_notice: raise RuntimeError("owned notice failure")
    def patch(value):
        context["order"].append("patch")
        assert value["enabled"] is False
    plugin = types.SimpleNamespace(user_yaml=UserPath(), systemmessage=types.SimpleNamespace(put=notice), update_config=patch)
    invoke = method(plugin_source, "init_plugin", {"CategoryHelper": lambda: helper})
    failed = False
    try: invoke(plugin, {"enabled": True, "content": ""})
    except RuntimeError: failed = True
    assert failed == fail_notice
    expected = ["write", "reload", "notice"] + ([] if fail_notice else ["patch"])
    assert context["order"] == expected
    assert plugin._enabled == fail_notice
    orders.append({"notice_failure": fail_notice, "order": context["order"], "enabled_after": plugin._enabled})

output = {"source": "complete installed methods compiled locally; fake file IO/notifications only", "yaml": "0.19.1; all 33 Python sources SHA match installed package", "cases": results, "orders": orders}
(pathlib.Path(sys.argv[2])).write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"source_cases": len(results), "source_order_cases": len(orders), "all_pass": True}))
