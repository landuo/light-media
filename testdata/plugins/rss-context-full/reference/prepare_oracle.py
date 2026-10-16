"""Build a read-only reference program; original AST statements are not rewritten.
Only application I/O/recognition and library facts are injected. Real installed
MetaInfo, MetaBase, both anime/video parsers, Context/MediaInfo/TorrentInfo execute.
"""
from pathlib import Path
import json
root=Path(__file__).resolve().parents[1]
prefix=(Path(__file__).resolve().parents[4]/'testdata/plugins/metainfo-reference/installed_full_metainfo.py').read_text().split('def evaluate(')[0]
base_sub=dict(name='Synthetic Show',type='电视剧',season=4,year=None,tmdbid=44,doubanid=None,bangumiid=None,anilistid=None,media_source=None,media_id=None,episode_group=None,custom_words=None,best_version=False)
base_media=dict(type='电视剧',title='Synthetic Show',tmdb_id=44,season=4)
cases=[]
def add(name,**kw):
 case=dict(name=name,title='Synthetic.Show.S04E02.1080p.WEB-DL',description=None,category=None,enclosure='synthetic://torrent',stype='rss',global_words=[],selection_global_words=None,subscription=dict(base_sub),target=dict(base_media),produce=dict(base_media),preflight=None,local=None,preflight_times=1,missing=None,meta_override={},media_override={},context_delete=[],context_override={},allow_title=True,sites=[],domains=[])
 sub=kw.pop('sub',{});case['subscription'].update(sub);case.update(kw);cases.append(case)
add('known_s04')
add('no_enclosure',enclosure=None)
add('spider',stype='spider')
add('nil_preflight',produce=None)
add('empty_preflight_resets',produce=None,preflight={},preflight_times=3)
add('three_nil_retry_cap',produce=None,preflight_times=5)
add('preflight_identified',produce=None,preflight=dict(base_media))
add('old_cache_membership',context_delete=['media_recognize_fail_count','resource_source','candidate_recognized','match_source','media_info_is_target'])
add('present_null_fail_count',context_override={'media_recognize_fail_count':None},preflight_times=1)
add('present_title_flags',context_override={'match_source':'title','candidate_recognized':False,'media_info_is_target':True})
add('local_same_value_applies',sub={'custom_words':'Synthetic => Synthetic'},local=dict(base_media))
add('local_noop_does_not_apply',sub={'custom_words':'Other => Synthetic'},local=dict(base_media))
add('local_from_org_not_raw',global_words=['Synthetic => Changed'],sub={'custom_words':'Synthetic => New'},selection_global_words=['Changed => Further'],local=dict(base_media))
add('local_new_global_not_reinterpreted',global_words=['Synthetic => Changed'],selection_global_words=['Synthetic => Different'])
add('local_rebuild_raw',global_words=['Synthetic => Changed'],sub={'custom_words':'Changed => Final'},local=dict(base_media))
add('local_failure_old_identity',produce=dict(base_media,tmdb_id=999),sub={'custom_words':'Synthetic => Synthetic'},local=None)
add('local_empty_old_identity',produce=dict(base_media,tmdb_id=999),sub={'custom_words':'Synthetic => Synthetic'},local={})
add('local_failure_title_reject',produce=dict(base_media),sub={'custom_words':'Synthetic => Different'},local=None)
add('local_rebuild_identity',sub={'custom_words':'Synthetic => New'},local=dict(base_media))
add('local_multiline_comments',sub={'custom_words':'# hello\n\nSynthetic => Synthetic\n '},local=dict(base_media))
add('local_invalid_regex',sub={'custom_words':'['},local=dict(base_media))
add('local_combined_offset_fails',sub={'custom_words':'Synthetic => Changed && Changed <> 2 >> X'},local=dict(base_media))
add('local_episode_group_presence',sub={'custom_words':'Synthetic => Synthetic','episode_group':'synthetic-group'},local=dict(base_media))
add('source_category_correction',title='Synthetic Show 1080p',category='电视剧',produce=dict(base_media),sub={'season':1})
add('rebuild_loses_category',title='Synthetic Show 1080p',category='电视剧',produce=dict(base_media),sub={'season':1,'custom_words':'Synthetic => Synthetic'},local=dict(base_media))
add('tmdb_conflict',produce=dict(base_media,tmdb_id=999))
add('tmdb_int_string_conflict',produce=dict(base_media,tmdb_id='44'))
add('douban_conflict',produce=dict(base_media,douban_id=9))
add('generic_conflict_core_allows',produce=dict(type='电视剧',title='Other',source='custom',media_id='other'))
add('bangumi_conflict_core_allows',produce=dict(type='电视剧',title='Other',bangumi_id=9))
add('anilist_conflict_core_allows',produce=dict(type='电视剧',title='Other',anilist_id=9))
add('media_type_conflict',produce=dict(base_media,type='电影'))
add('season_absent_s04',title='Synthetic Show 1080p')
add('season_absent_s01',title='Synthetic Show 1080p',sub={'season':1})
add('season_zero',title='Synthetic Show S00E02',sub={'season':0})
add('multiple_seasons',title='Synthetic Show S04-S06')
add('reversed_seasons',title='Synthetic Show S04-S02')
add('missing_intersection',missing=[2,3])
add('missing_reject',missing=[1,3])
add('missing_no_episode',title='Synthetic Show S04 1080p',missing=[1,3])
add('movie_title_fallback',title='Synthetic Film 2024 1080p',produce=None,sub={'name':'Synthetic Film','type':'电影','season':None,'year':'2024'},target=dict(type='电影',title='Synthetic Film',tmdb_id=44,year='2024'))
add('movie_missing_year_reject',title='Synthetic Film 1080p',produce=None,sub={'name':'Synthetic Film','type':'电影','season':None,'year':'2024'},target=dict(type='电影',title='Synthetic Film',tmdb_id=44,year='2024'))
add('fallback_alias_removed',title='Alias Show S04E02',produce=None,target=dict(base_media,names=['Alias Show']))
add('explicit_id_fallback',title='Other S04E02 {tmdb-44}',produce=None)
add('description_chinese',title='Other S04E02',description='合成剧集',produce=None,sub={'name':'合成剧集'},target=dict(base_media,title='合成剧集'))
add('plugin_zero_identity',produce=dict(type='电视剧',title='Synthetic Show',tmdb_id=0),sub={'tmdbid':0},target=dict(type='电视剧',title='Synthetic Show',tmdb_id=0))
add('plugin_id_string_numeric',produce=dict(base_media,tmdb_id='44'))
add('plugin_explicit_range',title='Synthetic Show S03-S05')
add('plugin_reversed_range',title='Synthetic Show S05-S03')
add('plugin_no_explicit_media_season',title='Synthetic Show 1080p',produce=dict(base_media,season=5))
add('plugin_no_identity_title',produce=None,preflight_times=0)
add('plugin_conflict_no_title',produce=dict(base_media,tmdb_id=999))
add('plugin_generic_identity',produce=dict(type='电视剧',title='Synthetic Show',source=' Custom ',media_id=' 42 '),sub={'tmdbid':None,'media_source':'custom','media_id':'42'},target=dict(type='电视剧',title='Synthetic Show',source='custom',media_id='42'))
add('site_reject',sites=[2])
add('site_accept',sites=[1])
(root/'cases.json').write_text(json.dumps([dict(c,mode=mode,name=c['name']+'/'+mode)for c in cases for mode in ['rust','legacy']],ensure_ascii=False,indent=2))
body=r'''
import copy,typing
EXPECTED.update(EXTRA_SHA)
contextmod=load('app/core/context.py',dict(base,MetaInfo=meta.MetaInfo))
identitymod=load('app/utils/media.py')
MediaInfo,TorrentInfo,Context=contextmod.MediaInfo,contextmod.TorrentInfo,contextmod.Context
resolve_media_identity=identitymod.resolve_media_identity

def methods(path,classname,names,env):
 source=(ROOT/path).read_bytes();sha[path]=hashlib.sha256(source).hexdigest();assert sha[path]==EXPECTED[path]
 cls=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef)and n.name==classname)
 cls.bases=[];cls.decorator_list=[];cls.body=[n for n in cls.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef))and n.name in names]
 ns=dict(typing.__dict__,**env);ns.update(__name__='source_reference',Optional=Optional)
 exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'<installed '+path+'>','exec'),ns)
 return ns[classname]
common=dict(typing.__dict__,**base);common.update(re=__import__("re"),Context=Context,MediaInfo=MediaInfo,TorrentInfo=TorrentInfo,resolve_media_identity=resolve_media_identity,copy=copy,MetaBase=base['MetaBase'])
def checked_source_ast(path):
 source=(ROOT/path).read_bytes();sha[path]=hashlib.sha256(source).hexdigest();assert sha[path]==EXPECTED[path],('installed-source changed',path,sha[path]);return ast.parse(source)
tor_tree=checked_source_ast('app/chain/torrents.py');sub_tree=checked_source_ast('app/chain/subscribe.py')
cls=next(n for n in tor_tree.body if isinstance(n,ast.ClassDef))
names=[n.name for n in cls.body if isinstance(n,ast.FunctionDef)and(n.name.startswith('_context')or n.name.startswith('_normalize')or n.name in ['_ensure_context_compatibility','_get_media_id_match_source','get_subscribe_cache_candidates','_mark_title_match_candidate'])]
TorrentsChain=methods('app/chain/torrents.py','TorrentsChain',names,common)
TorrentHelper=methods('app/helper/torrent.py','TorrentHelper',['match_torrent'],common)
subcls=next(n for n in sub_tree.body if isinstance(n,ast.ClassDef)and n.name=='SubscribeChain')
getsource=next(n for n in subcls.body if isinstance(n,ast.FunctionDef)and n.name=='__get_media_id_match_source')
sub_ns=dict(common);exec(compile(ast.fix_missing_locations(ast.Module(body=[getsource],type_ignores=[])),'<installed subscribe source matcher>','exec'),sub_ns)
self=NS(_get_media_id_match_source=TorrentsChain._get_media_id_match_source,__get_media_id_match_source=sub_ns['__get_media_id_match_source'],get_sub_sites=lambda s:s._sites)

# Statements are selected by fixed installed-source locations. The prefilter is
# the unmodified source body through type/ID/season/missing-episode gates. Only the
# best-version branch after that gate is deliberately a declared continuation.
refresh=next(n for n in cls.body if isinstance(n,ast.FunctionDef)and n.name=='refresh')
prodloop=next(n for n in ast.walk(refresh)if isinstance(n,ast.For)and any(getattr(x,'lineno',0)==597 for x in n.body))
prod=[copy.deepcopy(n)for n in prodloop.body if 592<=n.lineno<=631]
match=next(n for n in subcls.body if isinstance(n,ast.FunctionDef)and n.name=='match')
preflight=copy.deepcopy(next(n for n in ast.walk(match)if isinstance(n,ast.If)and n.lineno==1751))
coreloop=next(n for n in ast.walk(match)if isinstance(n,ast.For)and any(getattr(x,'lineno',0)==1867 for x in n.body))
core=[copy.deepcopy(n)for n in coreloop.body if 1867<=n.lineno<=1959]
for n in ast.walk(core[-1]):
 if isinstance(n,ast.If)and n.lineno==1973:n.orelse=[]

def function(name,args,body):
 fn=ast.FunctionDef(name=name,args=ast.arguments(posonlyargs=[],args=[ast.arg(arg=x)for x in args],kwonlyargs=[],kw_defaults=[],defaults=[]),body=body,decorator_list=[])
 exec(compile(ast.fix_missing_locations(ast.Module(body=[fn],type_ignores=[])),'<installed source '+name+'>','exec'),env)
env=dict(common,self=self,MediaType=media_type,MetaInfo=meta.MetaInfo,TorrentHelper=TorrentHelper,WordsMatcher=base['WordsMatcher'])
function('produce',['torrent','stype'],ast.parse('result=None\nfor _ in [0]:\n pass').body[:1]+[ast.For(target=ast.Name(id='_',ctx=ast.Store()),iter=ast.List(elts=[ast.Constant(0)],ctx=ast.Load()),body=prod+[ast.Assign(targets=[ast.Name(id='result',ctx=ast.Store())],value=ast.Name(id='context',ctx=ast.Load()))],orelse=[])]+[ast.Return(value=ast.Name(id='result',ctx=ast.Load()))])
function('preflight',['context'],[preflight])
function('core',['context','subscribe','meta','mediainfo','missing'],ast.parse('custom_words_list=subscribe.custom_words.split("\\n") if subscribe.custom_words else None\nwordsmatcher=WordsMatcher()\nmediakey="synthetic"\nno_exists={mediakey:{subscribe.season:NS(episodes=missing)}} if missing is not None else None\neligible=False\n_context=None').body+[ast.For(target=ast.Name(id='_',ctx=ast.Store()),iter=ast.List(elts=[ast.Constant(0)],ctx=ast.Load()),body=core+ast.parse('eligible=True').body,orelse=[])]+ast.parse('return eligible,_context').body)
env['NS']=NS
subfunctions=[copy.deepcopy(n)for n in sub_tree.body if isinstance(n,ast.FunctionDef)and n.name in ['build_subscribe_meta','_subscribe_recognize_kwargs']]
for n in subfunctions:n.returns=None;n.args.args[0].annotation=None
exec(compile(ast.fix_missing_locations(ast.Module(body=subfunctions,type_ignores=[])),'<installed subscription target>','exec'),env)

calls=[];phase='';response=None
class ControlledMediaChain:
 def recognize_by_meta(self,m,**kw):return recognize(m,kw,'recognize_by_meta')
def media(fields):
 if fields is None:return None
 fields=copy.deepcopy(fields)
 if isinstance(fields.get('type'),str):fields['type']=media_type(fields['type'])
 return MediaInfo(**fields)
def recognize(m,kw,api):
 calls.append(dict(phase=phase,api=api,meta=m.to_dict(),kwargs=kw))
 return media(response)
env['MediaChain']=ControlledMediaChain

def configure(words,mode):
 values.clear();values.update(words=words,release_groups=[],customization=[]);settings.RUST_ACCEL=mode=='rust';meta.clear_rust_parse_options_cache();runtime.supports_extended_media_ids.cache_clear()
def view(c):
 if c is None:return None
 try:return c.to_dict()
 except BaseException as err:return dict(serialization_error=type(err).__name__,type_is_enum=isinstance(c.media_info.type,Enum),media_fields=vars(c.media_info))

def run(case):
 global phase,response,calls
 calls=[];configure(case['global_words'],case['mode']);torrent=TorrentInfo(site=1,title=case['title'],description=case['description'],category=case['category'],enclosure=case['enclosure']);phase='produce';response=case['produce'];c=env['produce'](torrent,case['stype'])
 if c is None:return dict(produced=None,calls=calls)
 for k,v in case['meta_override'].items():setattr(c.meta_info,k,v)
 for k,v in case['media_override'].items():setattr(c.media_info,k,v)
 for k in case['context_delete']:vars(c).pop(k,None)
 for k,v in case['context_override'].items():setattr(c,k,v)
 TorrentsChain._ensure_context_compatibility({'synthetic':[c]},case['stype']);produced=copy.deepcopy(view(c))
 phase='preflight';response=case['preflight']
 for _ in range(case['preflight_times']):env['preflight'](c)
 preflight_result=copy.deepcopy(view(c))
 configure(case['global_words']if case['selection_global_words']is None else case['selection_global_words'],case['mode'])
 sub=NS(**case['subscription'],_sites=case['sites']);target_meta=env['build_subscribe_meta'](sub);phase='target';response=case['target'];kwargs=dict(mtype=target_meta.type,**env['_subscribe_recognize_kwargs'](sub),episode_group=sub.episode_group,cache=False);target=recognize(target_meta,kwargs,'recognize_media');target_before=copy.deepcopy(target.to_dict());target.clear()
 phase='local_rebuild';response=case['local'];eligible,selected=env['core'](c,sub,target_meta,target,case['missing'])
 plugin=TorrentsChain();plugin.get_torrents=lambda stype=None:{'synthetic':[c]};candidates=plugin.get_subscribe_cache_candidates(sub,stype=case['stype'],allow_title_match=case['allow_title'])
 return dict(produced=produced,preflight=preflight_result,target_before_clear=target_before,target_meta=target_meta.to_dict(),eligible=eligible,selected=view(selected),cached_final=view(c),plugin=[view(x)for x in candidates],calls=calls,alias=dict(selected_meta_is_cache=bool(selected and selected.meta_info is c.meta_info),selected_media_is_cache=bool(selected and selected.media_info is c.media_info),selected_media_is_target=bool(selected and selected.media_info is target),selected_torrent_is_cache=bool(selected and selected.torrent_info is c.torrent_info),plugin_meta_is_cache=bool(candidates and candidates[0].meta_info is c.meta_info),plugin_media_is_cache=bool(candidates and candidates[0].media_info is c.media_info)))
results=[]
for case in CASES:
 try:results.append(dict(case=case,result=run(case)))
 except BaseException as err:results.append(dict(case=case,error=type(err).__name__,message=str(err)))
print(json.dumps(dict(source_sha256=sha,python=sys.version,distributions={name:importlib.metadata.version(name)for name in ['moviepilot-rust','anitopy','cn2an','Pinyin2Hanzi','regex']},unicode_version=unicodedata.unidata_version,scope='real installed objects; unmodified source producer/preflight/core prefix and plugin candidate methods; normalized synthetic recognition responses; best-version/rules/library continuation excluded explicitly',cases=results),ensure_ascii=False,indent=2,default=lambda value:value.value if isinstance(value,Enum)else sorted(value)if isinstance(value,set)else (_ for _ in ()).throw(TypeError(type(value).__name__))))
'''
extra=json.loads((root/'source/manifest.json').read_text())
(root/'reference/installed_rss_context.py').write_text(prefix+'\nEXTRA_SHA='+repr(extra)+'\nCASES='+repr(json.loads((root/'cases.json').read_text()))+'\n'+body)
