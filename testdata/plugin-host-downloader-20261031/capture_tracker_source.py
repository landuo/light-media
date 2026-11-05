"""Fixed URL helper and exact membership oracle; all URI values are synthetic."""
import ast,copy,gzip,hashlib,json,pathlib,subprocess,urllib.parse as parse,zipfile
root=pathlib.Path(__file__).resolve().parent
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show','8a83efeacd3e26b82be495e17d50146e31c96a60:app/utils/string.py'])
tree=ast.parse(raw);method=next(n for c in tree.body if isinstance(c,ast.ClassDef) for n in c.body if isinstance(n,ast.FunctionDef) and n.name=='get_url_netloc')
method.decorator_list=[];method.returns=None
for arg in method.args.args:arg.annotation=None
ns={'parse':parse};exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'app/utils/string.py','exec'),ns)
values=['https://tracker.invalid/announce?passkey=OWNED_A','https://tracker.invalid/other?passkey=OWNED_B','udp://tracker.invalid/announce?passkey=OWNED_A','udp://tracker.invalid/announce?passkey=OWNED_B','http://user:OWNED_PASSWORD@tracker.invalid:8080/path?token=OWNED_A','HTTP://tracker.invalid/a?passkey=OWNED_A','HTTP://tracker.invalid/a?passkey=OWNED_B','','unknown','http:///path','http://[::1]:80/a','http://host:badport/path','http://]broken[','http://tracker.invalid\t/path']
cases=[]
for value in values:
 try:group=ns['get_url_netloc'](value)[1];cases.append({'url':value,'group':group,'valid':True})
 except Exception as e:cases.append({'url':value,'valid':False,'error_class':type(e).__name__})
messages=['Success','rejected OWNED_A',' rejected OWNED_A ','https://tracker.invalid/error?passkey=OWNED_A']
comparison=[]
for message in messages:comparison.append({'message':message,'nonempty':bool(message),'nonblank':bool(message.strip()),'matches':[message==x for x in messages],'stripped_matches':[message.strip()==x for x in messages]})
result={'kind':'fixed-source AST URL helper + Python primitive comparisons','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_path':'app/utils/string.py','source_sha256':hashlib.sha256(raw).hexdigest(),'source_line':method.lineno,'cases':cases,'messages':messages,'message_cases':comparison}


archive=zipfile.ZipFile(root.parents[1]/'work/mp-plugin-handoff-20261029/mp-plugins-handoff-20261029.zip')
name=next(n for n in archive.namelist() if n.endswith('/cleaninvalidseed/__init__.py'))
consumer=archive.read(name)
assert hashlib.sha256(consumer).hexdigest()=='fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda'
consumer_tree=ast.parse(consumer)
assignment=next(n for n in ast.walk(consumer_tree) if isinstance(n,ast.Assign) and n.lineno==841 and any(isinstance(t,ast.Name) and t.id=='is_error_tracker' for t in n.targets))
expression=compile(ast.fix_missing_locations(ast.Expression(body=copy.deepcopy(assignment.value))),'cleaninvalidseed/__init__.py:841','eval')
result['consumer_comparison']={'source_file':'cleaninvalidseed/__init__.py','sha256':hashlib.sha256(consumer).hexdigest(),'line':841,'cases':[]}
for status in [2,3,4]:
 for message in messages:
  result['consumer_comparison']['cases'].append({'status':status,'message':message,'is_error_tracker':eval(expression,{},dict(tracker_status=status,tracker_msg=message,error_msgs=messages[1:]))})
(root/'tracker-source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
