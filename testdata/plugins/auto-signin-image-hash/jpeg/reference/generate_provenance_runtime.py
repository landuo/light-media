"""Read-only installed decoder provenance; no configuration or user inputs."""
from PIL import Image,features,_imaging
import pathlib,hashlib,json,os,platform
sha=lambda p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
base=pathlib.Path(Image.__file__).parent
libs=base.parent/'pillow.libs'
flags=pathlib.Path('/proc/cpuinfo').read_text() if pathlib.Path('/proc/cpuinfo').exists() else ''
print(json.dumps({'pillow':Image.__version__,'jpeg_abi':features.version_codec('jpg'),'libjpeg_turbo_present':features.check_feature('libjpeg_turbo'),'libjpeg_turbo':features.version_feature('libjpeg_turbo'),'architecture':platform.machine(),'cpu_sse2':'sse2' in flags.split(),'cpu_avx2':'avx2' in flags.split(),'force_env':{k:os.environ.get(k) for k in ('JSIMD_FORCENONE','JSIMD_FORCESSE2','JSIMD_FORCEAVX2')},'pillow_sources':{p.name:sha(p) for p in (base/'Image.py',base/'JpegImagePlugin.py')},'imaging_extension':{'name':pathlib.Path(_imaging.__file__).name,'sha256':sha(_imaging.__file__)},'bundled_jpeg_libraries':[{'name':p.name,'sha256':sha(p)} for p in sorted(libs.glob('*jpeg*'))]},indent=2))
