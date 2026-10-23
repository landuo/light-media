package plugins

import (
	"encoding/json"
	"testing"
)

func TestManifestFormModelPolicyIsPresentationOnly(t *testing.T) {
	for _, policy := range []*FormModelPolicy{nil, {Installed: FormModelSource, Preview: FormModelHostOverlay}, {Installed: FormModelSource, Preview: FormModelSource}, {Installed: FormModelHostOverlay, Preview: FormModelHostOverlay}} {
		for _, projection := range []string{"", ConfigFormProjectionTransactional} {
			manifest := fixtureManifest()
			manifest.FormModelPolicy, manifest.ConfigFormProjection = policy, projection
			if err := manifest.Validate(); err != nil {
				t.Fatal("presentation policy was coupled to write permission", err)
			}
			raw, err := json.Marshal(manifest)
			var roundtrip Manifest
			if err != nil || json.Unmarshal(raw, &roundtrip) != nil || roundtrip.Validate() != nil || roundtrip.ConfigFormProjection != projection {
				t.Fatal("form policy did not roundtrip independently")
			}
			if policy == nil {
				if roundtrip.FormModelPolicy != nil {
					t.Fatal("legacy manifest acquired an explicit model policy")
				}
			} else if roundtrip.FormModelPolicy == nil || *roundtrip.FormModelPolicy != *policy {
				t.Fatal("installed/preview declarations were lost")
			}
		}
	}
}

func TestManifestFormModelPolicyRejectsUnknownOrPartialValues(t *testing.T) {
	for _, policy := range []FormModelPolicy{{}, {Installed: FormModelSource}, {Preview: FormModelHostOverlay}, {Installed: "worker", Preview: FormModelSource}, {Installed: FormModelSource, Preview: "SOURCE"}} {
		manifest := fixtureManifest()
		manifest.FormModelPolicy = &policy
		if manifest.Validate() == nil {
			t.Fatal("unknown or partial Form policy accepted")
		}
	}
}
