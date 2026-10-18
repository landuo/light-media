// Only original frontends whose native hosts are explicitly implemented may
// select a module graph. Neither a manifest nor configuration supplies a URL.
export function hasOriginalPluginUI(pluginID: string) {
  return pluginID === 'SeedRescue' || pluginID === 'BrushFlowLanduo';
}
