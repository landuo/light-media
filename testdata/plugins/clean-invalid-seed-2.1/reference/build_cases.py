import copy,json,pathlib
root=pathlib.Path(__file__).parent
obj=lambda **attrs:{'$object':attrs}
tracker=lambda msg='Success',status=2,url='https://tracker.synthetic/a',tier=0:dict(url=url,msg=msg,status=status,tier=tier)
def torrent(name='Synthetic',**kw):return obj(name=name,hash='a'*40,size=2048,category='',tags='',content_path='/dl/live',trackers=[tracker()],**kw)
def qb(name,tracks,**kw):
 a={'name':name,'hash':'a'*40,'size':2048,'category':'movies','tags':'own','content_path':'/dl/'+name,'trackers':tracks};a.update(kw);return {'$object':a,'$mapping':True}
def tr(name,stats=None,**kw):
 a={'name':name,'hashString':'b'*40,'total_size':4096,'downloadDir':'/dl','labels':['own'],'status':'seeding'}
 if stats is not None:a['trackerStats']=stats
 a.update(kw);return obj(**a)
err='torrent not registered with this tracker';invalid=qb('bad_1',[tracker(err,4)]);live=qb('good',[tracker()])
base={'enabled':True,'cron':'0 0 * * *','downloaders':['qB'],'label':'','notify':True,'notify_all':False,'delete_invalid_torrents':False,'delete_invalid_files':False,'detect_invalid_files':False,'label_only':False,'download_dirs':'','exclude_categories':'','exclude_labels':'','exclude_keywords':'','custom_error_msg':'','min_seeding_days':0,'more_logs':False,'onlyonce':False}
cases=[]
def case(id,cfg=None,torrents=None,services=None,steps=None,files=None,**other):
 c=copy.deepcopy(base);c.update(cfg or {});s=services if services is not None else [{'name':'qB','kind':'qbittorrent','torrents':copy.deepcopy(torrents if torrents is not None else [live,invalid])}]
 x=dict(id=id,config=c,services=s,**other)
 if steps is not None:x['steps']=steps
 if files is not None:x['files']=files
 cases.append(x);return x
case('detect_invalid')
case('delete_keep_data',{'delete_invalid_torrents':True})
case('delete_false_is_ignored',{'delete_invalid_torrents':True})['services'][0]['delete_return']=False
case('delete_exception_partial',{'delete_invalid_torrents':True})['services'][0]['delete_throw']=True
case('label_only_precedes_delete',{'label_only':True,'delete_invalid_torrents':True,'label':'mark'})
case('label_default',{'label_only':True})
case('label_none_not_default',{'label_only':True,'label':None})
case('category_excluded',{'delete_invalid_torrents':True,'exclude_categories':'movies'})
case('category_not_trimmed',{'delete_invalid_torrents':True,'exclude_categories':' movies '})
case('labels_count_each_match',{'delete_invalid_torrents':True,'exclude_labels':'own\nkeep'},[live,qb('bad',[tracker(err,4)],tags='own, keep,own')])
case('notify_all_empty_filters_source_error',{'delete_invalid_torrents':True,'notify_all':True})
case('notify_all_nonempty_filters',{'delete_invalid_torrents':True,'notify_all':True,'exclude_categories':'x','exclude_labels':'x'})
case('invalid_no_same_domain_evidence',torrents=[invalid])
case('unknown_tracker_proves_domain',torrents=[qb('waiting',[tracker('waiting',1)]),invalid])
case('domain_is_netloc_with_port',torrents=[qb('live',[tracker(url='https://tracker.synthetic:443/a')]),invalid])
case('tracker_tier_minusone_ignored',torrents=[live,qb('bad',[tracker(err,4,tier=-1)])])
case('no_trackers_not_deleted',torrents=[live,qb('bad',[])])
case('valid_tracker_prevents_invalid',torrents=[live,qb('mixed',[tracker(err,4),tracker('x',0)])])
case('paused_not_reported',{'notify_all':True},[qb('paused',[tracker('waiting',1)],state_enum=obj(is_paused=True))])
case('custom_error_exact_trim',{'custom_error_msg':'\n Custom deleted \n','delete_invalid_torrents':True},[live,qb('bad',[tracker('Custom deleted',4)])])
case('case_sensitive_error',torrents=[live,qb('bad',[tracker(err.upper(),4)])])
case('unknown_message_not_invalid',torrents=[live,qb('bad',[tracker('connection refused',4)])])
case('list_error') ['services'][0]['list_error']=True
case('list_throw')['services'][0]['list_throw']=True
case('empty_list',torrents=[])
case('no_selection',{'downloaders':[]})
case('none_selection',{'downloaders':None})
case('inactive',services=[{'name':'qB','kind':'qbittorrent','active':False}])
case('unsupported',services=[{'name':'qB','kind':'other'}])
case('false_instance',services=[{'name':'qB','kind':'qbittorrent','false_instance':True}])
case('invalid_tracker_url_caught',torrents=[live,qb('bad',[tracker(err,4,url='https://bad[bracket')])])
case('missing_hash_no_action',{'delete_invalid_torrents':True},[live,qb('bad',[tracker(err,4)],hash=None)])
case('qB_tracker_attribute_failure',torrents=[live,{'$object':{'name':'fault'},'$faults':{'trackers':'raise'}}])
# Transmission dict and object branches are distinct.
for id,attrs in [
 ('tr_dict_error',dict(trackerStats=[dict(announce='https://tracker.synthetic/a',lastAnnounceSucceeded=False,lastAnnounceResult=err,hasAnnounced=True,tier=0)])),
 ('tr_object_error',dict(trackerStats=[obj(announce='https://tracker.synthetic/a',last_announce_result=err,last_announce_succeeded=True,tier=0)])),
 ('tr_error_string',dict(error=2,errorString=' '+err+' ',trackerStats=[dict(announce='https://tracker.synthetic/a')])),
 ('tr_error_string_alias',dict(error=2,error_string=err,tracker_stats=[obj(announceUrl='https://tracker.synthetic/a',lastAnnounceResult=err,last_announce_succeeded=None,lastAnnounceSucceeded=False)])),
 ('tr_trackers_fallback',dict(trackers=[obj(announce='https://tracker.synthetic/a',lastAnnounceResult=err,lastAnnounceSucceeded=False)])),
 ('tr_list_fallback',dict(trackerList=['https://tracker.synthetic/a'])),
 ('tr_synthetic_error_trackerList',dict(error=1,errorString=err,trackerList=['https://tracker.synthetic/a'])),
 ('tr_error_code_only',dict(error=9,trackerStats=[])),
 ('tr_no_tracker',dict()),
 ]:
 goods=tr('good',[dict(announce='https://tracker.synthetic/a',lastAnnounceSucceeded=True,hasAnnounced=True)])
 bad=tr('bad',**attrs)
 case(id,{'downloaders':['TR'],'delete_invalid_torrents':True,'notify_all':False},services=[{'name':'TR','kind':'transmission','torrents':[goods,bad]}])
case('tr_labels_append',{'downloaders':['TR'],'label_only':True,'label':'mark'},services=[{'name':'TR','kind':'transmission','torrents':[tr('good',[dict(announce='https://tracker.synthetic/a',lastAnnounceSucceeded=True,hasAnnounced=True)]),tr('bad',[dict(announce='https://tracker.synthetic/a',lastAnnounceResult=err)],labels=['own','mark'])]}])
# File operations use virtual synthetic nodes only, no physical filesystem.
fs=[dict(path='/mp',kind='dir',mtime=0),dict(path='/mp/live',kind='file',size=4,mtime=0),dict(path='/mp/old',kind='dir',mtime=0),dict(path='/mp/old/a',kind='file',size=1024,mtime=0),dict(path='/mp/fresh',kind='file',size=10,mtime=1999999900),dict(path='/mp/keep_me',kind='file',size=22,mtime=0)]
for id,c in [('files_detect',{}),('files_delete',{'delete_invalid_files':True}),('files_min_days',{'min_seeding_days':'1'}),('files_keyword_blank',{'exclude_keywords':'skip\n'}),('files_multi_map',{'download_dirs':'/mp:/other,/dl'}),('files_original_trailing_slash',{'download_dirs':'/mp/:/dl'}),('files_empty_target_map',{'download_dirs':'/mp:'}),('files_bad_mapping',{'download_dirs':'not-a-map'}),('files_missing_dir',{'download_dirs':'/missing:/dl'})]:
 cfg={'download_dirs':'/mp:/dl','exclude_keywords':'keep'};cfg.update(c)
 case(id,cfg,[live],steps=[{'action':'init','config':{**base,**cfg}},{'action':'files'}],files=fs)
case('files_empty_unconditional_notice',{'notify':False},[live],steps=[{'action':'init','config':{**base,'notify':False}},{'action':'files'}],files=fs)
case('files_stat_error',{'download_dirs':'/mp:/dl','min_seeding_days':1},[live],steps=[{'action':'init','config':{**base,'download_dirs':'/mp:/dl','min_seeding_days':1}},{'action':'files'}],files=[dict(path='/mp',kind='dir'),dict(path='/mp/a',kind='file',stat_error=True)])
case('files_delete_exception_partial',steps=[{'action':'init','config':{**base,'download_dirs':'/mp:/dl','delete_invalid_files':True}},{'action':'files'}],files=[dict(path='/mp',kind='dir'),dict(path='/mp/a',kind='file',size=1,delete_error=True)])
case('clean_file_scan_per_service',{'downloaders':['qB','qB2'],'detect_invalid_files':True,'download_dirs':'/mp:/dl'},services=[{'name':'qB','kind':'qbittorrent','torrents':[live]},{'name':'qB2','kind':'qbittorrent','torrents':[live]}],files=fs)
for command in ['detect_invalid_torrents','delete_invalid_torrents','detect_invalid_files','delete_invalid_files','toggle_notify_all','other']:
 cfg={**base,'download_dirs':'/mp:/dl','detect_invalid_files':True}
 case('command_'+command,steps=[{'action':'init','config':cfg},{'action':'command','event':{'action':command,'channel':'synthetic','user':'user'}}],files=fs)
case('command_failure_keeps_temp_flags',steps=[{'action':'init','config':{**base,'detect_invalid_files':True}},{'action':'command','event':{'action':'delete_invalid_torrents'}}])['services'][0]['delete_throw']=True
for value in [None,'invalid','2','２','1_0',False,1.9,[],{}]:
 cfg={**base,'min_seeding_days':value,'onlyonce':True};case('init_min_'+str(value),steps=[{'action':'init','config':cfg},{'action':'state'},{'action':'service'},{'action':'stop'}])
case('init_empty_missing_downloaders',steps=[{'action':'init','config':{}},{'action':'clean'}])
case('cron_invalid',steps=[{'action':'init','config':{**base,'cron':'bad'}},{'action':'service'}])
case('reload_stops_once',steps=[{'action':'init','config':{**base,'onlyonce':True}},{'action':'init','config':{**base,'onlyonce':False}},{'action':'service'}])
# Real installed native library objects; qB trackers retain lazy reads.
qraw=lambda name,h,state='uploading':dict(name=name,hash=h,size=2048,category='movies',tags='own',content_path='/dl/'+name,state=state)
tstats=lambda msg,success:dict(announce='https://tracker.synthetic/a',lastAnnounceResult=msg,lastAnnounceSucceeded=success,tier=0)
traw=lambda name,h,**kw:dict(id=1,name=name,hashString=h,totalSize=4096,downloadDir='/dl',labels=['own'],status=6,error=0,errorString='',trackerStats=[tstats('Success',True)],trackers=[dict(id=0,announce='https://tracker.synthetic/a',scrape='',tier=0)],**kw)
for mode in ('detect','delete','label','paused'):
 cfg={'delete_invalid_torrents':mode=='delete','label_only':mode=='label','notify_all':mode=='paused'}
 good=qraw('good','a'*40);bad=qraw('bad','b'*40,state='pausedUP' if mode=='paused' else 'uploading')
 tracks={'a'*40:[tracker()],'b'*40:[tracker('waiting',1) if mode=='paused' else tracker(err,4)]}
 case('native_qB_'+mode,cfg,services=[dict(name='qB',kind='qbittorrent',native=True,native_torrents=[good,bad],tracker_by_hash=tracks)])
for mode in ('stats','error_code_skipped','label','files'):
 good=traw('good','a'*40);bad=traw('bad','b'*40);bad['trackerStats']=[tstats(err,False)]
 if mode=='error_code_skipped':bad['error']=2;bad['errorString']=err
 cfg={'downloaders':['TR'],'delete_invalid_torrents':mode=='stats','label_only':mode=='label','download_dirs':'/mp:/dl'}
 if mode=='files':steps=[{'action':'init','config':{**base,**cfg}},{'action':'files'}]
 else:steps=None
 case('native_TR_'+mode,cfg,services=[dict(name='TR',kind='transmission',native=True,native_torrents=[good,bad])],steps=steps,files=fs if mode=='files' else None)
for selected in ('native_qB_delete','native_TR_stats'):
 x=copy.deepcopy(next(v for v in cases if v['id']==selected));x['id']=selected+'_more_logs';x['config']['more_logs']=True;cases.append(x)
(root/'cases.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2)+'\n');print(len(cases))
