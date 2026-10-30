#!/usr/bin/env python3
"""独立审查：完整导入固定源 wrapper，仅隔离日志，使用真实 owned daemon。"""
import hashlib
import importlib.util
import importlib.metadata
import json
import logging
import os
from pathlib import Path
import subprocess
import sys
import time
import types
from urllib.parse import urlsplit

logging.disable(logging.CRITICAL)
sys.dont_write_bytecode = True
SOURCE = Path(os.environ['MOVIEPILOT_SOURCE'])
# 固定源文件内容，不把路径名称当作版本证明。
for relative, digest in {
    'app/modules/transmission/transmission.py': '6fc20e208b86e9e68ada074643f82b88270c2e51749e460ad881b10f744cf676',
    'app/utils/url.py': '0b83efc129ebc3a08e658ae61929e999bee671148680901d5150db8efb9e504b',
}.items():
    assert hashlib.sha256((SOURCE/relative).read_bytes()).hexdigest() == digest, 'pinned source content changed'
out = Path(sys.argv[1])
assert not out.exists()
assert os.environ.get('DOWNLOADER_TEST_INSTANCE')
url = urlsplit(os.environ['TRANSMISSION_URL'])
assert url.hostname == '127.0.0.1' and url.path == '/transmission/rpc'
logger = types.ModuleType('app.log')
logger.logger = types.SimpleNamespace(**{k:lambda *a,**kw:None for k in ['info','debug','warning','error']})
sys.modules['app.log'] = logger

def load(name, path):
    """导入原模块全部字节；不改写 AST、方法、返回值或过滤规则。"""
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module

load('app.utils.url', SOURCE/'app/utils/url.py')
original = load('owned_fixed_transmission', SOURCE/'app/modules/transmission/transmission.py')
wrapper = original.Transmission(host=url.hostname, port=url.port, username=os.environ['TRANSMISSION_USERNAME'], password=os.environ['TRANSMISSION_PASSWORD'])
assert wrapper.trc
client = wrapper.trc
assert importlib.metadata.version('transmission-rpc') == '7.0.11'

def encode(value):
    """仅为独立合成、private、无 tracker 的自有输入生成 bencode。"""
    if isinstance(value,int): return b'i'+str(value).encode()+b'e'
    if isinstance(value,str): value=value.encode()
    if isinstance(value,bytes): return str(len(value)).encode()+b':'+value
    if isinstance(value,list): return b'l'+b''.join(map(encode,value))+b'e'
    return b'd'+b''.join(encode(k)+encode(value[k]) for k in sorted(value))+b'e'

root = Path(os.environ['DOWNLOADER_TEST_INSTANCE'])/'data'
tasks=[]
for i,(name,complete,labels) in enumerate([
    ('independent-zeta.bin',True,['alpha','双语']),
    ('independent-alpha.bin',False,['alpha','beta']),
    ('independent-middle.bin',True,[' \talpha\u2003','\x1cbeta\x1f','gamma']),
]):
    payload=bytes([90-i])*32768
    info={'name':name,'length':len(payload),'piece length':16384,'pieces':b''.join(hashlib.sha1(payload[n:n+16384]).digest() for n in range(0,len(payload),16384)),'private':1}
    torrent=encode({'info':info})
    if complete: (root/name).write_bytes(payload)
    row=client.add_torrent(torrent,download_dir=str(root),paused=True,labels=labels)
    client.verify_torrent(row.id)
    until=time.monotonic()+20
    while True:
        current=client.get_torrent(row.id)
        if str(current.status)=='stopped' and current.percent_done==int(complete):break
        assert time.monotonic()<until
        time.sleep(.05)
    tasks.append({'id':row.id,'hash':row.hashString,'name':name,'present':complete,'data_sha256':hashlib.sha256(payload).hexdigest(),'torrent_sha256':hashlib.sha256(torrent).hexdigest()})

phase=os.environ.get('INDEPENDENT_PHASE','paused')
assert phase in ('paused','active','queued')
if phase=='queued':
    client.set_session(download_queue_enabled=True,download_queue_size=0,seed_queue_enabled=True,seed_queue_size=0,queue_stalled_enabled=False)
if phase!='paused':
    client.start_torrent([x['id'] for x in tasks])
    expected=['seed pending','download pending','seed pending'] if phase=='queued' else ['seeding','downloading','seeding']
    until=time.monotonic()+20
    while [str(client.get_torrent(x['id']).status) for x in tasks]!=expected:
        assert time.monotonic()<until,'owned states failed to settle'
        time.sleep(.05)
    time.sleep(1.1)
cases=[]
def collect(name, **request):
    """原版实际调用作为结果来源；附加断言只验证输入确有区分力。"""
    method=request.pop('query_method','get_torrents')
    result=getattr(wrapper,method)(**request)
    if method=='get_torrents': rows,error=result
    else: rows,error=result or [],result is None
    cases.append({'name':name,'request':dict(downloader_id='owned-tr',query_method=method,**request),'error':error,'torrents':[{'fields':dict(row.fields)} for row in rows]})
    return cases[-1]

allrows=collect('independent-nil-all')
assert len(allrows['torrents'])==3 and not allrows['error']
assert {row['fields']['name'] for row in allrows['torrents']}=={x['name'] for x in tasks}
assert all('light-media' not in x['fields']['labels'] for x in allrows['torrents'])
for name,ids in [
    ('empty-list',[]),('explicit-null',None),('reversed-numeric',[x['id'] for x in tasks[::-1]]),
    ('reverse-mixed',[tasks[2]['hash'],tasks[0]['id']]),('repeated',[tasks[1]['id']]*3),
    ('true',True),('false',False),('zero',0),('float-integer',1.0),('fraction',1.5),
    ('large-int',2**80),('negative',-1),('empty-string',''),('missing','f'*40),
    ('uppercase',tasks[0]['hash'].upper()),('numeric-string',str(tasks[0]['id'])),
    ('recently-in-list',['recently-active']),('recently-active','recently-active'),
    ('single-numeric',tasks[1]['id']),('single-hash',tasks[2]['hash']),
]: collect('independent-ids-'+name,ids=ids)
for name,value in [('null',None),('empty',''),('empty-list',[]),('stopped','stopped'),('seeding','seeding'),('space-pending','download pending'),('underscore-pending','download_pending'),('case-sensitive','Stopped'),('list',['stopped','seeding'])]:
    collect('independent-status-'+name,status=value)
for name,value in [('null',None),('empty',''),('empty-list',[]),('alpha','alpha'),('beta','beta'),('subset',['alpha','beta']),('duplicates',['alpha','alpha']),('comma','alpha,beta'),('comma-space','alpha, beta'),('unicode','双语'),('raw-label-whitespace',' \talpha\u2003'),('empty-member',['']),('empty-comma',','),('control-strip','beta')]:
    collect('independent-tags-'+name,tags=value)
for method in ['get_completed_torrents','get_downloading_torrents']:
    collect('independent-'+method,query_method=method)
collect('independent-intersection',ids=[tasks[0]['id'],tasks[1]['id']],status={'paused':'stopped','active':'downloading','queued':'download pending'}[phase],tags=['alpha','beta'])
assert next(x for x in cases if x['name']=='independent-intersection')['torrents'][0]['fields']['id']==tasks[1]['id']
assert len(next(x for x in cases if x['name']=='independent-tags-alpha')['torrents'])==3
assert len(next(x for x in cases if x['name']=='independent-tags-subset')['torrents'])==2
assert len(next(x for x in cases if x['name']=='independent-tags-comma-space')['torrents'])==0
repeated=next(x for x in cases if x['name']=='independent-ids-repeated')['torrents']
assert repeated and all(x['fields']['id']==tasks[1]['id'] for x in repeated)
wrong=original.Transmission(host=url.hostname,port=url.port,username=os.environ['TRANSMISSION_USERNAME'],password='owned-invalid-auth')
assert wrong.get_torrents()==([],True)
assert wrong.get_completed_torrents() is None and wrong.get_downloading_torrents() is None
assert all(not client.get_torrent(x['id']).peers for x in tasks)
doc={'source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_import':'entire module via spec_from_file_location; real url module; null logger only','source_files':{str(p.relative_to(SOURCE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [SOURCE/'app/modules/transmission/transmission.py',SOURCE/'app/utils/url.py']},'dependencies':{n:importlib.metadata.version(n) for n in ['transmission-rpc','requests','urllib3']},'phase':phase,'server_version':client.get_session().version,'tasks':tasks,'cases':cases}
out.write_text(json.dumps(doc,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'independent_source_cases':len(cases),'real_module_import':True,'no_owned_ui_tag':True,'whitespace_subset_distinguishes':True,'zero_peers':True,'oracle_sha256':hashlib.sha256(out.read_bytes()).hexdigest()}),flush=True)
env=os.environ.copy()
env['TRANSMISSION_QUERY_ORACLE']=str(out)
code=subprocess.run(sys.argv[2:],env=env).returncode
post={'target_exit':code,'zero_peers':all(not client.get_torrent(x['id']).peers for x in tasks),'retained_data':all(hashlib.sha256((root/x['name']).read_bytes()).hexdigest()==x['data_sha256'] for x in tasks if x['present'])}
assert post['zero_peers'] and post['retained_data']
out.with_name(out.stem+'-postflight.json').write_text(json.dumps(post,indent=2)+'\n')
sys.exit(code)
