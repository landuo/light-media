package app

// Independent review-only overlay. It is never written into the product tree.
// Real worker + resource adapter + parser + owned filesystem; config callback
// is deliberately recorded rather than falsely described as a real database.
import (
    "context"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "testing"

    "light-media/internal/catalog"
    "light-media/internal/plugins"
    "light-media/internal/plugins/categoryeditor"
)

type categoryReviewInput struct {
    ID string `json:"id"`
    Mode string `json:"mode"`
    Content string `json:"content_base64"`
    Media map[string]any `json:"media"`
}

func categoryReviewMedia(raw map[string]any) catalog.Media {
    m := catalog.Media{ID: 1, MediaType: "movie", CategoryMetadataKnown: true, CategoryFields: map[string]catalog.CategoryField{}}
    for k, v := range raw {
        switch k {
        case "original_language": m.OriginalLanguage, _ = v.(string)
        case "origin_country":
            if list, ok := v.([]any); ok { for _, x := range list { s, _ := x.(string); m.OriginCountry = append(m.OriginCountry, s) } }
        case "genre_ids":
            if list, ok := v.([]any); ok { for _, x := range list { n, _ := x.(float64); m.GenreIDs = append(m.GenreIDs, int64(n)) } }
        case "production_countries":
            if list, ok := v.([]any); ok { for _, x := range list { item, _ := x.(map[string]any); s, _ := item["iso_3166_1"].(string); m.ProductionCountries = append(m.ProductionCountries, catalog.Country{ISO3166_1:s}) } }
        case "release_date":
            if s, ok := v.(string); ok && len(s)>=4 { m.Year, _ = strconv.Atoi(s[:4]) }
        }
        if s, ok := v.(string); ok && s!="" { m.CategoryFields[k] = catalog.CategoryField{Present:true, Hashes:[]string{catalog.CategoryValueHash(s)}} }
    }
    return m
}

func TestCategoryEditorIndependentNarrow20261029Probe(t *testing.T) {
    root := os.Getenv("CATEGORYEDITOR_REVIEW_EVIDENCE")
    if root == "" { t.Fatal("CATEGORYEDITOR_REVIEW_EVIDENCE missing") }
    targetPaths:=[]string{"internal/plugins/categoryeditor/categoryeditor.go","internal/plugins/categoryeditor/form.json","internal/plugins/category_policy.go","internal/app/plugin_categoryeditor_resources.go","internal/app/plugin_categoryeditor_wire.go","internal/app/plugin_original_resources.go","internal/app/media_category_runtime.go","internal/app/media_category.go","internal/app/category_conditions.go","internal/app/category_yaml.go","internal/app/core_business_source_facts.go","internal/app/plugin_original_media_categories.go","internal/catalog/category_fields.go","internal/catalog/category_metadata.go","internal/app/defaults/category.yaml","internal/app/system_messages.go","go.mod","go.sum"}
    targetHashes:=func()map[string]string{out:=map[string]string{};for _,name:=range targetPaths{b,e:=os.ReadFile(filepath.Join(root,"../light-media",name));if e!=nil{t.Fatal(e)};out[name]=fmt.Sprintf("%x",sha256.Sum256(b))};return out}
    beforeHashes:=targetHashes()
    var primary struct { Cases []categoryReviewInput `json:"cases"`; Initial string `json:"initial_text"`; Media map[string]any `json:"media"` }
    b,e:=os.ReadFile(filepath.Join(root,"categoryeditor-source-helper-inputs.json")); if e!=nil {t.Fatal(e)}
    if e=json.Unmarshal(b,&primary);e!=nil {t.Fatal(e)}
    var extra struct {Cases []categoryReviewInput `json:"cases"`}
    b,e=os.ReadFile(filepath.Join(root,"categoryeditor-review-source-extra-inputs.json"));if e!=nil {t.Fatal(e)}
    if e=json.Unmarshal(b,&extra);e!=nil {t.Fatal(e)}
    var wire struct {Cases []categoryReviewInput `json:"cases"`};b,e=os.ReadFile(filepath.Join(root,"categoryeditor-independent-wire-edge-inputs-20261029.json"));if e!=nil{t.Fatal(e)};if e=json.Unmarshal(b,&wire);e!=nil{t.Fatal(e)}
    cases:=append(append(primary.Cases,extra.Cases...),wire.Cases...)
    output:=[]map[string]any{}
    selected:=map[string]bool{"blank":true,"parser_failure":true,"scalar_true":true,"scalar_integer":true,"scalar_string":true,"nonempty_sequence":true,"movie_scalar":true,"section_scalar":true,"rule_scalar":true,"unicode_decimal_range":true,"unicode_superscript_range":true,"yaml11_boolean_condition":true,"duplicate_true_and_one":true,"html_escaped_payload_under_64k":true,"truthy_empty_string_list_string_condition":true,"condition_bool":true,"condition_integer":true,"exact_64k_quotes_comment":true,"exact_64k_backslashes_comment":true,"exact_64k_control_comment":true}
    for _, input:= range cases {
        if !selected[input.ID] {continue}
        if input.Mode!="file" || input.ID=="cycle_bounded" || input.ID=="malformed_country_items" {continue} // cycle is bounded separately; malformed raw TMDB values cannot be faithfully represented by catalog.Media.
        raw,e:=base64.StdEncoding.DecodeString(input.Content);if e!=nil {t.Fatal(e)}
        row:=map[string]any{"id":input.ID}
        for _, temperature:=range []string{"warm","cold"} {
            dir,e:=os.MkdirTemp(root,"categoryeditor-review-go-fixtures-");if e!=nil {t.Fatal(e)}
            a:=&App{Dir:dir}
            path:=filepath.Join(dir,"category.yaml")
            if temperature=="warm" {
                if e=os.WriteFile(path,[]byte(primary.Initial),0600);e!=nil {t.Fatal(e)}
                if _,e=a.currentMediaCategoryPolicy();e!=nil {t.Fatal(e)}
            }
            instance:=categoryeditor.New()
            scope:=originalBaseCallScope{validate:func(context.Context)error{return nil}}
            calls:=[]map[string]any{}
            host:=func(ctx context.Context,method string,payload json.RawMessage)(json.RawMessage,error){
                calls=append(calls,map[string]any{"method":method,"payload":json.RawMessage(append([]byte(nil),payload...))})
                if method==plugins.BaseUpdateConfig {return json.RawMessage(`true`),nil}
                return a.categoryEditorResource(ctx,scope,method,payload)
            }
            config,_:=json.Marshal(map[string]any{"enabled":true,"content":string(raw)})
            result,applyErr:=instance.Handle(context.Background(),"init",config,host)
            state,stateErr:=instance.Handle(context.Background(),"get_state",nil,host)
            item:=map[string]any{"apply_result":json.RawMessage(result),"enabled":json.RawMessage(state),"calls":calls,"state_error":fmt.Sprint(stateErr)}
            if applyErr!=nil {item["apply_error"]=applyErr.Error()}
            stored,readErr:=os.ReadFile(path)
            if readErr!=nil {item["file_error"]=readErr.Error()} else {item["file_base64"]=base64.StdEncoding.EncodeToString(stored)}
            policy,queryErr:=a.currentMediaCategoryPolicy()
            if queryErr!=nil {item["query_error"]=queryErr.Error()} else {
                names,namesErr:=policy.categoryNames("movie");if namesErr!=nil{item["movie_categories_error"]=namesErr.Error()}else{item["movie_categories"]=names}
                media:=input.Media;if media==nil {media=primary.Media}
                match,matchErr:=policy.categoryWithError(categoryReviewMedia(media))
                item["movie_match"]=match;if matchErr!=nil {item["movie_match_error"]=matchErr.Error()}
            }
            // Expose the actual retained target cache independently of the public
            // read/reload path; a failed query must not look like empty cache.
            retained,retainedErr:=a.mediaCategories.policy.categoryWithError(categoryReviewMedia(primary.Media))
            item["retained_cache_match"]=retained;if retainedErr!=nil {item["retained_cache_match_error"]=retainedErr.Error()}
            row[temperature]=item
        }
        output=append(output,row)
    }
    // Runtime filesystem contrasts use only owned files; no symlink target is
    // outside this review fixture and no original host data are accessed.
    runtimeCases:=[]map[string]any{}
    for _, mode:=range []string{"missing_file","write_only","external_change_without_init"} {
        dir,e:=os.MkdirTemp(root,"categoryeditor-review-go-resource-");if e!=nil {t.Fatal(e)}
        a:=&App{Dir:dir};path:=filepath.Join(dir,"category.yaml")
        if mode!="missing_file" {if e=os.WriteFile(path,[]byte(primary.Initial),0600);e!=nil {t.Fatal(e)}}
        _,initialErr:=a.currentMediaCategoryPolicy()
        row:=map[string]any{"id":mode,"initial_error":fmt.Sprint(initialErr)}
        if mode=="missing_file" {_,e=os.Stat(path);row["file_created_by_read"]=e==nil;runtimeCases=append(runtimeCases,row);continue}
        if mode=="external_change_without_init" {if e=os.WriteFile(path,[]byte("movie:\n  New:\n"),0600);e!=nil{t.Fatal(e)};policy,e:=a.currentMediaCategoryPolicy();row["query_error"]=fmt.Sprint(e);match,matchErr:=policy.categoryWithError(categoryReviewMedia(primary.Media));row["match_without_explicit_reload"]=match;row["match_error"]=fmt.Sprint(matchErr);runtimeCases=append(runtimeCases,row);continue}
        if mode=="write_only" {if e=os.Chmod(path,0200);e!=nil {t.Fatal(e)}}
        if mode=="symlink" {owned:=filepath.Join(dir,"owned-policy.yaml");if e=os.Rename(path,owned);e!=nil {t.Fatal(e)};if e=os.Symlink(owned,path);e!=nil {t.Fatal(e)}}
        p:=categoryeditor.New();scope:=originalBaseCallScope{validate:func(context.Context)error{return nil}}
        calls:=[]string{}
        host:=func(ctx context.Context,method string,payload json.RawMessage)(json.RawMessage,error){calls=append(calls,method);if method==plugins.BaseUpdateConfig{return json.RawMessage(`true`),nil};return a.categoryEditorResource(ctx,scope,method,payload)}
        config,_:=json.Marshal(map[string]any{"enabled":true,"content":"movie:\n  New:\n"})
        _,e=p.Handle(context.Background(),"init",config,host);row["apply_error"]=fmt.Sprint(e)
        state,_:=p.Handle(context.Background(),"get_state",nil,host);row["enabled"]=json.RawMessage(state);row["calls"]=calls
        row["notification"]=json.RawMessage(a.systemMessages.get("plugin"))
        policy,queryErr:=a.currentMediaCategoryPolicy();row["query_error"]=fmt.Sprint(queryErr);match,matchErr:=policy.categoryWithError(categoryReviewMedia(primary.Media));row["match_after_apply"]=match;row["match_error"]=fmt.Sprint(matchErr)
        runtimeCases=append(runtimeCases,row)
    }
    // Re-execute the source-observed failed-root -> read-IO -> parser-failure
    // state sequence from the independent original-source input/observations.
    var sourceSequence struct {Steps []struct { ID string `json:"id"`; Content string `json:"input_content"`; Mode string `json:"mode"` } `json:"steps"`}
    b,e=os.ReadFile(filepath.Join(root,"categoryeditor-independent-root-read-sequence-20261029.json"));if e!=nil{t.Fatal(e)}
    if e=json.Unmarshal(b,&sourceSequence);e!=nil{t.Fatal(e)}
    sequenceDir,e:=os.MkdirTemp(root,"categoryeditor-independent-root-read-target-");if e!=nil{t.Fatal(e)}
    sequenceApp:=&App{Dir:sequenceDir};sequencePath:=filepath.Join(sequenceDir,"category.yaml")
    if e=os.WriteFile(sequencePath,[]byte(primary.Initial),0600);e!=nil{t.Fatal(e)}
    if _,e=sequenceApp.currentMediaCategoryPolicy();e!=nil{t.Fatal(e)}
    sequencePlugin:=categoryeditor.New();sequenceScope:=originalBaseCallScope{validate:func(context.Context)error{return nil}}
    sequenceRows:=[]map[string]any{}
    for _,step:=range sourceSequence.Steps {
        mode,e:=strconv.ParseUint(step.Mode[2:],8,32);if e!=nil{t.Fatal(e)}
        if e=os.Chmod(sequencePath,os.FileMode(mode));e!=nil{t.Fatal(e)}
        calls:=[]map[string]any{}
        host:=func(ctx context.Context,method string,payload json.RawMessage)(json.RawMessage,error){calls=append(calls,map[string]any{"method":method,"payload":json.RawMessage(append([]byte(nil),payload...))});if method==plugins.BaseUpdateConfig{return json.RawMessage(`true`),nil};return sequenceApp.categoryEditorResource(ctx,sequenceScope,method,payload)}
        config,_:=json.Marshal(map[string]any{"enabled":true,"content":step.Content})
        _,applyErr:=sequencePlugin.Handle(context.Background(),"init",config,host)
        state,_:=sequencePlugin.Handle(context.Background(),"get_state",nil,host)
        row:=map[string]any{"id":step.ID,"enabled":json.RawMessage(state),"calls":calls}
        if applyErr!=nil{row["apply_error"]=applyErr.Error()}
        policy,queryErr:=sequenceApp.currentMediaCategoryPolicy();if queryErr!=nil{row["query_error"]=queryErr.Error()}
        match,matchErr:=policy.categoryWithError(categoryReviewMedia(primary.Media));row["movie_match"]=match;if matchErr!=nil{row["movie_match_error"]=matchErr.Error()}
        sequenceRows=append(sequenceRows,row)
    }
    if e=os.Chmod(sequencePath,0600);e!=nil{t.Fatal(e)}
    afterHashes:=targetHashes();equalHashes:=true;for name,before:=range beforeHashes{if afterHashes[name]!=before{equalHashes=false}}
    probeBytes,e:=os.ReadFile(filepath.Join(root,"categoryeditor-independent-narrow-20261029_test.go"));if e!=nil{t.Fatal(e)}
    result:=map[string]any{"probe_sha256":fmt.Sprintf("%x",sha256.Sum256(probeBytes)),"scope":"independent target worker/resource/parser controlled probe; not installed-host/browser acceptance","target_hashes_before_and_after_equal":equalHashes,"target_files":beforeHashes,"target_files_after":afterHashes,"cases":output,"runtime_cases":runtimeCases,"root_read_sequence":sequenceRows,"excluded_probe_input":"malformed_country_items: raw TMDB shape cannot be represented faithfully by catalog.Media; no false difference claimed"}
    data,e:=json.MarshalIndent(result,"","  ");if e!=nil {t.Fatal(e)}
    if e=os.WriteFile(filepath.Join(root,"categoryeditor-independent-narrow-"+os.Getenv("CATEGORYEDITOR_REVIEW_PHASE")+"-target.json"),append(data,'\n'),0600);e!=nil {t.Fatal(e)}
    t.Logf("recorded %d target inputs warm/cold plus %d owned filesystem contrasts",len(output),len(runtimeCases))
}
