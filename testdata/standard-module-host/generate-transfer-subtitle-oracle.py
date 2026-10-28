"""Execute pinned __rename_subtitles, with real FileItem and pathlib.
Only the normal DEFAULT_SUB setting is supplied; no old application runs.
"""
import ast,copy,hashlib,json,re,subprocess
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
source=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show','8a83efeacd3e26b82be495e17d50146e31c96a60:app/modules/filemanager/transhandler.py'],text=True)
owner=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef)and n.name=='TransHandler')
fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='__rename_subtitles'));fn.decorator_list=[]
patterns={n.targets[0].id:ast.literal_eval(n.value)for n in fn.body if isinstance(n,ast.Assign)and isinstance(n.targets[0],ast.Name)and n.targets[0].id.endswith('_sub_re')}
ns={'Path':Path,'FileItem':env['ns']['FileItem'],'re':re,'settings':None}
exec(compile(ast.fix_missing_locations(ast.Module(body=[fn],type_ignores=[])),'<fixed:TransHandler.__rename_subtitles>','exec'),ns)
names=['Film.srt','Film.zh.srt','Film.zh-cn.srt','Film.cht.ass','Film.繁体中文.ass','Film.中字.ass','Film.eng.srt','Film.jpn.srt','Film.ja-jp.srt','Film.gb.srt','Film.big5.srt','Film.gb2312.srt','Film.chinese.srt','Film.JPSC.srt','Film.JPTC.srt','Film.sc_jp.srt','Film.tc_jp.srt','Film.中英字幕.srt','Film.中文繁体.srt','Film.chs+cht.srt','Film.CHI.SRT','Film.English.srt','Film.chİnese.srt','Film.ENG.ass']
rows=[]
for default in ['zh-cn','zh-tw','ja','eng','',None,'ZH-CN',False,1]:
 ns['settings']=SimpleNamespace(DEFAULT_SUB=default)
 for name in names:
  for target in ['/owned/library/Film.mkv','/owned/library/.hidden','/owned/library/Film.']:
   item=ns['FileItem'](path='/owned/source/'+name,name=name,extension=Path(name).suffix[1:])
   result=ns['__rename_subtitles'](item,Path(target))
   rows.append({'default_sub':default,'source':item.path,'target':target,'result':result.as_posix()})
out={'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':'app/modules/filemanager/transhandler.py:833-890','source_sha256':hashlib.sha256(source.encode()).hexdigest(),'kind':__doc__,'patterns':patterns,'cases':rows}
Path(__file__).with_name('transfer-subtitle-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
root=Path(__file__).resolve().parents[2]
go='// Generated from pinned TransHandler.__rename_subtitles by generate-transfer-subtitle-oracle.py.\npackage app\n\nvar originalTransferSubtitlePatterns = []struct{Language, Tag, Pattern string}{\n'
for language,tag,key in [('zh-tw','.zh-tw','_zhtw_sub_re'),('zh-cn','.chi.zh-cn','_zhcn_sub_re'),('ja','.ja','_ja_sub_re'),('eng','.eng','_eng_sub_re')]:
 go+='\t{'+','.join(json.dumps(v,ensure_ascii=False)for v in [language,tag,patterns[key]])+'},\n'
go+='}\n';(root/'internal/app/plugin_original_transfer_subtitle_patterns.go').write_text(go)
print(len(rows),'fixed subtitle renaming cases;4 exact patterns exported to Go')
