package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"

	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/media/words"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
)

const remoteIdentifiersMarker = "#========以下识别词由 RemoteIdentifiers 插件添加========#"

func (a *App) loadRecognitionWords(ctx context.Context) error {
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	_, _, err := a.readSharedSystemConfigLocked(ctx, a.Store.DB)
	return err // Legal stored JSON may be invalid rules; report at consumption, not startup.
}
func (a *App) recognitionWords(ctx context.Context) (int64, []string, error) {
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	_, raw, e := a.readSharedSystemConfigLocked(ctx, a.Store.DB)
	if e != nil {
		return 0, nil, e
	}
	// Native CAS metadata comes from current storage, not the source hot value.
	observed, e := readSharedSystemConfigRow(ctx, a.Store.DB)
	if e != nil {
		return 0, nil, e
	}
	rules, e := sharedSystemConfigRules(raw)
	if e == nil {
		_, e = words.New(rules)
	}
	return observed.revision, rules, e
}

// Called under recognitionMu. The CAS and cache publication are serialized
// with both user edits/migration and remote updates, never replacing a prefix
// captured before the network operation.
func (a *App) saveRecognitionWordsLocked(ctx context.Context, revision int64, rules []string) error {
	return a.commitRecognitionWords(ctx, revision, rules)
}

func (a *App) commitRecognitionWords(ctx context.Context, revision int64, rules []string) error {
	if revision < 0 {
		return bad("RECOGNITION_REVISION_CONFLICT", "识别词版本无效")
	}
	_, err := words.New(rules)
	if err != nil {
		return bad("INVALID_RECOGNITION_WORDS", "识别词超过允许范围")
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if len(raw) > pluginDataValueLimit {
		return bad("INVALID_RECOGNITION_WORDS", "识别词超过允许范围")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, rows, err := a.readSharedSystemConfigRows(ctx, tx)
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		return bad("SYSTEM_CONFIG_MULTIPLE_RESULTS", "原生词表不能静默覆盖重复源行")
	}
	if len(rows) == 0 {
		rows = []sharedSystemConfigSourceRow{{ID: 1, Value: raw}}
	} else {
		rows[0].Value = raw
	}
	sealed, err := a.sealSharedSystemConfigRows(rows)
	if err != nil {
		return err
	}

	var result sql.Result
	if revision == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO recognition_words(id,revision,sealed,updated_at) SELECT 'CustomIdentifiers',1,?,? WHERE ?=0 ON CONFLICT(id) DO UPDATE SET revision=recognition_words.revision+1,sealed=excluded.sealed,updated_at=excluded.updated_at WHERE recognition_words.revision=?`, sealed, now(), revision, revision)
	}
	if revision > 0 {
		result, err = tx.ExecContext(ctx, `UPDATE recognition_words SET revision=revision+1,sealed=?,updated_at=? WHERE id='CustomIdentifiers' AND revision=?`, sealed, now(), revision)
	}
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return bad("RECOGNITION_REVISION_CONFLICT", "识别词已变化，本次未覆盖")
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	a.sharedSystemConfig.loaded = false
	_, _, err = a.readSharedSystemConfigLocked(ctx, a.Store.DB)
	return err
}

func (a *App) saveRecognitionWords(ctx context.Context, revision int64, rules []string) error {
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	return a.saveRecognitionWordsLocked(ctx, revision, rules)
}

func (a *App) prepareRecognitionTitle(title string) string {
	return a.prepareRecognitionTitleContext(context.Background(), title)
}
func (a *App) prepareRecognitionTitleContext(ctx context.Context, title string) string {
	prepared, _ := a.prepareRecognitionTitleChecked(ctx, title)
	return prepared // Existing string-only helpers retain their fail-closed contract.
}
func (a *App) prepareRecognitionTitleChecked(ctx context.Context, title string) (string, error) {
	var matcher *words.Matcher
	if scope, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition); ok {
		matcher = scope.matcher
	} else {
		a.recognitionMu.Lock()
		matcher = a.recognitionMatcher.Load()
		failure := a.sharedSystemConfig.consumerError.Load()
		a.recognitionMu.Unlock()
		if failure != nil {
			return "", failure.err
		}
	}
	result, e := matcher.PrepareChecked(title)
	if e != nil {
		return "", bad("SYSTEM_CONFIG_TITLE_UNICODE", "识别结果不能编码为Unicode标题")
	}
	if result.Limited {
		return "", bad("INVALID_RECOGNITION_WORDS", "识别预处理超过现有执行范围")
	}
	return result.Title, nil
}

func (a *App) parseMediaName(name string) media.ParseResult {
	return a.parseMediaNameContext(context.Background(), name)
}
func (a *App) parseMediaNameContext(ctx context.Context, name string) media.ParseResult {
	return media.Parse(a.prepareRecognitionTitleContext(ctx, name))
}
func (a *App) parseManualImportFile(file ImportFileRequest) media.ParseResult {
	return a.parseManualImportFileContext(context.Background(), file)
}
func (a *App) parseManualImportFileContext(ctx context.Context, file ImportFileRequest) media.ParseResult {
	if file.originalMetadataPath != "" {
		file.Path = file.originalMetadataPath
	}
	return parseManualImportName(file, a.prepareRecognitionTitleContext(ctx, filepath.Base(file.Path)))
}
func (a *App) evaluateResource(resource sites.Resource, target decision.Target, policy decision.Policy) decision.Decision {
	return a.evaluateResourceContext(context.Background(), resource, target, policy)
}
func (a *App) evaluateResourceContext(ctx context.Context, resource sites.Resource, target decision.Target, policy decision.Policy) decision.Decision {
	original := resource
	policy.FilterResource = &original
	resource.Title = a.prepareRecognitionTitleContext(ctx, resource.Title)
	return decision.EvaluateContext(ctx, resource, target, policy)
}
func (a *App) resourceNumberingSuggestion(title string) (int, []int) {
	return a.resourceNumberingSuggestionContext(context.Background(), title)
}
func (a *App) resourceNumberingSuggestionContext(ctx context.Context, title string) (int, []int) {
	return resourceNumberingSuggestion(a.prepareRecognitionTitleContext(ctx, title))
}

func (a *App) eligibleAutomaticSeasonPack(resource sites.Resource, target decision.Target, policy Policy) bool {
	return a.eligibleAutomaticSeasonPackContext(context.Background(), resource, target, policy)
}
func (a *App) eligibleAutomaticSeasonPackContext(ctx context.Context, resource sites.Resource, target decision.Target, policy Policy) bool {
	original := resource
	filters := decisionPolicy(policy)
	filters.FilterResource = &original
	resource.Title = a.prepareRecognitionTitleContext(ctx, resource.Title)
	return eligibleAutomaticSeasonPackPolicy(resource, target, filters)
}
func (a *App) validateAutomaticSeasonPackFiles(material metainfo.Material, target decision.Target, siteID string) error {
	return a.validateAutomaticSeasonPackFilesContext(context.Background(), material, target, siteID)
}
func (a *App) validateAutomaticSeasonPackFilesContext(ctx context.Context, material metainfo.Material, target decision.Target, siteID string) error {
	return validateAutomaticSeasonPackFilesWithWords(material, target, siteID, func(name string) string {
		return a.prepareRecognitionTitleContext(ctx, name)
	})
}
