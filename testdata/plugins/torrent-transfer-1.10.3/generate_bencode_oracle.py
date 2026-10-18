import sys,pathlib,base64,subprocess,json,resource,os
root=pathlib.Path(__file__).resolve().parent
samples=[b'',b'garbage',b'i0e',b'i-1e',b'i03e',b'i-0e',b'i+2e',b'i2',b'0:',b'01:a',b'2:a',b'1:aa',b'd1:ai1e1:ai2ee',b'd1:bi1e1:ai2ee',b'di1ei2ee',b'd1:ae',b'de',b'le',b'llee',b'd4:infod4:name1:x6:pieces2:\xff\xfeee',b'l2:\xff\xfe2:\xc3\xa9e',b'999999999999999999999999:a',b'ie',b'i999999999999999999999999999999999999999999e',b'li1ei-1e0:e']
script="""import sys,base64,json,resource
resource.setrlimit(resource.RLIMIT_CORE,(0,0))
resource.setrlimit(resource.RLIMIT_CPU,(2,2))
sys.path.insert(0,sys.argv[1])
from bencode import bdecode,bencode
b=base64.b64decode(sys.stdin.buffer.read())
try:print(json.dumps({'roundtrip_b64':base64.b64encode(bencode(bdecode(b))).decode()}))
except Exception as e:print(json.dumps({'exception':type(e).__name__,'message':str(e)}))
"""
cases=[]
for i,b in enumerate(samples):
 value=base64.b64encode(b).decode();r=subprocess.run([sys.executable,'-c',script,os.environ.get('TRANSFER_ORACLE_RUNTIME',str(root/'oracle-runtime'))],input=value.encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=5)
 result={'name':str(i),'raw_b64':value}
 if r.returncode==0:result.update(json.loads(r.stdout))
 else:result.update({'source_process_exit':r.returncode,'source_native_crash':True,'stderr':r.stderr.decode(errors='replace')[:300]})
 if i in [22,23]:result['source_native_undefined']=True;result['undefined_reason']='_bencode.pyx ignores CM_Atoi failure/uninitialized or int64 overflow; deterministic safe rejection, not C undefined behavior emulation'
 cases.append(result)
report={'dependency':'fast-bencode1.1.8','oracle_only':True,'isolation':'each dependency input separate child; core disabled,CPU2s; no product Python','initial_unisolated_generator_exit':139,'cases':cases}
text=json.dumps(report,ensure_ascii=False,indent=2)+'\n';(root/'bencode-oracle.json').write_text(text);print(json.dumps({'cases':len(cases),'native_crashes':[x['name']for x in cases if x.get('source_native_crash')]}))
