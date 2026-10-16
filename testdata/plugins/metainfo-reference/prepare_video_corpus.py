from pathlib import Path
import argparse
args=argparse.ArgumentParser();args.add_argument('--reference-dir',type=Path,required=True);args=args.parse_args();reference=args.reference_dir
import itertools,json
source=(reference/'installed_full_metainfo.py').read_text();prefix=source.split('CASES=')[0]
cases=[]
def add(group,title,subtitle=None,words=None):
 for mode in ['rust','legacy']:cases.append(dict(name=group+'/'+str(len(cases))+'/'+mode,title=title,subtitle=subtitle,mode=mode,global_words=words or [],local_words=None))
names=['Synthetic Film','中文标题','中文 Title','中文 12','123Synthetic','Synthetic2','2024','12','１２','中文\u200b','A.B.C','The 1st Part','Season','Part','DC','IMAX','PTS','2019-2020','日本剧场版','中文字幕','Dian Ying','dianying']
tails=['','2024','S02E03','S04-S02','E01-E04','1080p','3840X2160','2160p WEB-DL','2024 BluRay REMUX HDR10+ H.265 10bit AAC 5.1','S02E03 AMZN WEB-DL H265 DDP 5.1','2024 PART II','2024 CD2','2024 DISC 2','2024 UHD Blu-Ray DIY','2024 WEB-DL Dolby Vision HDR','2024 23.976FPS','2024 60FPS','2024 10bit DTS-HD MA 7.1','2024 12Bit AV1 FLAC','01-12Fin']
for n,t in itertools.product(names,tails):add('video-name',n+(' '+t if t else ''))
for token in ['WEB','BluRay','BLU','UHD','HDTV','HDR10+','HDRVivid','DOVI','3D','REPACK','HLG','HQ','REMUX','AMZN','NF','H','265','DDP','DTS','ATMOS','10bit','1080p','4K','60FPS','S01','E02','2024','2','12','Ⅱ','PART','II']:
 for position in [0,1,2,3]:
  pieces=['Synthetic','Film','2024'];pieces.insert(position,token);add('video-order',' '.join(pieces))
for sep in [' ','.','_','-','/','|',';', '\u202f','\x1c','\n']:
 add('video-token',sep.join(['Synthetic','Film','2024','2160p','WEB','DL','H','265','AAC','5.1']))
for number in ['0','00','01','001','999','1000','１２','١٢','𐵁','𞗱','²','00000000000000000000001']:
 for fmt in ['Season {n}','Synthetic Film S{n}','Synthetic Film E{n}','Synthetic Film Episode {n}','Synthetic Film {n}FPS','Synthetic Film 2024 {n}p']:
  add('video-number',fmt.format(n=number))
for tag in ['[tmdbid=123]','{tmdb-123}','[doubanid=123]','{[tmdbid=123;type=tv;s=0-2;e=0-4;g=abc]}','{[doubanid=123;type=movie;s=4-2;e=4-2]}','[bangumi=123]','{anilist-123}','[tmdbid=１２]','{[s=１２;e=١٢;type=tv]}','{[tmdbid=123;bangumiid=456;anilistid=789]}']:
 for title in ['Synthetic Film 2024','12.mkv','中文.1080p','[Group] Synthetic Show - 01 [1080p]']:
  add('explicit',title+' '+tag)
for name in ['Dian Ying','dianying','Nu Er','nuer','Ce Shi','ceshi','null','Ke','hao']:
 for desc in ['电影 简介','电影/简介','电影|简介','电影\x1c简介','电影\u202f简介','1电影 简介',' 中文 简介']:
  add('pinyin-title',name+' 2024 1080p',desc)
for bracket in ['[Synthetic.Film.2024.1080p.WEB-DL]','[Synthetic Film 2024 1080p]','【Synthetic.Film.2024.BluRay】','[Group]','[]']:
 add('bracket',bracket+'-TEAM.mkv')
(reference/'installed_video_corpus.py').write_text(prefix+'\nCASES='+repr(cases)+'\n'+source[source.index('outputs=[]\nfor sample in CASES:'):])
print(len(cases))
