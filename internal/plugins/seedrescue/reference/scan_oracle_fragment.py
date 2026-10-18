from urllib.parse import urlparse
namespace['StringUtils']=SimpleNamespace(get_url_domain=lambda raw:urlparse(raw or '').hostname)
scan_cases=[]
for scenario in ['manual','auto','hybrid','capacity','rss_empty','rss_failure','metadata_failure','duplicate','initial_limit','batch','missing_fields']:
 cfg={'enabled':True,'notify':False,'token':'fixture-token','target_site_id':1,'source_site_ids':[2],'rss_urls':'https://target.test/rss?passkey=fixture-secret','downloader':'qb','selection_mode':'auto' if scenario in ['auto','capacity'] else 'hybrid' if scenario=='hybrid' else 'manual'}
 if scenario=='capacity':cfg['max_download_count']=1
 if scenario=='initial_limit':cfg['initial_limit']=1
 p=Plugin();p.init_plugin(cfg);p._now=lambda:'2026-10-18T01:02:03+00:00';p._valid_config=lambda **kw:True;p._get_torrent=lambda h:None
 p.post_message=lambda **kw:None
 target_site=SimpleNamespace(id=1,name='Target',domain='target.test',url='https://target.test/',proxy=False,timeout=15,cookie='fixture-target-cookie',ua='FixtureUA')
 namespace['SiteOper']=lambda:SimpleNamespace(get=lambda sid:target_site)
 rss=[{'title':'Film.2026.WEB-DL','link':'https://target.test/details.php?id=42','enclosure':'https://target.test/download.php?id=42&passkey=fixture-secret','size':1024}]
 if scenario in ['initial_limit','batch','missing_fields']:rss.append({'title':'Other.2026.WEB-DL','link':'https://target.test/details.php?id=43','enclosure':'https://target.test/download.php?id=43&passkey=fixture-secret','size':2048})
 if scenario=='duplicate':rss=rss+rss
 if scenario=='rss_empty':rss=[]
 if scenario=='missing_fields':rss=[{}]
 namespace['RssHelper']=lambda:SimpleNamespace(parse=lambda *a,**kw:False if scenario=='rss_failure' else copy.deepcopy(rss))
 second_content=b'd4:infod4:name1:zee'
 def metadata(**kw):return None,(None if scenario=='metadata_failure' else second_content if 'id=43' in kw['url'] else content),None,None,'fixture metadata error'
 namespace['TorrentHelper']=lambda:SimpleNamespace(download_torrent=metadata)
 shash=core.info_hash_from_torrent(b'd4:infod4:name1:yee')
 p._iyuu=SimpleNamespace(get_seed_info=lambda hashes:({h:{'torrent':[{'sid':2,'torrent_id':'7','info_hash':shash}]} for h in hashes},''),get_torrent_url=lambda sid:('https://source.test/','download.php?id={id}'))
 namespace['SitesHelper']=lambda:SimpleNamespace(get_indexer=lambda domain:{'id':2,'name':'Source','url':'https://source.test/','schema':'nexus','cookie':'fixture-source-cookie','ua':'FixtureUA'},check=lambda domain:(False,None))
 p._fetch_source_detail=lambda domain,site,tid:namespace['DetailResult'](torrent={'title':'Source fixture','page_url':'https://source.test/details.php?id=7','enclosure':'https://source.test/download.php?id=7&passkey=fixture-secret','seeders':3,'downloadvolumefactor':0},status='ready')
 def browse(**kw):
  tid='43' if kw['keyword'].startswith('Other') else '42'
  return [SimpleNamespace(page_url='https://target.test/details.php?id='+tid,enclosure='https://target.test/download.php?id='+tid,seeders=2,peers=1,grabs=5,size=1024,downloadvolumefactor=0)]
 namespace['TorrentsChain']=lambda:SimpleNamespace(browse=browse)
 adds=[]
 def add(**kw):adds.append({'content_hex':kw['content'].hex(),'download_dir':kw['download_dir'],'category':kw['category'],'tag':kw['tag']});return True
 namespace['DownloaderHelper']=lambda:SimpleNamespace(get_service=lambda **kw:SimpleNamespace(instance=SimpleNamespace(add_torrent=add,get_downloading_torrents=lambda **kw:[{}] if scenario=='capacity' else [])))
 # Source wrapper classifies hash from downloaded source; the fixture must return
 # the correct source .torrent rather than reuse target metadata.
 def metadata2(**kw):
  raw=b'd4:infod4:name1:yee' if 'source.test' in kw['url'] else second_content if 'id=43' in kw['url'] else content
  return None,None if scenario=='metadata_failure' else raw,None,None,'fixture metadata error'
 namespace['TorrentHelper']=lambda:SimpleNamespace(download_torrent=metadata2)
 p._at_download_capacity=lambda:scenario=='capacity'
 p.scan()
 scan_cases.append({'scenario':scenario,'value':copy.deepcopy({'data':p.data,'adds':adds})})
(ROOT/'scan-oracle.json').write_text(json.dumps({'cases':scan_cases,'source':'actual scan/_run_scan and complete matching/probing/runtime control flow, trusted dependencies synthetic'},ensure_ascii=False,indent=2)+'\n')
