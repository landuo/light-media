# Native model/group actual05 — finite component PASS

Candidate05 `ecac8aa2a9410920348c2586e3055d443980be005e4e2855c14c5a1a42fec072` is now integrated byte-for-byte for all 12 frozen members. Independent metadata readback verified the three gates' before/after-compile/after-test maps, all 6,280 unique current input paths, three actual programs, and three preserved generated test-main sources. No current drift, failed/skipped root, nonzero command exit or race-warning string was found.

| Actual gate | Input paths | Actual result | Acceptance here |
|---|---:|---|---|
| `evidence/s11-id-native-app05` | 6,262 | Normal 5 roots PASS; ended 01:45:23 UTC | Native model/group root and old 80-case model root only |
| `evidence/s11-id-native-catalog05` | 2,288 | Normal 5 roots PASS | Artifact/input/result integrity; semantic review remains with catalog reviewer |
| `evidence/s11-id-native-catalog-race05` | 2,296 | Race 5 roots PASS | Same limited integrity readback; **not an App model/group race run** |

`TestOriginalRecognitionNativeModelAndGroupBoundaries` actually passed its fixed assertions:
- Provider augmentation → full model assembly preserves the ordinary source IMDb string `owned-not-imdb` and calculated category; category matching does not require UI title/id/date validity, and the field equal to the synthetic API key `owned` is not redacted.
- `genres:[null]` yields a direct `originalMediaRecognitionSourceError{Class:"AttributeError"}` with no model; the next valid augmentation succeeds. This tests the precise helper boundary, not the complete App catch/reentry chain.
- Null/scalar episode shapes each produce one ERROR and empty transformed groups; the later valid transformation retains both group rows. True→1 and 1→True keep the first key spelling and replace its value; -0.0→False→0 remains one identity with first wire key `-0.0` and last value 3.
- Response `[]` returns nil without ERROR; truthy non-object `[{}]` returns nil with one ERROR. This is the response-object source-catch helper, not an assertion that every native miss/error has run end to end.
- Controlled current-configuration ABA changes reject client binding; the inherited guard's exact error is retained; an allowed snapshot binds `owned-A`, and changing to `owned-B` invalidates it. No provider request, concurrent configuration stress or actual credential is involved in these assertions.

`TestPluginNoticeV2TMDBMediaInfoOriginalAST` actually passed **80/80 named subtests**, comparing the retained full fields against the unmodified source oracle. Its non-Go input `internal/app/testdata/notice-media-source-oracle.json` is present in all three App input maps and current bytes, SHA `2aba300c54f93ffa46a7320047dda0694b8e47e21fe1b87912e4efa2a2a838ac`. The previously reviewed two-phase augmentation split remains the same frozen source. No source oracle was regenerated.

The other two App endpoint roots were authored by this reviewer: their raw PASS presence is recorded, **not independently approved here**. App endpoint and cache/session/TLS semantics remain with their designated reviewers. This is not ordinary-server, installed-worker, owned-launcher, full S11 or real TMDB network acceptance. The new model/group cases are static-source witnesses, not newly executed source-model oracle cases; arbitrary Python objects and previously declared unsupported key/model domains remain outside the claim.

Machine evidence: `actual05-model-readback.json` SHA `1deb166116a3e620ab2356a5bea99b4c5dd842b29626ded24caa3baf583aa1d4`. Reviewer only read artifacts and wrote this audit; no Go/source/test/service/network operation or product/ledger edit was performed.
