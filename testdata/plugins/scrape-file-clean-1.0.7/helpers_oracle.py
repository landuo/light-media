"""Original pure helper methods, with no app imports and synthetic HOME."""
import ast,hashlib,json,os,pathlib,typing
source=pathlib.Path('/app/app/plugins/scrapefileclean/__init__.py').read_bytes();assert hashlib.sha256(source).hexdigest()=='c14c36e17112605e6c856614e88ea425530be2462123126a85e1adfea009a3af'
cls=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef)and n.name=='ScrapeFileClean');wanted={'_parse_custom_scrap_extensions','_normalize_config_path','_is_same_or_child_path','_same_media_scrap_name','_belongs_to_other_media'};cls.bases=[];cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef)and n.name in wanted];ns=dict(typing.__dict__,os=os,Path=pathlib.Path);exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'<installed helper methods>','exec'),ns);p=ns['ScrapeFileClean'];os.environ['HOME']='/synthetic-home'
paths=['','.', './','a','a/.','a/../b','../a','/a','/a/./b','/a/../b','//a/b','///a/b','~/a']
contain=[dict(path=a,base=b,result=p._is_same_or_child_path(pathlib.Path(a),b))for a in paths for b in paths]
parsed=[dict(value=s,result=p._parse_custom_scrap_extensions(s))for s in ['', 'nfo，JPG\n-ass,nfo, .SRT ', '\x1cZ\x1f', '-poster.jpg,-poster.jpg', '.²,K,Σ,İ']]
names=[]
for media in ['a','Film','é','合成','']:
 for suffix in ['', '.', '.nfo', '-x.nfo','².nfo','_zh.srt','字.nfo','\U00011f02.nfo','\U0001e4d0.nfo']:
  name=media+suffix;names.append(dict(name=name,media=media,result=p._same_media_scrap_name(pathlib.Path(name),media)))
others=[dict(name=name,media=media,other=other,result=p._belongs_to_other_media(name,media,set(other)))for name,media,other in [('Film-2.nfo','Film',['Film-2']),('Film.nfo','Film',['Film']),('Film².nfo','Film',['Film²']),('Film-2','Film',['Film-2']),('Film-2.nfo','Film',[])]]
print(json.dumps(dict(source_sha256=hashlib.sha256(source).hexdigest(),home='/synthetic-home',contain=contain,extensions=parsed,names=names,others=others),ensure_ascii=False,indent=2))
