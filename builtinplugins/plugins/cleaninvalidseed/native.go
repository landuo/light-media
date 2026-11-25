package cleaninvalidseed

func nativeQB(fields map[string]any) *Object {
	o := &Object{Attrs: fields, Mapping: true}
	o.Accessor = func(k string) (any, bool, error) {
		switch k {
		case "state_enum":
			v, ok := fields["state"]
			if !ok {
				return nil, false, nil
			}
			paused := v == "pausedUP" || v == "pausedDL" || v == "stoppedUP" || v == "stoppedDL"
			return &Object{Attrs: map[string]any{"is_paused": paused}}, true, nil
		}
		return nil, false, nil
	}
	return o
}
func nativeTR(fields map[string]any) (*Object, error) {
	if _, ok := fields["id"]; !ok {
		return nil, source("ValueError", "Torrent requires id")
	}
	o := &Object{Attrs: map[string]any{}, Faults: map[string]string{}}
	for attr, key := range map[string]string{"name": "name", "total_size": "totalSize", "download_dir": "downloadDir", "hashString": "hashString", "hash_string": "hashString", "labels": "labels"} {
		if v, ok := fields[key]; ok {
			o.Attrs[attr] = v
		} else {
			o.Faults[attr] = "KeyError"
		}
	}
	if v, ok := fields["status"]; ok {
		statuses := []string{"stopped", "check pending", "checking", "download pending", "downloading", "seed pending", "seeding"}
		found := false
		for i, s := range statuses {
			if eq(v, i) {
				o.Attrs["status"] = s
				found = true
			}
		}
		if !found {
			o.Faults["status"] = "KeyError"
		}
	} else {
		o.Faults["status"] = "KeyError"
	}
	return o, nil
}
