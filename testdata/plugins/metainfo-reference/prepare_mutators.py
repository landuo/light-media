from pathlib import Path
import argparse
args=argparse.ArgumentParser();args.add_argument('--reference-dir',type=Path,required=True);args=args.parse_args();reference=args.reference_dir
prefix=(reference/'installed_full_metainfo.py').read_text().split('def evaluate(')[0]
body=r'''
def initialized(fields):
 obj=base['MetaBase']('Synthetic',None,False)
 for key,value in fields.items():setattr(obj,key,media_type(value) if key=='type' else value)
 return obj
cases=[]
values=[None,False,True,0,1,2,-1,'','0','01','１２','١','²','-1','1_0',' 1 ',[],[1],[1,3],[3,1],['1','3'],['²'],['1','²'],[False],[None],[1,'x',3],[1,0],['١','３']]
for field in ['season','episode']:
 for i,value in enumerate(values):
  for j,fields in enumerate([{}, {'type':'电视剧','begin_'+field:2,'end_'+field:4,'total_'+field:3}]):
   cases.append(dict(name='set_'+field+'/'+str(i)+'/'+str(j),initial=fields,actions=[dict(method='set_'+field,args=[value])]))
for i,name in enumerate([None,'','中文','English','中文 Mixed',' ','１２','中文\t']):
 cases.append(dict(name='name/'+str(i),initial=dict(cn_name='旧名',en_name='Old Name'),actions=[dict(method='name',args=[name])]))
for i,pair in enumerate([(None,None),(0,0),(1,3),(3,1),(None,4),(0,3),(-1,4),(2,0)]):
 cases.append(dict(name='set_episodes/'+str(i),initial=dict(begin_episode=2,end_episode=5,total_episode=4),actions=[dict(method='set_episodes',args=list(pair))]))
queries=[None,False,True,0,1,2,4,-1,'1','+2',' 2 ','１２','1_0','x',[],[None],[True],[1],[1,2],[1,4],['1'],[2,2]]
for field in ['season','episode']:
 for i,state in enumerate([{}, {'begin_'+field:1}, {'begin_'+field:1,'end_'+field:3}, {'begin_'+field:3,'end_'+field:1}, {'end_'+field:3}]):
  cases.append(dict(name='is_in_'+field+'/'+str(i),initial=state,actions=[dict(method='is_in_'+field,args=[q]) for q in queries]))
merge_fields=['original_name','year','resource_type','resource_pix','resource_team','customization','resource_effect','video_encode','video_bit','audio_encode','fps','part','tmdbid','doubanid','media_source','media_id','episode_group','web_source','bangumiid','anilistid']
for i,empty in enumerate([None,'',0]):
 target={key:empty for key in merge_fields};target.update(type='未知',cn_name='',en_name='')
 source={key:24 if key in ['fps','tmdbid','bangumiid','anilistid'] else 'Source' for key in merge_fields};source.update(type='电视剧',cn_name='源名',en_name='Source',begin_season=2,end_season=3,total_season=2,begin_episode=4,end_episode=6,total_episode=3)
 cases.append(dict(name='merge/'+str(i),initial=target,actions=[dict(method='merge',args=[source])]))
for i,fields in enumerate([dict(type='电视剧',begin_season=0,begin_episode=0),dict(type='电影',cn_name='',en_name='Target'),dict(type='电视剧',cn_name='中文')]):
 cases.append(dict(name='merge/range/'+str(i),initial=fields,actions=[dict(method='merge',args=[dict(type='电视剧',cn_name='源',en_name='Source',begin_season=2,end_season=3,total_season=2,begin_episode=4,end_episode=6,total_episode=3)])]))
outputs=[]
for sample in cases:
 obj=initialized(sample['initial']);steps=[]
 for action in sample['actions']:
  error='';value=None
  try:
   if action['method']=='name':obj.name=action['args'][0]
   elif action['method']=='merge':obj.merge(initialized(action['args'][0]))
   else:value=getattr(obj,action['method'])(*action['args'])
  except BaseException as err:error=type(err).__name__
  steps.append(dict(action=action,value=value,error=error,state=project_full(obj)))
 outputs.append(dict(**sample,steps=steps))
bits=[]
for bit in ['8','10','12','16','9','010']:
 for sep in ['',' ','.','_','-','\u202f','\x1c']:
  for word in ['bit','BIT','bits','bıt','bİt','bitſ','bitrate']:
   for prefix,suffix in [('',''),('x',''),('','x'),('中','文')]:
    text=prefix+bit+sep+word+suffix
    bits.append(dict(text=text,result=base['MetaBase'].extract_video_bit(text)))
print(json.dumps(dict(source='actual installed complete MetaBase mutators and properties; real class, synthetic states only',source_sha256=sha,cases=outputs,video_bits=bits),ensure_ascii=False,indent=2,default=lambda value:value.value if isinstance(value,Enum) else (_ for _ in ()).throw(TypeError(type(value).__name__))))
'''
(reference/'installed_mutators.py').write_text(prefix+body)
