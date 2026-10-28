import ColorThief from 'colorthief/dist/color-thief.mjs';

// Same browser ColorThief 2.6.0, quality, FIFO promise cache and fallback as the
// fixed frontend's @core/utils/image.ts. No server-side image runtime is used.
export function createPluginIconColorReader(
  extract: (image: HTMLImageElement, quality: number) => number[] = (image, quality) =>
    new ColorThief().getColor(image, quality),
) {
  const cache = new Map<string, Promise<string>>();
  return async function color(
    image?: HTMLImageElement | null,
    options: { fallback?: string; quality?: number } = {},
  ) {
    const fallback = options.fallback ?? '#28A9E1';
    if (!image) return fallback;
    const key = image.currentSrc || image.src || '';
    const cached = key ? cache.get(key) : undefined;
    if (cached) return cached;
    const pending = Promise.resolve()
      .then(() => {
        const rgb = extract(image, options.quality ?? 20);
        if (rgb.length !== 3 || rgb.some(isNaN)) throw new Error('Invalid RGB string format');
        return '#' + rgb.map((channel) => channel.toString(16).padStart(2, '0')).join('');
      })
      .catch(() => fallback);
    if (key) {
      if (cache.size >= 100) {
        const first = cache.keys().next().value;
        if (first) cache.delete(first);
      }
      cache.set(key, pending);
    }
    return pending;
  };
}
export const pluginIconColor = createPluginIconColorReader();
