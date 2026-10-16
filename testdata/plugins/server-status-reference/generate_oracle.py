import ast
import datetime as dt
import hashlib
import json
import pathlib
import re
import types
import typing
from email.utils import parsedate_to_datetime
from urllib.parse import urlparse

ROOT=pathlib.Path(__file__).resolve().parent
reference=json.loads((ROOT/'installed-source.json').read_text())
clock=types.SimpleNamespace(now=1000.0)
fake_time=types.SimpleNamespace(time=lambda:clock.now)
state=types.SimpleNamespace(step={},calls=[])

class Response:
    def __init__(self,data):
        self.status_code=data.get('status',200)
        self.text=data.get('text','')
        self.content=data.get('content',self.text.encode())
        if isinstance(self.content,str): self.content=self.content.encode()
        self.headers=data.get('headers',{})
        self.raw=types.SimpleNamespace(version=data.get('protocol',11))
    def __bool__(self):return not 400<=self.status_code<600

class RequestUtils:
    def __init__(self,timeout=None):self.timeout=timeout
    def get_res(self,target):
        state.calls.append({'op':'status','url':target,'timeout':self.timeout})
        if state.step.get('type_error_once') and self.timeout is not None:raise TypeError('synthetic signature')
        clock.now+=state.step.get('duration',0.125)
        if state.step.get('http_error'):raise RuntimeError(state.step['http_error'])
        value=state.step.get('response')
        return None if value is None else Response(value)

def dns(host,port,type=None):
    state.calls.append({'op':'dns','host':host,'port':port})
    clock.now+=state.step.get('dns_seconds',0.02)
    if state.step.get('dns_error'):raise RuntimeError(state.step['dns_error'])
    return [(0,0,0,'',(ip,port)) for ip in state.step.get('dns',['192.0.2.1','2001:db8::1','192.0.2.1'])]

class FakeDateTime(dt.datetime):
    @classmethod
    def now(cls,tz=None):return dt.datetime.fromtimestamp(clock.now,tz)

class Socket:
    def __enter__(self):return self
    def __exit__(self,*args):pass

class TLS(Socket):
    def getpeercert(self):
        data=state.step.get('tls',{})
        return {'issuer':data.get('issuer',[[('organizationName','Fallback CA'),('commonName','Synthetic CA')]]),'subject':data.get('subject',[[('commonName','movie-pilot.org')]]),'notAfter':data.get('not_after','Nov 30 00:00:00 2026 GMT')}
    def version(self):return state.step.get('tls',{}).get('version','TLSv1.3')

class TLSContext:
    def wrap_socket(self,sock,server_hostname):
        state.calls.append({'op':'tls_wrap','host':server_hostname})
        if state.step.get('tls_error'):raise RuntimeError(state.step['tls_error'])
        return TLS()

def connect(target,timeout):
    state.calls.append({'op':'tls','host':target[0],'port':target[1],'timeout':timeout})
    clock.now+=state.step.get('tls_seconds',0.03)
    return Socket()

namespace={'_PluginBase':object,'List':typing.List,'Tuple':typing.Tuple,'Dict':typing.Dict,'Any':typing.Any,'Optional':typing.Optional,'re':re,'socket':types.SimpleNamespace(getaddrinfo=dns,SOCK_STREAM=1,create_connection=connect),'ssl':types.SimpleNamespace(create_default_context=TLSContext),'time':fake_time,'datetime':FakeDateTime,'timezone':dt.timezone,'parsedate_to_datetime':parsedate_to_datetime,'urlparse':urlparse,'logger':types.SimpleNamespace(info=lambda *a:None,warn=lambda *a:None),'RequestUtils':RequestUtils}
exec(compile(ast.parse(reference['class_source']),'<installed-MPServerStatus-methods>','exec'),namespace)
Plugin=namespace['MPServerStatus']
cases=[]
def save(name,kind,data,call):
    item={'name':name,'kind':kind,'input':data}
    try:item['expected']=call()
    except Exception as err:item['error']=str(err)
    cases.append(item)

good='Active connections: 12\nserver accepts handled requests\n1000 995 12345\nReading: 2 Writing: 3 Waiting: 7\n'
parse_cases=[good,'','Active connections: 1','100 200 300\nReading: 1 Writing: 2 Waiting: 3','Active connections: 0\n0 0 0\nReading: 0 Writing: 0 Waiting: 0','active CONNECTIONS:\t8\nheader\n10 9 100\nreading: 1 writing: 2 waiting: 5','Active connections: 9\nserver accepts\n10 9 100 999\nReading: 1 Writing: 2 Waiting: 6','Active connections: 7\nserver accepts\nbad\n2 3 4\nReading: 1 Writing: 1 Waiting: 5','Active connections: 3\nreading: 1 writing: 1 waiting: 1','Active connections:\u00a012\n1 2 3\nReading:\u20032 Writing: 3 Waiting: 7','Active connections: １２\n١٠ ٩ ١٠٠\nReading: ２ Writing: ３ Waiting: ７',good.replace('12345','1234567890123456789012345678901234567890'),good.replace('\n','\u2028'),good.replace('server accepts handled requests','not a header')]
for i,value in enumerate(parse_cases):save('parse-'+str(i),'parse',value,lambda value=value:Plugin._parse_status_text(value))
for config in [None,{}, {'enable':False},{'enable':True},{'enable':'false'},{'enable':[]},{'enable':[0]},{'enable':{}},{'enable':{'x':0}},[],0,False,'',17]:
    def form(config=config):
        p=Plugin();p.init_plugin(config);conf,model=p.get_form();return {'conf':conf,'model':model,'state':p.get_state(),'command':p.get_command(),'api':p.get_api()}
    save('form-'+str(len(cases)),'form',config,form)

formats={'_format_http_version':[[None],[10],[11],[20],[30],[True]],'_format_response_date':[[None],[''],['Fri, 16 Oct 2026 12:30:00 GMT'],['Fri, 16 Oct 2026 12:30:00 +0530'],['bad-date']], '_format_integer':[[None],[0],[-1234567],[123456789012345678901234567890],[-2.9],[2.9]],'_format_float':[[None],[0],[2.675],[-1.25,1]],'_format_percent':[[None],[0],[12.35]],'_format_seconds':[[None],[-0.1],[0],[0.0005],[0.9995],[1],[2.675]],'_format_rate':[[None,'次/秒'],[0,'次/秒'],[2.675,'个/秒']], '_format_sample_caption':[[None],[0],[1.25]],'_format_size':[[0],[1023],[1024],[1048575],[1048576]],'_format_ips':[[[]],[['192.0.2.1','2001:db8::1','192.0.2.2','192.0.2.3']],[['192.0.2.1'],'synthetic DNS error']], '_safe_percent':[[1,0],[1,3],[10,-5]],'_safe_ratio':[[1,0],[5,2]],'_safe_delta_rate':[[5,1,2],[1,5,2],[5,1,0],[5,5,10]],'_severity_color':[[0,1,10],[1,1,10],[10,1,10],[0,1,0],[1,1,0],[2,1,0]],'_latency_color':[[None,True],[0.9,True],[1,True],[2.9,True],[3,True],[0,False]],'_extract_certificate_name':[[None],[[]],[[[['organizationName','First'],['commonName','CN']]]],[[[['organizationName','First']]]]],'_format_connection_detail':[[1000,12.5],[None,None]]}
for method,args_list in formats.items():
    for args in args_list:save(method+str(len(cases)),'format',{'method':method,'args':args},lambda method=method,args=args:getattr(Plugin(),method)(*args))
for method,args in [('_safe_percent',[1.5,2.5]),('_safe_ratio',[0.1,0.3]),('_safe_percent',[True,True]),('_safe_delta_rate',[1.9,1.1,2]),('_format_integer',[True]),('_format_integer',[-123456789012345678901234567890])]:
    save(method+str(len(cases)),'format',{'method':method,'args':args},lambda method=method,args=args:getattr(Plugin(),method)(*args))
for days in [None,-1,0,6,7,29,30,365]:
    for enabled,error in [(True,''),(False,''),(True,'synthetic TLS error')]:
        data={'enabled':enabled,'days_left':days,'error':error}
        for method in ['_format_tls_days','_tls_color']:save(method+str(len(cases)),'format',{'method':method,'args':[data]},lambda method=method,data=data:getattr(Plugin,method)(data))

status=Plugin._parse_status_text(good)
metrics_sequences=[[(1000,status,.125),(1010,{**status,'accepts':1050,'handled':1040,'requests':12445},.25)],[(1000,status,.125),(1000,status,.25),(990,status,.125),(1000,status,.125)],[(1000,status,0),(1010,{**status,'accepts':1,'handled':2,'requests':3},0),(1020,{**status,'accepts':5,'handled':4,'requests':103},0)],[(1000,{},.5)],[(1000,Plugin._parse_status_text(parse_cases[4]),0)]]
for i,samples in enumerate(metrics_sequences):
    def metrics(samples=samples):
        p=Plugin();p.init_plugin({});out=[]
        for at,data,seconds in samples:clock.now=at;out.append(p._build_metrics(data,seconds))
        return out
    save('metrics-'+str(i),'metrics',[{'at':at,'status':data,'seconds':seconds} for at,data,seconds in samples],metrics)

base_response={'status':200,'text':good,'headers':{'Server':'nginx/1.25','Date':'Fri, 16 Oct 2026 12:30:00 GMT','Content-Type':'text/plain'},'protocol':11}
def runtime(data):
    p=Plugin();p.init_plugin(data.get('config',{'enable':True}));p._server_base=data.get('url','https://movie-pilot.org/status');out=[]
    for step in data['steps']:
        clock.now=step.get('at',1792152000.0);state.step=step;state.calls=[]
        action=step['action']
        if action=='init':p.init_plugin(step.get('config'));result=None
        elif action=='page':result=p.get_page()
        elif action=='dashboard':
            cols,attrs,elements=p.get_dashboard();result={'cols':cols,'attrs':attrs,'elements':elements}
        else:
            metrics,probe,dns_info,tls_info,error=p._load_status_snapshot(action=='load_details');result={'Metrics':metrics,'Probe':probe,'DNS':dns_info,'TLS':tls_info,'Error':error or ''}
        out.append({'result':result,'calls':state.calls[:],'last_sample':p._last_sample,'dns_cached':p._dns_cache is not None,'tls_cached':p._tls_cache is not None})
    return out

runtime_cases=[{'config':{'enable':False},'steps':[{'action':'page'},{'action':'dashboard','response':base_response}]}, {'steps':[{'action':'page','response':base_response},{'action':'dashboard','at':1792152010,'response':{**base_response,'text':good.replace('12345','12445')}},{'action':'page','at':1792152020,'response':base_response}]}, {'steps':[{'action':'page','response':None}]},{'steps':[{'action':'page','http_error':'synthetic unexpected RequestUtils error'}]},{'steps':[{'action':'page','response':{**base_response,'text':'<html>oops</html>'}}]}, {'steps':[{'action':'page','response':{**base_response,'status':503}}]}, {'steps':[{'action':'page','response':{**base_response,'status':302}}]}, {'steps':[{'action':'page','response':base_response,'dns_error':'synthetic DNS error','tls_error':'synthetic TLS error'},{'action':'page','at':1792152059,'response':base_response}]}, {'steps':[{'action':'load_details','response':base_response},{'action':'load_details','at':1792152060.1249,'response':base_response},{'action':'load_details','at':1792152060.125,'response':base_response},{'action':'load_details','at':1792155600.145,'response':base_response}]}, {'url':'http://movie-pilot.org:8080/status','steps':[{'action':'page','response':base_response}]},{'steps':[{'action':'load_dashboard','response':base_response},{'action':'load_details','at':1792152010,'response':None},{'action':'load_dashboard','at':1792152020,'response':{**base_response,'text':good.replace('12345','12445')}}]}, {'steps':[{'action':'page','response':base_response},{'action':'init','config':{'enable':True}},{'action':'page','at':1792152010,'response':base_response}]}, {'steps':[{'action':'load_details','response':base_response,'type_error_once':True}]}]
runtime_cases.append({'steps':[{'action':'load_details','response':base_response,'at':at,'duration':0,'dns_seconds':0,'tls_seconds':0} for at in [1000,1059.999,1060,4599.999,4600]]})
runtime_cases.append({'steps':[{'action':'load_details','response':base_response,'at':at,'duration':0,'dns_seconds':0,'tls_seconds':0,'dns_error':'negative DNS cache','tls_error':'negative TLS cache'} for at in [1000,1059.999,1060,4599.999,4600]]})
for i,data in enumerate(runtime_cases):save('runtime-'+str(i),'runtime',data,lambda data=data:runtime(data))
output={'provenance':{'source_sha256':reference['source_sha256'],'method_count':42,'execution':'actual installed class AST; only stdlib imports and synthetic RequestUtils/socket/ssl/time; no user config or external network'},'cases':cases}
(ROOT.parent/'server-status-original-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2,allow_nan=False)+'\n')
print(json.dumps({'cases':len(cases),'runtime_cases':len(runtime_cases),'sha256':hashlib.sha256((ROOT.parent/'server-status-original-oracle.json').read_bytes()).hexdigest()}))
