package removelink

import (
	"reflect"
	"testing"
)

func TestRuntimeSupportDeclaresLinuxLocalWithoutChangingAuthority(t *testing.T) {
	m := Manifest()
	if m.Validate() != nil || m.RuntimeSupport == nil || !reflect.DeepEqual(m.RuntimeSupport.Platforms, []string{"linux"}) || !reflect.DeepEqual(m.RuntimeSupport.StorageTypes, []string{"local"}) {
		t.Fatal("incorrect support declaration")
	}
	if !reflect.DeepEqual(m.Capabilities, []string{ConfigureCapability, ApplyCapability}) {
		t.Fatal("support metadata changed RPC authority")
	}
	m.RuntimeSupport.Platforms[0] = "mutated"
	if Manifest().RuntimeSupport.Platforms[0] != "linux" {
		t.Fatal("shared mutable declaration")
	}
}
