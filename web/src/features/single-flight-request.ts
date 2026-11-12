// Polls share one request, even when the server takes longer than the interval.
// Cancellation invalidates late responses as well as aborting the transport.
export function createSingleFlightRequest<T>(options: {
  request: (key: string, signal: AbortSignal) => Promise<T>;
  result: (value: T) => void;
  error: (error: unknown) => void;
  settled: () => void;
}) {
  let current: { controller: AbortController; promise: Promise<void> } | undefined;
  function cancel() {
    const old = current;
    current = undefined;
    old?.controller.abort();
  }
  function run(key: string) {
    if (current) return current.promise;
    const flight = { controller: new AbortController(), promise: Promise.resolve() };
    current = flight;
    flight.promise = Promise.resolve()
      .then(() => {
        if (flight.controller.signal.aborted) return;
        return options.request(key, flight.controller.signal);
      })
      .then((value) => {
        if (current === flight) options.result(value as T);
      })
      .catch((error: unknown) => {
        if (current === flight) options.error(error);
      })
      .finally(() => {
        if (current !== flight) return;
        current = undefined;
        options.settled();
      });
    return flight.promise;
  }
  return { run, cancel, get pending() { return !!current; } };
}
