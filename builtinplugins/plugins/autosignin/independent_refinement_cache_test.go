package autosignin
import("bytes";"context";"encoding/base64";"encoding/json";"os";"testing")
type independentCacheCapture struct{bytes []byte;writes int}
func(c *independentCacheCapture)Ensure(context.Context)error{return nil}
func(c *independentCacheCapture)Read(context.Context)([]byte,error){return nil,nil}
func(c *independentCacheCapture)Write(_ context.Context,b []byte)error{c.bytes=append([]byte(nil),b...);c.writes++;return nil}
type independentCacheHTTP func(context.Context,PageRequest)(*PageResponse,error)
func(f independentCacheHTTP)Do(c context.Context,p PageRequest)(*PageResponse,error){return f(c,p)}
func TestIndependentRefinementTjuptCacheSourceWrite(t *testing.T){
 raw,e:=os.ReadFile("testdata/source-oracles/independent-autosignin-refinement-review-20261106/source-cache-writer-oracle.json");if e!=nil{t.Fatal(e)}
 var f struct{Rows []struct{Input string;Bytes string `json:"bytes_b64"`;Error any}};if e=json.Unmarshal(raw,&f);e!=nil{t.Fatal(e)}
 for _,c:=range f.Rows{t.Run(c.Input,func(t *testing.T){
  if c.Error!=nil{t.Fatal("unexpected source error",c.Error)};v,e:=parseSourceHTTPJSON(c.Input);if e!=nil{t.Fatal(e)};m,ok:=v.(sourceHTTPMap);if !ok{t.Fatal("not source map")}
  cap:=&independentCacheCapture{};calls:=0
  out,e:=tjuptSubmit(context.Background(),Object{"name":"Owned","cookie":"synthetic"},SiteVisitHooks{HTTP:independentCacheHTTP(func(context.Context,PageRequest)(*PageResponse,error){calls++;return &PageResponse{Status:200,Text:"这是您的首次签到，本次签到获得1个魔力值。"},nil}),Tjupt:&TjuptDependencies{Cache:cap}},"owned-answer",m.values,append([]string(nil),m.keys...),"new-hash")
  want,err:=base64.StdEncoding.DecodeString(c.Bytes);if err!=nil{t.Fatal(err)}
  if e!=nil||!Truth(out.State)||calls!=1||cap.writes!=1||!bytes.Equal(cap.bytes,want){t.Errorf("source write succeeded after signin; target result=%+v error=%v postcalls=%d cachewrites=%d\ngot=%s\nwant=%s",out,e,calls,cap.writes,cap.bytes,want)}
 })}
}
