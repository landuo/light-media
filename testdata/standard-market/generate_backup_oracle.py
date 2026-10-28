"""Pinned Docker backup methods on owned temporary native files only."""
import ast, hashlib, json, shutil, subprocess, sys, tempfile
from pathlib import Path
from types import SimpleNamespace as NS

commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
source={name:subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':'+name]) for name in ['app/chain/system.py','app/helper/plugin.py']}
names={'backup_plugins','restore_plugins','refresh_persistent_plugin_backup'}
nodes=[node for raw in source.values() for node in ast.walk(ast.parse(raw)) if isinstance(node,ast.FunctionDef) and node.name in names]
assert len(nodes)==3
for node in nodes: node.decorator_list=[]
with tempfile.TemporaryDirectory(prefix='owned-source-backup-') as temporary:
    root=Path(temporary);runtime=root/'app/plugins';config=root/'config';runtime.mkdir(parents=True);config.mkdir()
    ns=dict(Path=Path,shutil=shutil,settings=NS(ROOT_PATH=root,CONFIG_PATH=config),PLUGIN_DIR=runtime,SystemUtils=NS(is_docker=lambda:True),SystemHelper=lambda:NS(is_system_reset=lambda:ns['reset']),PluginManager=NS(install_plugin_missing_dependencies=lambda:None),logger=NS(**{key:lambda *args,**kwargs:None for key in ['info','debug','error','warning','warn']}),reset=False)
    exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'fixed/backup','exec'),ns)
    live=runtime/'owned';live.mkdir();(live/'code').write_text('one')
    backup=config/'plugins_backup/owned'
    cases=[]
    def capture(kind):cases.append(dict(kind=kind,live=(live/'code').read_text(),backup_exists=backup.exists(),backup=(backup/'code').read_text() if backup.exists() else None))
    ns['backup_plugins']();capture('initial_backup')
    (live/'code').write_text('two');ns['backup_plugins']();capture('shutdown_skips_existing_backup')
    assert ns['refresh_persistent_plugin_backup']('Owned'.lower());capture('install_refresh_replaces_backup')
    (live/'code').write_text('three');ns['reset']=True;ns['restore_plugins']();capture('reset_restores_then_consumes_backup')
    ns['backup_plugins']();(live/'code').write_text('four');ns['reset']=False;ns['restore_plugins']();capture('ordinary_start_consumes_without_restore')
assert [c['backup'] for c in cases]==['one','one','two',None,None]
assert [c['live'] for c in cases]==['one','two','two','two','four']
Path(__file__).with_name('backup-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_sha256={name:hashlib.sha256(raw).hexdigest() for name,raw in source.items()},kind='actual fixed methods with owned temporary filesystem and Docker/reset/dependency boundaries; not actual Docker runtime acceptance',cases=cases),indent=2)+'\n')
