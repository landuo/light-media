#!/usr/bin/env python3
"""Read-only fixed-source positions and unique matrix accounting; not acceptance."""
import collections, hashlib, json, pathlib, re, subprocess

root=pathlib.Path(__file__).resolve().parents[2]
matrix=root/'docs/plugin-module-parity-matrix.md'
sources={'B':('/Users/accidia/projects/MoviePilot','8a83efeacd3e26b82be495e17d50146e31c96a60'),'F':('/Users/accidia/projects/MoviePilot-Frontend','2829e3b08214348f05ff2f5beae644882eccad58')}
rows=[];positions=[];contents={};errors=[]
for line in matrix.read_text().splitlines():
    if not re.match(r'^\| (?:S\d\d|\d\d) ·',line):continue
    columns=line.split('|')
    identity=columns[1].split('·')[0].strip()
    state=columns[-2].strip()
    if state not in ['待实现','已实现待验证','验收通过','阻塞','按用户要求排除']:errors.append({'id':identity,'invalid_state':state})
    rows.append({'id':identity,'state':state,'source':columns[2].strip(),'target':columns[3].strip(),'evidence':columns[5].strip()})
    for tree,path,location in re.findall(r'\b([BF])/([^`<>;；\s:]+):(\d+(?:[-–,]\d+)*)',columns[2]):
        key=(tree,path)
        if key not in contents:
            repo,commit=sources[tree]
            result=subprocess.run(['git','-C',repo,'show',commit+':'+path],capture_output=True)
            if result.returncode:errors.append({'id':identity,'missing_source':path});continue
            contents[key]=result.stdout
        numbers=[int(n) for n in re.findall(r'\d+',location)]
        valid=all(0<n<=len(contents[key].splitlines()) for n in numbers)
        positions.append({'id':identity,'tree':tree,'path':path,'location':location,'sha256':hashlib.sha256(contents[key]).hexdigest(),'valid':valid})
        if not valid:errors.append({'id':identity,'invalid_location':path+':'+location})
ids=[r['id'] for r in rows]
expected={f'{n:02d}' for n in range(1,51)}|{f'S{n:02d}' for n in range(1,18)}
if set(ids)!=expected or len(ids)!=len(expected):errors.append({'invalid_unique_matrix_ids':ids})
excluded={r['id'] for r in rows if r['state']=='按用户要求排除'}
if excluded!={'22','44','45','46'}:errors.append({'unexpected_excluded_ids':sorted(excluded)})
out={'kind':'structural/source-position audit only; runtime and semantic evidence reviewed separately','matrix_sha256':hashlib.sha256(matrix.read_bytes()).hexdigest(),'source_commits':{k:v[1] for k,v in sources.items()},'counts':dict(collections.Counter(r['state'] for r in rows)),'scope_total':len(rows)-len(excluded),'rows':rows,'positions':positions,'errors':errors}
path=pathlib.Path(__file__).with_name('matrix-source-audit.json');path.write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'scope_total':out['scope_total'],'states':out['counts'],'positions':len(positions),'errors':errors},ensure_ascii=False))
raise SystemExit(bool(errors))
