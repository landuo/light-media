type TransferType = { title: string; value: string };
type Directory = { mode: string; _transferTypes?: TransferType[] };

// Fixed DirectoryCard performs two sequential queries even for local → local.
// Only operations already supported by the Go organizer can be selected.
export async function loadDirectoryTransferTypes(
  directory: Directory,
  request: () => Promise<any>,
  nextTick: (callback: () => void) => unknown,
) {
  const source = (await request()).transtype;
  const destination = (await request()).transtype;
  if (!source || !destination) return;
  const modes: Record<string, string> = { link: 'hardlink', copy: 'copy', auto: 'auto' };
  const items = Object.keys(source)
    .filter((key) => key in destination && key in modes)
    .map((key) => ({ title: source[key], value: modes[key] }));
  directory._transferTypes = items;
  if (!items.length) directory.mode = '';
  else if (!items.some((item) => item.value === directory.mode)) {
    nextTick(() => { directory.mode = items[0].value; });
  }
}
