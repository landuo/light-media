import json,pathlib,sqlite3,sys
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A');data=root/'tmp/TestPluginUI3133ProductionCLI1959242803/001';assert data.is_relative_to(root) and (data/'instance.lock').exists()
con=sqlite3.connect(data/'light-media.db',timeout=10)
assert con.execute("select count(*) from records where kind='plugin_dashboard_settings' and id='enabled'").fetchone()[0]==1
for name in ['owned_ui3133_fail_insert','owned_ui3133_fail_update']:con.execute('drop trigger if exists '+name)
if sys.argv[1]=='enable':
 for verb in ['INSERT','UPDATE']:
  name='owned_ui3133_fail_'+verb.lower();con.execute(f"CREATE TRIGGER {name} BEFORE {verb} ON records WHEN NEW.kind='plugin_dashboard_settings' AND NEW.id='enabled' BEGIN SELECT RAISE(ABORT,'owned dashboard settings write failure'); END")
else:assert sys.argv[1]=='disable'
con.commit();print(json.dumps({'kind':'actual owned SQLite trigger injection; no production fixture response override','mode':sys.argv[1],'db':str(data/'light-media.db'),'triggers':con.execute("select name from sqlite_master where type='trigger' and name like 'owned_ui3133_fail_%'").fetchall()}));con.close()
