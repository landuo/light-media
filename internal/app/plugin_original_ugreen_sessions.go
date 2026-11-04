package app

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "sync"

 "light-media/internal/plugins/mediaserverruntime"
 "light-media/internal/plugins/mediaservervendors"
 "modernc.org/sqlite"
)

// New private session namespace, never projected from FNOS/old media settings,
// never reachable through SystemConfig SDK, public HTTP or config.updated.
const originalUgreenSessionKind="source_ugreen_sessions_v1"
const originalUgreenSessionKey="UgreenSessionCache"
type originalUgreenSessionStore struct{
 mu sync.Mutex
 plugin *PluginStore
 hot json.RawMessage
}
var _ mediaservervendors.UgreenSessionStore=(*originalUgreenSessionStore)(nil)

// One instance must be owned by the App and shared by all Ugreen vendors. This
// leaf does not initialize an App/registry or grant any plugin authority.
func newOriginalUgreenSessionStore(ctx context.Context,plugin *PluginStore)(*originalUgreenSessionStore,error){
 if ctx==nil||plugin==nil||plugin.store==nil||plugin.store.DB==nil{return nil,errors.New("Ugreen sessions require the owned application store")}
 if err:=ctx.Err();err!=nil{return nil,err}
 s:=&originalUgreenSessionStore{plugin:plugin,hot:json.RawMessage(`null`)}
 raw,_,err:=s.read(ctx,plugin.store.DB);if err!=nil{return nil,err};s.hot=raw
 return s,nil
}
type originalUgreenSessionReader interface{QueryRowContext(context.Context,string,...any)*sql.Row}
func originalUgreenSessionSQLError(ctx context.Context,err error)error{
 if ctx!=nil&&ctx.Err()!=nil{return ctx.Err()}
 if err==nil{return nil}
 // Only an actual directly returned owned SQLite driver error at a marked SQL
// call becomes an ordinary source DB exception. Do not unwrap mixed errors.
 if e,ok:=err.(*sqlite.Error);ok&&e!=nil{return &mediaserverruntime.SourceError{Class:"UgreenSessionDatabaseError",Cause:e}}
 return err
}
func(s *originalUgreenSessionStore)read(ctx context.Context,db originalUgreenSessionReader)(json.RawMessage,bool,error){
 var sealed []byte
 err:=db.QueryRowContext(ctx,`SELECT payload FROM records WHERE kind=? AND id=?`,originalUgreenSessionKind,originalUgreenSessionKey).Scan(&sealed)
 if errors.Is(err,sql.ErrNoRows){return json.RawMessage(`null`),false,nil}
 if err!=nil{return nil,false,originalUgreenSessionSQLError(ctx,err)}
 // This private non-SDK cache has no source total size cap. Zero is explicit
// unsealBound's no-application-limit mode, not a global HTTP/SDK limit change.
 raw,err:=s.plugin.unsealBound(originalUgreenSessionKind,"system",originalUgreenSessionKey,sealed,0)
 if err!=nil{return nil,false,err} // Integrity errors are never a missing value.
 value,_,err:=sharedSystemConfigJSON(raw);if err!=nil{return nil,false,err}
 return value,true,nil
}
func(s *originalUgreenSessionStore)Get(ctx context.Context)(json.RawMessage,error){
 if ctx==nil{return nil,errors.New("Ugreen private cache requires context")}
 if err:=ctx.Err();err!=nil{return nil,err}
 s.mu.Lock();defer s.mu.Unlock()
 if err:=ctx.Err();err!=nil{return nil,err}
 return append(json.RawMessage(nil),s.hot...),nil
}
func(s *originalUgreenSessionStore)Set(ctx context.Context,raw json.RawMessage)error{
 if ctx==nil{return errors.New("Ugreen private cache requires context")}
 if err:=ctx.Err();err!=nil{return err}
 value,_,err:=sharedSystemConfigJSON(raw);if err!=nil{return err}
 // Local representation/encryption preflight cannot publish a malformed or
// unsealable credential value. This does not query or mutate the source row.
 sealed,err:=s.plugin.seal(originalUgreenSessionKind,"system",originalUgreenSessionKey,value)
 if err!=nil{return err}
 s.mu.Lock();defer s.mu.Unlock()
 if err:=ctx.Err();err!=nil{return err}
 old:=s.hot
 s.hot=append(json.RawMessage(nil),value...) // Original SYNC deepcopy precedes SQL.
 tx,err:=s.plugin.store.DB.BeginTx(ctx,nil)
 if err!=nil{return originalUgreenSessionSQLError(ctx,err)}
 defer tx.Rollback()
 _,present,err:=s.read(ctx,tx);if err!=nil{return err}
 if present&&sharedSystemConfigEqual(old,value){
  // Compare old HOT value, not DB value. After a failed prior write an equal
// retry still skips SQL as source set does; don't silently repair that behavior.
  return originalUgreenSessionSQLError(ctx,tx.Rollback())
 }
 if present{
  if originalBaseJSONTruthy(value){
   _,err=tx.ExecContext(ctx,`UPDATE records SET payload=?,updated_at=? WHERE kind=? AND id=?`,sealed,now(),originalUgreenSessionKind,originalUgreenSessionKey)
  }else{
   _,err=tx.ExecContext(ctx,`DELETE FROM records WHERE kind=? AND id=?`,originalUgreenSessionKind,originalUgreenSessionKey)
  }
 }else{
  // Source inserts a row even for a falsey new value when no row exists.
  _,err=tx.ExecContext(ctx,`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)`,originalUgreenSessionKind,originalUgreenSessionKey,sealed,now())
 }
 if err!=nil{return originalUgreenSessionSQLError(ctx,err)}
 return originalUgreenSessionSQLError(ctx,tx.Commit())
}
