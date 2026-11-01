# Fixed native type routing increment — prepared only

Original candidate01/CONTRACT/tar remain unchanged. candidate02 adds only two
fixed operation names: native_tv and native_unknown. Both use explicit900045,
meta/share_meta null; mtype is respectively 电视剧/null. Existing native is电影.
The module returns None for these three type values only for900045; existing
module_hit900043/module_false900044 remain movie-only. No arbitrary ID/type or
provider endpoint parameter, no native response fabrication, no worker retry.
Ordinary CLI input allowlist/source is unchanged and never selects these ops.
Its actual worker program/input closure must nevertheless bind candidate02,
not silently inherit candidate01's executable or exact closure.
New unitroot TestOwnedMediaRecognitionFixtureNativeTypeRoutes is recordingHost
routing only, not actual native/provider proof. Original unitroot text unchanged.
