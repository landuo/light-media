package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

// MissingCapability is a concrete current-host limitation, never a successful
// site result. It crosses the source's ordinary network exception handlers.
type MissingCapability struct{ Operation, Detail string }

func (e *MissingCapability) Error() string {
	return "AutoSignIn requires " + e.Operation + ": " + e.Detail
}

type sdkBackend struct {
	ctx        context.Context
	host       sdk.HostCaller
	now        func() time.Time
	logSecrets *sourceCredentialLogSecrets
}

func (b sdkBackend) fatal(err error) {
	if err != nil {
		panic(&boundaryFailure{err})
	}
}
func (b sdkBackend) configured() []Object {
	result, err := (sdk.Sites{Host: b.host}).Read(b.ctx, sdk.SiteHostRequestV1{OrderByPriority: true, SourceProjection: true})
	b.fatal(err)
	out := make([]Object, 0, len(result.Items))
	for _, row := range result.Items {
		// SourceID is the imported Site.id. Native host IDs are an explicit
		// platform identity mapping; no integer is fabricated from an ID hash.
		var id any = row.ID
		if row.SourceID != 0 {
			id = json.Number(fmt.Sprint(row.SourceID))
		}
		value := Object{"id": id, "name": row.Name, "url": row.URL, "domain": row.Domain, "is_active": row.Enabled, "public": row.Public, "proxy": row.Proxy, "render": row.Render, "_host_site_id": row.ID}
		if len(row.SourceFields) > 0 {
			restored, err := sourceRestoreSavedJSON(row.SourceFields)
			b.fatal(err)
			fields, ok := restored.(Object)
			if !ok {
				b.fatal(errors.New("source native metadata must be an object"))
			}
			for _, key := range []string{"id", "name", "domain", "url", "pri", "is_active", "public", "proxy", "render"} {
				v, ok := fields[key]
				if !ok {
					b.fatal(errors.New("source native metadata field missing"))
				}
				value[key] = v
			}
			value["_host_source_projection"] = true
		}
		out = append(out, value)
	}
	return out
}
func (b sdkBackend) customInventory() (any, []Object) {
	raw, err := (sdk.Sites{Host: b.host}).SourceCustomConfig(b.ctx)
	b.fatal(err)
	if b.logSecrets != nil {
		b.logSecrets.registerCustom(raw.Config)
	}
	var value any
	value, err = sourceRestoreSavedJSON(raw.Config)
	b.fatal(err)
	if !Truth(value) {
		return false, nil
	}
	object, ok := value.(Object)
	if !ok {
		panic(&sourceException{fmt.Errorf("'%s' object has no attribute 'get'", typeName(value))})
	}
	if !Truth(object["enabled"]) {
		return object["enabled"], nil
	}
	rows, ok := object["sites"].([]any)
	if !ok {
		panic(&sourceException{fmt.Errorf("'%s' object is not iterable", typeName(object["sites"]))})
	}
	out := make([]Object, 0, len(rows))
	for ordinal, row := range rows {
		object, ok := row.(Object)
		if !ok {
			panic(&sourceException{fmt.Errorf("'%s' object has no attribute 'get'", typeName(row))})
		}
		object["_host_source_reference"] = sdk.SiteSourceReferenceV1{CustomOrdinal: &ordinal, Snapshot: raw.Snapshot}
		out = append(out, object)
	}
	return true, out
}
func (b sdkBackend) custom() []Object { _, rows := b.customInventory(); return rows }
func (b sdkBackend) inventory() Inventory {
	rows := b.configured()
	enabled, custom := b.customInventory()
	return Inventory{Configured: rows, Custom: custom, CustomEnabled: enabled}
}

func (b sdkBackend) project(indexer sdk.SiteSourceIndexerV1, configured []Object, credentials bool) Object {
	var fields Object
	b.fatal(Decode(indexer.Fields, &fields))
	if fields == nil {
		b.fatal(errors.New("source indexer fields must be an object"))
	}
	if indexer.SiteID == nil {
		fields["_host_site_id"] = nil
		return fields
	}
	id := *indexer.SiteID
	fields["_host_site_id"] = id
	for _, row := range configured {
		if row["_host_site_id"] == id {
			for _, key := range []string{"public", "proxy", "render"} {
				if _, exists := fields[key]; !exists && !(key == "public" && Truth(row["_host_source_projection"])) {
					fields[key] = row[key]
				}
			}
			break
		}
	}
	if credentials {
		private, err := (sdk.Sites{Host: b.host}).SourceCredentials(b.ctx, sdk.SiteSourceReferenceV1{SiteID: id})
		b.fatal(err)
		if b.logSecrets != nil {
			b.logSecrets.registerFields(private.Fields)
		}
		value, err := sourceRestoreSavedJSON(private.Fields)
		b.fatal(err)
		privateFields, ok := value.(Object)
		if !ok {
			b.fatal(errors.New("source credential fields must be an object"))
		}
		for key, value := range privateFields {
			fields[key] = value
		}
		fields["_host_source_reference"] = private.Reference
	}

	return fields
}
func (b sdkBackend) indexers(credentials bool) []Object {
	out, err := (sdk.Sites{Host: b.host}).SourceIndexers(b.ctx)
	b.fatal(err)
	configured := b.configured()
	rows := make([]Object, 0, len(out.Items))
	for _, row := range out.Items {
		rows = append(rows, b.project(row, configured, credentials))
	}
	return rows
}
func (b sdkBackend) indexer(domain string, credentials bool) Object {
	out, err := (sdk.Sites{Host: b.host}).SourceIndexer(b.ctx, domain)
	b.fatal(err)
	if out.Indexer == nil {
		return nil
	}
	return b.project(*out.Indexer, b.configured(), credentials)
}
func (b sdkBackend) history(key string) (any, error) {
	raw, err := (sdk.Base{Host: b.host}).GetData(b.ctx, &key)
	if err != nil {
		return nil, err
	}
	return sourceRestoreSavedJSON(raw)
}
func (b sdkBackend) historyKeys() []string {
	raw, err := (sdk.Base{Host: b.host}).GetData(b.ctx, nil)
	b.fatal(err)
	var rows []sdk.BaseDataRow
	b.fatal(json.Unmarshal(raw, &rows))
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.Key)
	}
	return keys
}
func (b sdkBackend) log(level, message string) error {
	if sourceHasPrivateString(message) {
		// The actual source UTF8 file handler drops this unencodable record,
		// diagnoses it internally and continues. Keep the same business error
		// boundary. The host UTF8 log transport gets only a finite diagnostic;
		// its current scope/cancellation/guard check is still required.
		return (sdk.Logger{Host: b.host}).Log(b.ctx, sdk.LogWarning, "source_utf8_log_dropped: unpaired surrogate in original log record")
	}

	if b.logSecrets != nil {
		message = b.logSecrets.redact(message)
	}

	// The existing finite logger bounds each UTF8 record. A long original
	// message is split without dropping text, with each fragment attributed
	// to the current active instance/callback by the host.
	for len(message) > sdk.LogMessageLimit {
		end := sdk.LogMessageLimit
		for end > 0 && !utf8.RuneStart(message[end]) {
			end--
		}
		if err := (sdk.Logger{Host: b.host}).Log(b.ctx, sdk.LogLevel(level), message[:end]); err != nil {
			return err
		}
		message = message[end:]
	}
	return (sdk.Logger{Host: b.host}).Log(b.ctx, sdk.LogLevel(level), message)
}
func (b sdkBackend) notify(ctx context.Context, fields Object) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copy := Object{}
	for key, value := range fields {
		if sourceHasPrivateString(value) {
			// Preserve source string code points through the existing finite
			// Notification call. The host owns validation/history failure.
			raw, err := sourceHTTPJSONBytes(value)
			if err != nil {
				return err
			}
			copy[key] = json.RawMessage(raw)
		} else {
			copy[key] = value
		}
	}
	if copy["mtype"] == "NotificationType.SiteMessage" {
		copy["mtype"] = "站点"
	}
	_, err := (sdk.Base{Host: b.host}).PostMessage(ctx, copy)
	return err
}
func (b sdkBackend) emit(ctx context.Context, _ string, fields Object) error {
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = sdk.SendEvent(ctx, b.host, sdk.EventSendRequest{Kind: sdk.BroadcastEventKind, Type: "plugin.action", Data: raw})
	return err
}

type sdkSiteTransport struct {
	backend sdkBackend
	site    Object
}

func (s sdkSiteTransport) identity() string {
	id, ok := s.site["_host_site_id"].(string)
	if !ok || id == "" {
		s.backend.fatal(&MissingCapability{"source-bound site identity", "custom/template site has no current canonical managed site handle"})
	}
	return id
}
func (s sdkSiteTransport) path(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		panic(&sourceException{err})
	}
	base, err := url.Parse(textValue(s.site["url"]))
	if err != nil {
		panic(&sourceException{err})
	}
	if !u.IsAbs() {
		return u.String()
	}
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) || u.User != nil {
		s.backend.fatal(&MissingCapability{"source-bound adapter HTTP target", "current sites.request only accepts same-origin relative URLs; source adapter requires an alias or API origin"})
	}
	return u.RequestURI()
}
func timeoutNumber(raw any) (*float64, error) {
	// RequestUtils uses20 for every falsey value before urllib3 validates it.
	if !Truth(raw) {
		return nil, nil
	}
	if _, ok := raw.(bool); ok {
		return nil, errors.New("Timeout cannot be a boolean value. It must be an int, float or None.")
	}
	var seconds float64
	integer := false
	switch v := raw.(type) {
	case json.Number:
		integer = !strings.ContainsAny(string(v), ".eE")
		if integer {
			n, ok := numeric(v)
			if !ok {
				return nil, fmt.Errorf("invalid source integer timeout")
			}
			seconds, _ = n.Float64()
		} else {
			seconds, _ = strconv.ParseFloat(string(v), 64)
		}
	case float64:
		seconds = v
	case int:
		seconds = float64(v)
		integer = true
	default:
		return nil, fmt.Errorf("Timeout value connect was %s, but it must be an int, float or None.", pythonScalar(raw))
	}
	if math.IsNaN(seconds) {
		return nil, errors.New("Invalid value NaN (not a number)")
	}
	if seconds <= 0 {
		return nil, fmt.Errorf("Attempted to set connect timeout to %s, but the timeout cannot be set to a value less than or equal to 0.", pythonScalar(raw))
	}
	if math.IsInf(seconds, 1) && integer {
		return nil, errors.New("int too large to convert to float")
	}
	if math.IsInf(seconds, 1) || seconds > float64((1<<63-1)/int64(time.Second)) {
		return nil, errors.New("timestamp out of range for platform time_t")
	}
	return &seconds, nil
}

func (s sdkSiteTransport) checkMetadata() {
	if Truth(s.site["_host_timeout_unavailable"]) {
		s.backend.fatal(&MissingCapability{"source indexer timeout", "safe metadata omits the raw configured timeout and its null/falsey value"})
	}
	if Truth(s.site["_host_empty_ua_ambiguous"]) {
		s.backend.fatal(&MissingCapability{"raw site user-agent", "current credentials DTO conflates source None and empty string"})
	}
}
func (s sdkSiteTransport) Do(ctx context.Context, request PageRequest) (*PageResponse, error) {
	return s.sourceDo(ctx, request)
}

func (s sdkSiteTransport) Page(ctx context.Context, request PageRequest) (string, error) {
	if _, bound := s.site["_host_source_reference"].(sdk.SiteSourceReferenceV1); !bound {
		// Keep the existing fail-closed legacy metadata path. A selected
		// original row already has its raw fields and scoped reference.
		s.checkMetadata()
	}
	return s.sourcePage(ctx, request)
}

func (s sdkSiteTransport) Post(ctx context.Context, plan RequestPlan) (*AdapterResponse, error) {
	headers := Object{}
	for key, value := range plan.Headers {
		headers[key] = value
	}
	r, err := s.Do(ctx, PageRequest{Method: plan.Method, URL: plan.URL, Headers: headers, Form: plan.Data, FormOrder: []string{"_timestamp", "_sgin"}, Timeout: plan.Timeout})
	if err != nil || r == nil {
		return nil, err
	}
	value, decodeErr := parseSiteJSON(r.text())
	return &AdapterResponse{Status: r.Status, Payload: value, JSONError: decodeErr}, nil
}
func (b sdkBackend) visitor(site Object) SiteVisitor {
	transport := sdkSiteTransport{backend: b, site: site}
	hooks := SiteVisitHooks{HTTP: transport, Browser: transport, SourcePageValue: transport.sourcePageValue, MTeam: transport, Now: time.Now}
	if b.now != nil {
		hooks.Now = b.now
	}
	hooks.Decode = func(content []byte, fallback func() string) (string, error) {
		return decodeSourcePage(content, fallback, func(level, message string) error { return b.log(level, message) })
	}
	hooks.OCR = func(ctx context.Context, image string) (any, error) {
		result, err := (sdk.CaptchaOCR{Host: b.host}).Text(ctx, sdk.CaptchaOCRRequestV1{SourceReference: sourceReferencePointer(transport.reference(ctx)), ImageURL: image})
		b.fatal(err)
		if result.SourceResponse != nil {
			return sourceOCRServiceResult(result.SourceResponse.Content, result.SourceResponse.ContentType)
		}
		var value any
		err = Decode(result.Result, &value)
		if err != nil {
			return nil, err
		}
		return &sourceOCRValue{value: value, raw: append(json.RawMessage(nil), result.Result...)}, nil
	}
	hooks.Stats = func(ctx context.Context, _ string, success bool, seconds int) error {
		var elapsed *int64
		if success {
			n := int64(seconds)
			elapsed = &n
		}
		_, err := (sdk.Sites{Host: b.host}).SourceRecordAccess(ctx, sdk.SiteSourceAccessResultV1{Reference: transport.reference(ctx), Success: success, Seconds: elapsed})
		return err
	}
	hooks.Log = func(level, message string) error {
		message = redactSiteDiagnostic(message, site)
		return b.log(level, message)
	}
	hooks.Tjupt = &TjuptDependencies{Cache: &sdkTjuptCache{backend: b}, Public: sdkTjuptPublic{backend: b}}
	return SiteVisitor{Adapters: InstalledAdapterRegistry(), Hooks: hooks}
}

func sourceReferencePointer(value sdk.SiteSourceReferenceV1) *sdk.SiteSourceReferenceV1 {
	return &value
}
