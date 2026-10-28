package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// The author-owned ESM component exercises the actual host-provided Vue and
// instance-bound API. It never imports application internals or emits a fake
// saved state. Config emits the fixed frontend's save event; other views call
// the real process worker's bearer API and render its returned version/marker.
const ownedProcessFrontendChunk = `export const component=(v,name)=>({
  name:'OwnedProcess'+name.slice(2),props:['initialConfig','api','navKey','pluginId'],emits:['save'],
  setup(p,{emit}){
    const marker=v.ref(p.initialConfig?.marker||''), output=v.ref(''), error=v.ref('');
    const read=async(fail=false)=>{try{error.value='';const result=await p.api.get('plugin/OwnedFrontendFixture/status',{params:fail?{fail:true}:{}});output.value=result.version+' / '+(result.marker||'');}catch(e){error.value=e.message;}};
    if(name!=='./Config')v.onMounted(()=>read());
    return()=>v.h('section',{'data-owned-frontend':name},[
      v.h('h3','Owned Vue '+name.slice(2)),
      name==='./Config'?v.h('div',[
        v.h('label',{for:'owned-marker'},'Owned Vue marker'),
        v.h('input',{id:'owned-marker','aria-label':'Owned Vue marker',value:marker.value,onInput:e=>marker.value=e.target.value}),
        v.h('button',{type:'button',onClick:()=>emit('save',{enabled:true,marker:marker.value})},'Save owned Vue config')
      ]):v.h('div',[
        v.h('p',{'data-owned-result':true},output.value),
        v.h('button',{type:'button',onClick:()=>read()},'Read owned API'),
        v.h('button',{type:'button',onClick:()=>read(true)},'Fail owned API'),
        error.value?v.h('p',{role:'alert'},error.value):null,
        p.navKey?v.h('p','Navigation: '+p.navKey):null
      ])
    ]);
  }
});`

func ownedProcessFrontendArchive(t *testing.T, binary string) (artifacts.Index, []byte) {
	return ownedProcessFrontendBuild(t, binary, "")
}

func ownedProcessFrontendBuild(t *testing.T, binary, build string) (artifacts.Index, []byte) {
	t.Helper()
	worker, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.Frontend == nil {
		t.Fatal("wrong actual browser frontend artifact")
	}
	chunk := ownedProcessFrontendChunk
	if build != "" {
		chunk = strings.ReplaceAll(chunk, "'Owned Vue '+", "'Owned Vue "+build+" '+")
	}
	chunk += "\nexport const literal=()=>window.MoviePilotAPI.get('plugin/" + manifest.PluginID + "/status');export const runtime=()=>{const id='OwnedFront'+'endFixture';return window.MoviePilotAPI.get('plugin/'+id+'/status')};"
	files := map[string][]byte{
		"frontend/assets/" + strings.ToLower(manifest.PluginID) + "-theme.css": []byte("." + strings.ToLower(manifest.PluginID) + " { --" + strings.ToLower(manifest.PluginID) + "-class:17px }"),
		manifest.Entry:                   worker,
		"frontend/assets/remoteEntry.js": []byte(`let scope;export const init=s=>{scope=s};export const get=async name=>{if(!['./Config','./Page','./Dashboard','./AppPage'].includes(name))throw Error('unknown expose');const vue=(await scope.vue[Object.keys(scope.vue)[0]].get())();const m=await import('./chunk.js');return()=>m.component(vue,name)};`),
		"frontend/assets/chunk.js":       []byte(chunk),
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	inventory := []artifacts.PackageFile{}
	for _, name := range names {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(files[name]); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, artifacts.PackageFile{Path: name, SHA256: originalFrontendHash(files[name]), Size: int64(len(files[name]))})
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	payload := data.Bytes()
	artifact := artifacts.Artifact{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: originalFrontendHash(payload), ExecutableSHA256: originalFrontendHash(worker), Size: int64(len(payload)), HostContract: pluginPureSDKContract, Files: inventory}
	return artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{artifact}}}}, payload
}
