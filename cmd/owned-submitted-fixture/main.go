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

type gate struct { entered, release chan struct{}; once sync.Once; parent plugins.HostCaller }
type object struct { mu sync.Mutex; gates map[string]*gate; service string; failGetter, recursive bool; order []string }
func manifest() plugins.Manifest {
	apis:=[]plugins.HTTPAPI{}
	for _,key:=range []string{"submit","release","refresh","state"} { apis=append(apis,plugins.HTTPAPI{Key:key,Path:"/"+key,Methods:[]string{"GET"},Auth:"apikey",Query:[]plugins.HTTPParameter{{Name:"token",Kind:"string"},{Name:"group",Kind:"string"}}}) }
	caps:=append(plugins.BaseMethods(),plugins.BackgroundSubmitCapability,plugins.ServicesRefreshCapability)
	return plugins.Manifest{SchemaVersion:1,PluginID:"OwnedSubmitted",Name:"Owned submitted calls",Version:"1.0",Runtime:"go-executable",Entry:"owned-submitted-fixture",HostAPIMin:1,HostAPIMax:1,Methods:[]string{"init","get_state","get_service","get_command","get_api","get_render_mode","get_form","get_page","get_dashboard","get_sidebar_nav","background","configure","http","close","stop_service","stop"},Capabilities:caps,APIs:apis}
}
func(o *object)submit(ctx context.Context,host plugins.HostCaller,token string,poll bool)error{
	g:=&gate{entered:make(chan struct{}),release:make(chan struct{}),parent:host}
	o.mu.Lock();o.gates[token]=g;o.mu.Unlock()
	if err:=(plugins.Manager{Host:host}).Submit(ctx,"background",map[string]any{"token":token,"poll":poll,"removed_task":map[string]any{"id":token,"value":"before-removal"}});err!=nil{return err}
	// An actual child must be able to enter BEFORE this parent returns. The
	// parent never waits for completion and holds no worker mutex here.
	select{case <-g.entered:return nil;case <-ctx.Done():return ctx.Err()}
}
func(o *object)handle(ctx context.Context,method string,raw json.RawMessage,host plugins.HostCaller)(json.RawMessage,error){
	base:=plugins.Base{Host:host};manager:=plugins.Manager{Host:host}
	switch method {
	case "init":
		var input struct{Submit bool `json:"submit_init"`;Fail bool `json:"fail_init"`}
		if err:=json.Unmarshal(raw,&input);err!=nil{return nil,err}
		if input.Submit {if err:=o.submit(ctx,host,"init",true);err!=nil{return nil,err}}
		if input.Fail{return nil,errors.New("owned init failure after independent submission")}
		return json.RawMessage(`null`),nil
	case "configure":
		_,err:=base.UpdateConfig(ctx,raw);return json.RawMessage(`null`),err
	case "get_state":return json.RawMessage(`true`),nil
	case "get_service":
		o.mu.Lock();service,fail,recursive:=o.service,o.failGetter,o.recursive;o.mu.Unlock()
		if recursive {if err:=manager.RefreshServices(ctx);err==nil{return nil,errors.New("recursive refresh was accepted")};if err:=base.SaveData(ctx,"recursive_denied",true);err!=nil{return nil,err}}
		if fail{return nil,errors.New("owned declaration failure")}
		if service==""{return json.RawMessage(`[]`),nil}
		return json.Marshal([]map[string]any{{"id":service,"name":service,"handler":"background","trigger":map[string]any{"kind":"interval","timezone":"UTC","interval_ms":3600000},"input":map[string]any{"token":"scheduled"}}})
	case "get_command","get_page","get_sidebar_nav":return json.RawMessage(`[]`),nil
	case "get_api":return json.Marshal(manifest().APIs)
	case "get_render_mode":return json.RawMessage(`["vuetify",""]`),nil
	case "get_form":return json.RawMessage(`[[],{}]`),nil
	case "get_dashboard":return json.RawMessage(`[{},{},[]]`),nil
	case "background":
		var input struct{Token string `json:"token"`;Poll bool `json:"poll"`;Removed json.RawMessage `json:"removed_task"`}
		if err:=json.Unmarshal(raw,&input);err!=nil{return nil,err}
		o.mu.Lock();g:=o.gates[input.Token];o.mu.Unlock();if g==nil{return nil,errors.New("owned gate missing")}
		if err:=base.SaveData(ctx,"entered_"+input.Token,map[string]any{"pid":os.Getpid()});err!=nil{return nil,err};close(g.entered)
		if input.Poll {
			key:="release_"+input.Token
			for {v,err:=base.GetData(ctx,&key);if err!=nil{return nil,err};if string(v)=="true"{break};select{case <-time.After(5*time.Millisecond):case <-ctx.Done():return nil,ctx.Err()}}
		} else {select{case <-g.release:case <-ctx.Done():return nil,ctx.Err()}}
		oldDenied:=(plugins.Base{Host:g.parent}).SaveData(ctx,"unauthorized_parent",true)!=nil
		if !oldDenied{return nil,errors.New("completed parent caller retained authority")}
		if err:=base.SaveData(ctx,"completed_"+input.Token,map[string]any{"pid":os.Getpid(),"old_denied":oldDenied,"removed_task":input.Removed});err!=nil{return nil,err}
		return json.RawMessage(`null`),nil
	case "close","stop_service","stop":
		o.mu.Lock();o.order=append(o.order,method);order:=append([]string(nil),o.order...);o.mu.Unlock()
		if err:=base.SaveData(ctx,"cleanup_"+method,map[string]any{"pid":os.Getpid(),"order":order});err!=nil{return nil,err}
		return json.RawMessage(`null`),nil
	case "http":
		var request plugins.HTTPRequest;if err:=json.Unmarshal(raw,&request);err!=nil{return nil,err}
		token,mode:=request.LastQuery("token"),request.LastQuery("group")
		result:=map[string]any{"pid":os.Getpid(),"accepted":true}
		switch request.Key {
		case "submit":
			if mode=="getter" {if err:=manager.Submit(ctx,"get_state",map[string]any{});err!=nil{result["accepted"]=false};break}
			if mode=="lifecycle" {if err:=manager.Submit(ctx,"close",map[string]any{});err!=nil{result["accepted"]=false};break}
			if err:=o.submit(ctx,host,token,false);err!=nil{result["accepted"]=false;break}
			if mode=="error"{return nil,errors.New("owned parent error after submit")}
			if mode=="cancel"{<-ctx.Done();return nil,ctx.Err()}
		case "release":
			o.mu.Lock();g:=o.gates[token];o.mu.Unlock();if g==nil{return nil,errors.New("owned gate missing")};g.once.Do(func(){close(g.release)})
		case "refresh":
			o.mu.Lock();o.service=token;o.failGetter=mode=="fail";o.recursive=mode=="recursive";o.mu.Unlock()
			if err:=manager.RefreshServices(ctx);err!=nil{result["accepted"]=false}
		case "state":
		default:return nil,errors.New("owned endpoint")
		}
		body,err:=json.Marshal(result);if err!=nil{return nil,err};return json.Marshal(plugins.HTTPResponse{Status:200,Body:body,Headers:map[string]string{"Content-Type":"application/json"}})
	}
	return nil,errors.New("owned method unsupported")
}
func main(){
	m:=manifest();if err:=m.Validate();err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(2)}
	if len(os.Args)==2&&os.Args[1]=="--manifest"{_ = json.NewEncoder(os.Stdout).Encode(m);return}
	o:=&object{gates:map[string]*gate{}}
	if err:=plugins.ServeConcurrent(context.Background(),os.Stdin,os.Stdout,m,o.handle);err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}
}
