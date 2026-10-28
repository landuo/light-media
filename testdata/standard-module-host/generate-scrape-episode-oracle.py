import json,subprocess
from pathlib import Path
FIXED='8a83efeacd3e26b82be495e17d50146e31c96a60'
source=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',FIXED+':app/schemas/tmdb.py'],text=True)
ns={};exec(compile(source,'<fixed:app/schemas/tmdb.py>','exec'),ns)
cases=[]
for row in [{'season_number':1,'episode_number':1,'name':'First'},{'id':12345,'season_number':1,'episode_number':2,'name':'Second'}]:cases.append({'input':row,'output':ns['TmdbEpisode'](**row).model_dump()})
Path(__file__).with_name('scrape-episode-source-oracle.json').write_text(json.dumps({'fixed_backend':FIXED,'source':['app/schemas/tmdb.py:19-34','app/chain/transfer.py:1945-1962'],'boundary':'Actual fixed Pydantic TmdbEpisode construction; no provider or light-media code. Provider id is discarded; source transfer consumes this model without requiring an episode id.','cases':cases},indent=2)+'\n')
print(len(cases),'fixed real episode DTO cases')
