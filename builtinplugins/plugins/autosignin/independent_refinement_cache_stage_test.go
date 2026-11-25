package autosignin
import("context";"encoding/base64";"encoding/json";"math";"os";"testing";"bytes")
func TestIndependentRefinementCacheWriteStageControls(t *testing.T){
 raw,e:=os.ReadFile("testdata/source-oracles/independent-autosignin-refinement-review-20261106/source-cache-stage-oracle.json");if e!=nil{t.Fatal(e)}
 var f struct{Rows []struct{Name string;Signed bool;Hash string;Result []any;Events []string;File *string `json:"file_b64"`;Error any}};if e=json.Unmarshal(raw,&f);e!=nil{t.Fatal(e)}
 for _,c:=range f.Rows{t.Run(c.Name,func(t *testing.T){
  cache:=Object{};keys:=[]string{};if c.Name!="empty-cache"{keys=[]string{"old"};cache["old"]="keep"};if c.Name=="empty-hash"{cache["old"]=math.NaN()};if c.Name=="not-signed"{cache["old"]=string([]byte{0xed,0xa0,0x80})}
  storage:=&independentCacheCapture{};calls:=0;text:="not a success";if c.Signed{text="这是您的首次签到，本次签到获得1个魔力值。"}
  out,e:=tjuptSubmit(context.Background(),Object{"name":"Owned"},SiteVisitHooks{HTTP:independentCacheHTTP(func(context.Context,PageRequest)(*PageResponse,error){calls++;return &PageResponse{Status:200,Text:text},nil}),Tjupt:&TjuptDependencies{Cache:storage}},"owned-answer",cache,keys,c.Hash)
  wantWrites:=0;for _,v:=range c.Events{if v=="WRITE"{wantWrites++}}
  if e!=nil||out.State!=c.Result[0]||out.Message!=c.Result[1]||calls!=1||storage.writes!=wantWrites{t.Fatalf("source stage mismatch: %+v %v POST%d WRITE%d want%v",out,e,calls,storage.writes,c.Events)}
  if c.File!=nil{want,e:=base64.StdEncoding.DecodeString(*c.File);if e!=nil||!bytes.Equal(want,storage.bytes){t.Fatal("written bytes differ")}}
 })}
}
