// Opt-in display reads only: never time out mutations implicitly.
export async function boundedRead<T>(
  read: (signal: AbortSignal) => Promise<T>,
  parent: AbortSignal,
  budget = 10000,
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort(parent.reason);
  parent.addEventListener('abort', abort, { once: true });
  if (parent.aborted) abort();
  const timer = setTimeout(() => controller.abort(new Error('读取超时，请重试')), budget);
  try {
    return await read(controller.signal);
  } finally {
    clearTimeout(timer);
    parent.removeEventListener('abort', abort);
  }
}
