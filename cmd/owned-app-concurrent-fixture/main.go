// A standalone owned artifact using the real standard Base SDK. Its barriers
// hold no state mutex and neither replace the App host nor perform external IO.
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "sync"
    "time"

    "light-media/internal/plugins"
)

type gate struct{ready chan struct{};once sync.Once;arrived int}
type object struct{
    mu sync.Mutex
    initCount int
    enabled bool
    failClose bool
    barriers map[string]*gate
    holds map[string]*gate
    closed chan struct{}
    closeOnce sync.Once
    ordinary plugins.HostCaller
    cleanup plugins.HostCaller
    order []string
}
func manifest()plugins.Manifest{
    apis:=[]plugins.HTTPAPI{}
    for _,key:=range []string{"barrier","hold","release","state","long"}{
        apis=append(apis,plugins.HTTPAPI{Key:key,Path:"/"+key,Methods:[]string{"GET"},Auth:"apikey",Query:[]plugins.HTTPParameter{{Name:"token",Kind:"string"},{Name:"group",Kind:"string"}}})
    }
    return plugins.Manifest{SchemaVersion:1,PluginID:"OwnedAppConcurrent",Name:"Owned concurrent standard artifact",Version:"1.0",Runtime:"go-executable",Entry:"owned-app-concurrent-fixture",HostAPIMin:1,HostAPIMax:1,Methods:[]string{"init","get_state","get_service","get_command","get_api","get_render_mode","get_form","get_page","get_dashboard","get_sidebar_nav","snapshot","http","event","close","stop_service","stop"},Capabilities:plugins.BaseMethods(),Events:[]string{"owned.concurrent"},APIs:apis}
}
func(o *object)snapshot(ctx context.Context,base plugins.Base,token string)(json.RawMessage,error){
    path,err:=base.GetDataPath(ctx);if err!=nil{return nil,err}
    cfg,err:=base.GetConfig(ctx);if err!=nil{return nil,err}
    key:="shared";value,err:=base.GetData(ctx,&key);if err!=nil{return nil,err}
    o.mu.Lock();count:=o.initCount;order:=append([]string(nil),o.order...);o.mu.Unlock()
    return json.Marshal(map[string]any{"pid":os.Getpid(),"init_count":count,"path":path,"config":cfg,"value":value,"token":token,"order":order})
}
func(o *object)cleanupCall(ctx context.Context,method string,host plugins.HostCaller)(json.RawMessage,error){
    o.mu.Lock()
    old:=o.cleanup;if method=="close"{old=o.ordinary}
    o.cleanup=host;o.order=append(o.order,method);order:=append([]string(nil),o.order...);fail:=o.failClose
    o.mu.Unlock()
    denied:=true
    if old!=nil {denied=(plugins.Base{Host:old}).SaveData(ctx,"unauthorized_old",true)!=nil}
    if !denied{return nil,errors.New("retained callback was authorized")}
    base:=plugins.Base{Host:host}
    if err:=base.SaveData(ctx,"cleanup_"+method,map[string]any{"pid":os.Getpid(),"order":order,"old_denied":denied});err!=nil{return nil,err}
    if method=="close"{o.closeOnce.Do(func(){close(o.closed)});if fail{return nil,errors.New("owned close failure")}}
    return json.RawMessage(`null`),nil
}
func(o *object)handle(ctx context.Context,method string,raw json.RawMessage,host plugins.HostCaller)(json.RawMessage,error){
    base:=plugins.Base{Host:host}
    switch method{
    case "close","stop_service","stop":return o.cleanupCall(ctx,method,host)
    case "init":
        var fields map[string]json.RawMessage
        if err:=json.Unmarshal(raw,&fields);err!=nil{return nil,err}
        enabled:=true;fail:=false
        if v,ok:=fields["enabled"];ok{if err:=json.Unmarshal(v,&enabled);err!=nil{return nil,err}}
        if v,ok:=fields["fail_close"];ok{if err:=json.Unmarshal(v,&fail);err!=nil{return nil,err}}
        o.mu.Lock();o.initCount++;o.enabled=enabled;o.failClose=fail;o.mu.Unlock()
        if v,ok:=fields["key"];ok{if err:=base.SaveData(ctx,"shared",v);err!=nil{return nil,err}}
        return json.RawMessage(`null`),nil
    case "get_state":o.mu.Lock();enabled:=o.enabled;o.mu.Unlock();return json.Marshal(enabled)
    case "get_service","get_command","get_sidebar_nav":return json.RawMessage(`[]`),nil
    case "get_api":return json.Marshal(manifest().APIs)
    case "get_render_mode":return json.RawMessage(`["vuetify",""]`),nil
    case "get_form":return json.RawMessage(`[[],{}]`),nil
    case "get_page":return json.RawMessage(`[]`),nil
    case "get_dashboard":return json.RawMessage(`[{},{},[]]`),nil
    case "snapshot":return o.snapshot(ctx,base,"")
    case "event":
        var event struct{Type string `json:"type"`;Data struct{Token string `json:"token"`} `json:"data"`}
        if err:=json.Unmarshal(raw,&event);err!=nil || event.Type!="owned.concurrent"{return nil,errors.New("invalid owned event")}
        o.mu.Lock();o.ordinary=host;o.mu.Unlock()
        if err:=base.SaveData(ctx,"entered_"+event.Data.Token,true);err!=nil{return nil,err}
        // Deliberately needs source close to release it, not just ctx.Cancel.
        <-o.closed
        return json.RawMessage(`null`),nil
    case "http":
        var request plugins.HTTPRequest
        if err:=json.Unmarshal(raw,&request);err!=nil{return nil,err}
        token:=request.LastQuery("token");group:=request.LastQuery("group")
        if request.Method!="GET" || request.Path!="/"+request.Key{return nil,errors.New("invalid owned HTTP")}
        switch request.Key{
        case "barrier":
            o.mu.Lock();g:=o.barriers[group];if g==nil{g=&gate{ready:make(chan struct{})};o.barriers[group]=g};g.arrived++;if g.arrived==2{g.once.Do(func(){close(g.ready)})};o.ordinary=host;o.mu.Unlock()
            if err:=base.SaveData(ctx,"entered_"+token,true);err!=nil{return nil,err}
            select{case <-g.ready:case <-ctx.Done():return nil,ctx.Err()}
            if err:=base.SaveData(ctx,"result_"+token,map[string]any{"pid":os.Getpid(),"token":token});err!=nil{return nil,err}
        case "hold":
            o.mu.Lock();g:=&gate{ready:make(chan struct{})};o.holds[token]=g;o.ordinary=host;o.mu.Unlock()
            if err:=base.SaveData(ctx,"entered_"+token,true);err!=nil{return nil,err}
            select{case <-g.ready:case <-ctx.Done():return nil,ctx.Err()}
        case "release":
            o.mu.Lock();g:=o.holds[token];o.mu.Unlock()
            if g==nil{return nil,errors.New("hold missing")};g.once.Do(func(){close(g.ready)})
        case "long":
            if err:=base.SaveData(ctx,"entered_"+token,true);err!=nil{return nil,err}
            timer:=time.NewTimer(31*time.Second);defer timer.Stop()
            select{case <-timer.C:case <-ctx.Done():return nil,ctx.Err()}
        case "state":
        default:return nil,errors.New("unknown owned HTTP")
        }
        body,err:=o.snapshot(ctx,base,token);if err!=nil{return nil,err}
        return json.Marshal(plugins.HTTPResponse{Status:200,Body:body,Headers:map[string]string{"Content-Type":"application/json"}})
    }
    return nil,errors.New("unsupported owned method")
}
func main(){
    m:=manifest();if err:=m.Validate();err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(2)}
    if len(os.Args)==2 && os.Args[1]=="--manifest"{if json.NewEncoder(os.Stdout).Encode(m)!=nil{os.Exit(2)};return}
    o:=&object{enabled:true,barriers:map[string]*gate{},holds:map[string]*gate{},closed:make(chan struct{})}
    if err:=plugins.ServeConcurrent(context.Background(),os.Stdin,os.Stdout,m,o.handle);err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}
}
