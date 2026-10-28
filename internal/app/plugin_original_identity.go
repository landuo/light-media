package app

import "context"

func (a *App) originalLoadedPluginSnapshot() []*originalPluginInstance {
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	objects := make([]*originalPluginInstance, 0, len(a.pluginOriginalRunningOrder))
	for _, id := range a.pluginOriginalRunningOrder {
		if value, ok := a.pluginRunningInstances.Load(id); ok {
			objects = append(objects, value.(*originalPluginInstance))
		}
	}
	return objects
}

func (p *originalPluginInstance) originalDisplayName(ctx context.Context) (string, error) {
	metadata, err := p.app.pluginStore.instanceMetadata(ctx, p.app.Store.DB, p.installation.InstanceID)
	if err != nil {
		return "", err
	}
	if metadata.SourceInstanceID != "" {
		return metadata.Name, nil
	}
	return p.manifest.Name, nil
}
