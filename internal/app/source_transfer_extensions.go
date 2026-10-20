package app

import (
	"context"
	"sync"

	"light-media/internal/fileextensions"
	"light-media/internal/transferextensions"
)

type nativeTransferExtensionRuntime struct {
	mu          sync.Mutex
	constructor *fileextensions.Snapshot
}

// The native transfer instance is constructed at first actual transfer use.
// Configuration changes do not rebuild that instance. A cold App is a new one.
// This is not evidence of the original process's unknown constructor values.
func (a *App) transferFileExtensions(ctx context.Context) (fileextensions.Snapshot, error) {
	if e := ctx.Err(); e != nil {
		return fileextensions.Snapshot{}, e
	}
	r := &a.transferExtensions
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.constructor == nil {
		s, e := a.sourceFileExtensions(ctx)
		if e != nil {
			return fileextensions.Snapshot{}, e
		}
		r.constructor = &s
	}
	return fileextensions.Resolve(r.constructor.Inputs)
}
func (a *App) transferMediaClassifier(ctx context.Context) (transferextensions.Classifier, error) {
	s, e := a.transferFileExtensions(ctx)
	if e != nil {
		return transferextensions.Classifier{}, e
	}
	return transferextensions.New(s)
}

// transfer_media's nested predicates and latest candidates read settings at the
// call, independently from TransferChain's constructor-cached candidate lists.
type nativeTransferExtensionObservation struct {
	Version     int
	InputSHA    string
	Constructor fileextensions.Snapshot
	Handler     fileextensions.Snapshot
}
type nativeTransferExtensionContext struct{}

func (a *App) observeTransferExtensions(ctx context.Context) (context.Context, *nativeTransferExtensionObservation, error) {
	constructor, e := a.transferFileExtensions(ctx)
	if e != nil {
		return ctx, nil, e
	}
	handler, e := a.sourceFileExtensions(ctx)
	if e != nil {
		return ctx, nil, e
	}
	p := &nativeTransferExtensionObservation{Version: 1, Constructor: constructor, Handler: handler}
	p.InputSHA = tokenHash(string(mustCleanSeedJSON(struct {
		Version              int
		Constructor, Handler fileextensions.Snapshot
	}{1, constructor, handler})))
	return context.WithValue(ctx, nativeTransferExtensionContext{}, p), p, nil
}
func transferExtensionObservation(ctx context.Context) *nativeTransferExtensionObservation {
	p, _ := ctx.Value(nativeTransferExtensionContext{}).(*nativeTransferExtensionObservation)
	return p
}
func validTransferExtensionObservation(p *nativeTransferExtensionObservation) bool {
	if p == nil || p.Version != 1 || !p.Constructor.Valid() || !p.Handler.Valid() {
		return false
	}
	return p.InputSHA == tokenHash(string(mustCleanSeedJSON(struct {
		Version              int
		Constructor, Handler fileextensions.Snapshot
	}{1, p.Constructor, p.Handler})))
}
func (a *App) guardTransferExtensionObservation(ctx context.Context, p *nativeTransferExtensionObservation) error {
	if !validTransferExtensionObservation(p) {
		return bad("REVIEW_TRANSFER_EXTENSIONS", "整理扩展名配置来源缺失或不可认证")
	}
	_, now, e := a.observeTransferExtensions(ctx)
	if e != nil {
		return e
	}
	if now.InputSHA != p.InputSHA {
		return bad("REVIEW_TRANSFER_EXTENSIONS", "传输实例或当前覆盖扩展名配置已变化，请重新预览")
	}
	return nil
}

func (a *App) importExtensionObservation(ctx context.Context, policy importOverwritePolicy) (*nativeTransferExtensionObservation, error) {
	if policy.Selection != nil && policy.Selection.Plan != "" {
		var auth nativeDirectoryAuthorization
		if e := a.readQualitySealed(ctx, a.Store.DB, "native_directory_selection", policy.Selection.Plan, &auth); e != nil {
			return nil, e
		}
		if auth.Version == 2 {
			if e := a.guardTransferExtensionObservation(ctx, auth.Extensions); e != nil {
				return nil, e
			}
			return auth.Extensions, nil
		}
	}
	// Legacy direct-host operations have no native plan constructor receipt.
	_, p, e := a.observeTransferExtensions(ctx)
	return p, e
}

func sidecarMaterialKind(sidecar importSidecar) string {
	if sidecar.Kind == "audio" {
		return "audio"
	}
	return "subtitle"
}
func auxiliaryMaterialKind(kind string) bool { return kind == "subtitle" || kind == "audio" }

func (a *App) guardDirectoryOverwriteExtensionPolicy(ctx context.Context, op directoryOverwriteOperation) error {
	if e := a.guardDirectoryOverwritePolicy(ctx, op.Policy); e != nil {
		return e
	}
	if op.ExtensionInputSHA != "" {
		snapshot, e := a.sourceFileExtensions(ctx)
		if e != nil {
			return e
		}
		if snapshot.InputSHA != op.ExtensionInputSHA {
			return bad("REVIEW_TRANSFER_EXTENSIONS", "覆盖检查的实际扩展名配置已经变化")
		}
	}
	return nil
}
