package main

import (
 "context"
 "encoding/json"
 "errors"
 "os"
 "light-media/internal/plugins"
)

func main(){
 m:=plugins.Manifest{SchemaVersion:1,PluginID:"OriginalAPIRegistrationFixture",Name:"Original API registration fixture",Version:"1.0",Runtime:"go-executable",Entry:"original-api-registration-fixture",HostAPIMin:1,HostAPIMax:1,Methods:[]string{"invoke","get_state","get_service","get_command","get_api","get_pid","set_mode","stop"},Capabilities:[]string{},APIs:[]plugins.HTTPAPI{{Key:"declared",Path:"/declared",Methods:[]string{"POST"},Auth:"bear"}}}
 if len(os.Args)==2 && os.Args[1]=="--manifest" {_=json.NewEncoder(os.Stdout).Encode(m);return}
 mode:=""
 handle:=func(_ context.Context,method string,raw json.RawMessage,_ plugins.HostCaller)(json.RawMessage,error){
  switch method{
  case "invoke":
   var inv plugins.Invocation
   if err:=json.Unmarshal(raw,&inv);err!=nil{return nil,err}
   if inv.Action=="init" {return json.Marshal(nil)}
   if inv.Action!="api" {return nil,errors.New("unsupported action")}
   var req plugins.HTTPRequest
   if err:=json.Unmarshal(inv.Input,&req);err!=nil{return nil,err}
   body,_:=json.Marshal(map[string]any{"pid":os.Getpid(),"path":req.Path,"method":req.Method})
   return json.Marshal(plugins.HTTPResponse{Status:200,Body:body,Headers:map[string]string{"Content-Type":"application/json"}})
  case "get_pid":return json.Marshal(os.Getpid())
  case "get_state":return json.Marshal(false)
  case "get_service","get_command":return json.Marshal([]any{})
  case "stop":return json.Marshal(nil)
  case "set_mode":return json.RawMessage("null"),json.Unmarshal(raw,&mode)
  case "get_api":
   if mode=="error" {return nil,errors.New("actual getter error")}
   if mode=="null" {return json.Marshal(nil)}
   path:="/actual";if mode=="changed"{path="/actual_again"}
   return json.Marshal([]plugins.HTTPAPI{{Key:"actual",Path:path,Methods:[]string{"GET","PATCH"}}})
  default:return nil,errors.New("unsupported method")
  }
 }
 if err:=plugins.Serve(context.Background(),os.Stdin,os.Stdout,m,handle);err!=nil{os.Exit(1)}
}
