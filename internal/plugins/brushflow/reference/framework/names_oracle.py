"""Offline fixed actual-source AST only: no application import/network/config."""
import ast,json,hashlib,pathlib,types
p=pathlib.Path(__file__).resolve().parent
s=p/"app/modules/themoviedb/tmdbapi.py"
t=ast.parse(s.read_text());cls=next(x for x in t.body if isinstance(x,ast.ClassDef)and x.name=="TmdbApi");f=next(x for x in cls.body if isinstance(x,ast.FunctionDef)and x.name=="__get_names");f.decorator_list=[]
ns={"List":list,"MediaType":types.SimpleNamespace(MOVIE="movie")}
exec(compile(ast.fix_missing_locations(ast.Module(body=[f],type_ignores=[])),str(s),"exec"),ns)
cases=[]
for kind in ["movie","tv"]:
 for obj in [{},{"alternative_titles":{"titles":[{"title":"wrong movie"}],"results":[{"title":"wrong tv"}]}},{"alternative_titles":{"titles":[{"title":" A "},{"title":"A"},{"title":"a"},{"title":"A"},{"title":""}],"results":[{"title":" A "},{"title":"A"},{"title":"a"},{"title":"A"}]}},{"alternative_titles":{"titles":[{"title":"foreign%d"%i,"iso_3166_1":"RU"}for i in range(20)],"results":[{"title":"foreign%d"%i,"iso_3166_1":"RU"}for i in range(20)]},"translations":{"translations":[{"data":{"title":"电影译名","name":"剧集译名"}},{"data":{"title":"foreign1","name":"foreign1"}}]}},{"translations":{"translations":[{"data":{"title":"中文","name":"中文"}},{"data":{}},{"data":{"title":"English","name":"English"}}]}}]:
  raw={"id":123,"media_type":kind,**obj};cases.append({"kind":kind,"raw":raw,"expected":ns["__get_names"](raw)})
result={"source_sha256":hashlib.sha256(s.read_bytes()).hexdigest(),"runtime":__import__("platform").python_version(),"scope":"fixed TmdbApi.__get_names AST, local offline CPython, no actual3.12 claim","cases":cases}
(p/"names-oracle.json").write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n");print(json.dumps({"cases":len(cases),"source_sha256":result["source_sha256"]}))
