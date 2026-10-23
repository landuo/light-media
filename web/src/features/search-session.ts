import { reactive, watch, type Ref } from 'vue';

// Navigation is deliberately separate from submitting a search. Cached pages
// consume each explicit request once, including a second search for the same term.
export const searchSession = reactive({ lastRoute: 'search', route: '', request: 0 });
const consumed = new Map<string, number>();

export function startSearch(route: string) {
  searchSession.lastRoute = route;
  searchSession.route = route;
  searchSession.request++;
  location.hash = route;
}

export function useExplicitSearch(route: string, run: () => unknown, ready?: Ref<boolean>) {
  return watch(
    () => [searchSession.request, ready?.value ?? true],
    () => {
      if (ready && !ready.value) return;
      if (searchSession.route !== route || !searchSession.request) return;
      if (consumed.get(route) === searchSession.request) return;
      consumed.set(route, searchSession.request);
      void run();
    },
    { immediate: true },
  );
}

export function clearSearchSession() {
  searchSession.lastRoute = 'search';
  searchSession.route = '';
  consumed.clear();
}
