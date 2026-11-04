// Concrete value and dependency contracts for the original TrimeMedia wrapper.
// This first slice is not an HTTP Api implementation or a healthy default fake.
package mediaservervendors

import (
	"context"
	"encoding/json"
	"math/big"
)

type TrimeCategory string

const (
	TrimeMovie  TrimeCategory = "Movie"
	TrimeTV     TrimeCategory = "TV"
	TrimeMix    TrimeCategory = "Mix"
	TrimeOthers TrimeCategory = "Others"
)

type TrimeItemType string

const (
	TrimeMovieItem TrimeItemType = "Movie"
	TrimeTVItem    TrimeItemType = "TV"
)

// These are actual retained data objects, corresponding to original dataclasses.
// Initial typed domain is the original collector's ordinary string/nullable
// string/list and finite integer fields, not arbitrary Python dynamic objects.
type TrimeUser struct {
	GUID, Username string
	IsAdmin        int64
}
type TrimeVersion struct{ Frontend, Backend *string }
type TrimeMediaDB struct {
	GUID                 string
	Category             TrimeCategory
	Name                 *string
	Posters, Directories []string
}

// All thirteen original MediaServerLibrary fields are represented, including
// null defaults. The current wrapper supplies string GUIDs; arbitrary union IDs
// are not manufactured from this finite dataclass input domain.
type TrimeLibrary struct {
	Server, ID, ItemID, ServerID, Name *string
	Path                               []string
	Type                               *string
	ItemCount                          *int64
	Image                              *string
	ImageList                          []string
	Link, ServerType                   *string
	UseCookies                         *bool
	FieldsSet                          []string
}

// TrimeAPI is a mandatory live dependency seam, never implemented by config.
// Token/Host are properties in the original Api; errors here preserve owner/
// context failures. Returned object pointers remain real retained references.
type TrimeAPI interface {
	Token(context.Context) (*string, error)
	Host(context.Context) (string, error)
	VerifyAccessCode(context.Context) (bool, error)
	SystemVersion(context.Context) (*TrimeVersion, error)
	Login(context.Context, string, string) (*string, error)
	Logout(context.Context) (bool, error)
	UserInfo(context.Context) (*TrimeUser, error)
	MediaDBList(context.Context) ([]*TrimeMediaDB, error)
	AdminMediaDBList(context.Context) ([]*TrimeMediaDB, error)
	ItemCount(context.Context, string, []TrimeItemType) (*int64, error)
	TaskRunning(context.Context) (any, error)
	ScanAll(context.Context) (*bool, error)
	Close(context.Context) error
}
type TrimeDiagnostic struct{ Level, Stage string }
type TrimeDependencies struct {
	NewAPI    func(context.Context, string, string, *string) (TrimeAPI, error)
	NewRawAPI func(context.Context, string, string, json.RawMessage) (TrimeAPI, error)
	Report    func(TrimeDiagnostic)
}

// Nonempty path matching is deliberately not implemented by this first slice.
// A future typed path adapter must use actual owned Path.resolve semantics, not
// prefix strings. These values are never used to pretend a nonempty match.
type TrimeRefreshYear struct {
	Text    *string
	Integer *big.Int
}
type TrimeRefreshItem struct {
	Title                           *string
	Year                            *TrimeRefreshYear
	MediaType, Category, TargetPath *string
}

// Optional constructor-only compatibility seams. The concrete native Api
// implements both; old ordinary-string API implementations remain compatible.
type TrimeRawLogin interface {
	LoginJSON(context.Context, json.RawMessage, json.RawMessage) (*string, error)
}
type TrimeRawAccess interface {
	SetConstructorAccessCode(context.Context, json.RawMessage) error
}
