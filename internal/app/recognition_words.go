package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/media/words"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
)

const remoteIdentifiersMarker = "#========以下识别词由 RemoteIdentifiers 插件添加========#"

func (a *App) loadRecognitionWords(ctx context.Context) error {
	_, rules, err := a.recognitionWords(ctx)
	if err != nil {
		return err
	}
	matcher, err := words.New(rules)
	if err != nil {
		return bad("INVALID_RECOGNITION_WORDS", "识别词超过允许范围")
	}
	a.recognitionMatcher.Store(matcher)
	return nil
}

func (a *App) recognitionWords(ctx context.Context) (int64, []string, error) {
	var revision int64
	var sealed []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT revision,sealed FROM recognition_words WHERE id='CustomIdentifiers'`).Scan(&revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, []string{}, nil
	}
	if err != nil {
		return 0, nil, err
	}
	raw, err := a.pluginStore.unseal("core_words", "system", "CustomIdentifiers", sealed)
	if err != nil {
		return 0, nil, err
	}
	var rules []string
	if json.Unmarshal(raw, &rules) != nil {
		return 0, nil, bad("INVALID_RECOGNITION_WORDS", "识别词存储无法读取")
	}
	return revision, rules, nil
}

// Called under recognitionMu. The CAS and cache publication are serialized
// with both user edits/migration and remote updates, never replacing a prefix
// captured before the network operation.
func (a *App) saveRecognitionWordsLocked(ctx context.Context, revision int64, rules []string) error {
	return a.commitRecognitionWords(ctx, revision, rules, nil, 0)
}

func (a *App) commitRecognitionWords(ctx context.Context, revision int64, rules []string, run *PluginRun, remoteCount int) error {
	if revision < 0 {
		return bad("RECOGNITION_REVISION_CONFLICT", "识别词版本无效")
	}
	matcher, err := words.New(rules)
	if err != nil {
		return bad("INVALID_RECOGNITION_WORDS", "识别词超过允许范围")
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal("core_words", "system", "CustomIdentifiers", raw)
	if err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if run != nil {
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE r.id=? AND r.instance_id=? AND r.state='running' AND i.plugin_id='RemoteIdentifiers' AND i.installed=1 AND i.desired_enabled=1 AND r.generation=? AND i.generation=r.generation AND r.config_revision=? AND v.revision=r.config_revision`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&current); err != nil {
			return err
		}
		if current != 1 {
			return bad("PLUGIN_SNAPSHOT_STALE", "识别词更新任务已失效")
		}
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
	if run != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recognition_word_updates VALUES(?,?,?,?,?,?,?)`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, revision+1, len(rules), remoteCount); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.recognitionMatcher.Store(matcher)
	return nil
}

func (a *App) saveRecognitionWords(ctx context.Context, revision int64, rules []string) error {
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	return a.saveRecognitionWordsLocked(ctx, revision, rules)
}

func (a *App) replaceRemoteIdentifiers(ctx context.Context, remote []string) (int, error) {
	return a.replaceRemoteIdentifiersBound(ctx, nil, remote)
}
func (a *App) replaceRemoteIdentifiersForRun(ctx context.Context, run PluginRun, remote []string) (int, error) {
	return a.replaceRemoteIdentifiersBound(ctx, &run, remote)
}
func (a *App) replaceRemoteIdentifiersBound(ctx context.Context, run *PluginRun, remote []string) (int, error) {
	if len(remote) == 0 || remote[0] != remoteIdentifiersMarker {
		return 0, bad("INVALID_RECOGNITION_WORDS", "远程识别词标记无效")
	}
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	revision, current, err := a.recognitionWords(ctx)
	if err != nil {
		return 0, err
	}
	prefix := 0
	for prefix < len(current) && !strings.Contains(current[prefix], "RemoteIdentifiers") {
		prefix++
	}
	rules := append(append([]string{}, current[:prefix]...), remote...)
	return len(rules), a.commitRecognitionWords(ctx, revision, rules, run, len(remote)-1)
}

func (a *App) prepareRecognitionTitle(title string) string {
	result := a.recognitionMatcher.Load().Prepare(title)
	if result.Limited {
		// Incomplete preprocessing must not silently download or file a
		// different episode using the unadjusted name. Physical paths stay intact.
		return ""
	}
	return result.Title
}
func (a *App) parseMediaName(name string) media.ParseResult {
	return media.Parse(a.prepareRecognitionTitle(name))
}
func (a *App) parseManualImportFile(file ImportFileRequest) media.ParseResult {
	return parseManualImportName(file, a.prepareRecognitionTitle(filepath.Base(file.Path)))
}
func (a *App) evaluateResource(resource sites.Resource, target decision.Target, policy decision.Policy) decision.Decision {
	resource.Title = a.prepareRecognitionTitle(resource.Title)
	return decision.Evaluate(resource, target, policy)
}
func (a *App) resourceNumberingSuggestion(title string) (int, []int) {
	return resourceNumberingSuggestion(a.prepareRecognitionTitle(title))
}

func (a *App) eligibleAutomaticSeasonPack(resource sites.Resource, target decision.Target, policy Policy) bool {
	resource.Title = a.prepareRecognitionTitle(resource.Title)
	return eligibleAutomaticSeasonPack(resource, target, policy)
}
func (a *App) validateAutomaticSeasonPackFiles(material metainfo.Material, target decision.Target, siteID string) error {
	return validateAutomaticSeasonPackFilesWithWords(material, target, siteID, a.prepareRecognitionTitle)
}
