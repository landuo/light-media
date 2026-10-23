<script setup lang="ts">
defineProps<{ name: string }>();

const paths: Record<string, string> = {
  compass: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z m-6-3-2 4-4 2 2-4 4-2Z',
  search: 'M15.5 15.5 21 21 M17 10a7 7 0 1 1-14 0 7 7 0 0 1 14 0Z',
  plus: 'M12 5v14 M5 12h14',
  plugin:
    'M9 3v3H3v5h3a2 2 0 1 1 0 4H3v6h6v-3a2 2 0 1 1 4 0v3h6v-6h-3a2 2 0 1 1 0-4h3V6h-6V3a2 2 0 1 0-4 0Z',
  close: 'M6 6l12 12 M18 6 6 18',
  refresh: 'M20 8a8 8 0 1 0 0 8 M20 3v5h-5',
  more: 'M12 5h.01 M12 12h.01 M12 19h.01',
  grip: 'M9 5h.01 M15 5h.01 M9 12h.01 M15 12h.01 M9 19h.01 M15 19h.01',
  upload: 'M12 16V3 M7 8l5-5 5 5 M4 16v5h16v-5',
  download: 'M12 3v13 M7 11l5 5 5-5 M4 16v5h16v-5',
  chart: 'M4 4v16h17 M9 15v-4 M14 15V7 M19 15v-6',
  check: 'M5 12l4 4L19 6',
  save: 'M4 3h13l4 4v14H3V3h1Z M7 3v6h10V3 M7 21v-8h10v8',
  clipboard: 'M8 5H5v16h14V5h-3 M9 3h6v4H9V3Z M8 11h8 M8 15h2 M8 18h2 M13 15h3',
  playlist: 'M4 5h12 M4 10h12 M4 15h7 M16 13l5 4-5 4v-8Z',
  quality: 'M3 5h18v14H3V5Z M7 8v8 M11 8v8 M7 12h4 M15 8h3v8h-3V8Z M17 14l3 4',
  sparkles:
    'm15 2 1.8 4.2L21 8l-4.2 1.8L15 14l-1.8-4.2L9 8l4.2-1.8L15 2Z M3 18l7-7 M3 3v4 M1 5h4 M18 17v4 M16 19h4',
  clock: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M12 7v5l3 2',
  'arrow-up': 'M12 20V4 M6 10l6-6 6 6',
  'arrow-down': 'M12 4v16 M6 14l6 6 6-6',
  'arrow-left': 'M20 12H4 M10 6l-6 6 6 6',
  'arrow-right': 'M4 12h16 M14 6l6 6-6 6',
  edit: 'M4 16 16 4l4 4L8 20H4v-4Z M13 7l4 4',
  trash: 'M4 6h16 M9 6V3h6v3 M6 6l1 15h10l1-15 M10 10v7 M14 10v7',
  key: 'M14 8a5 5 0 1 1-2 4l-8 8H1v-3l8-8 M16 6h.01',
  external: 'M14 3h7v7 M21 3 11 13 M10 3H4v17h16v-6',
  globe: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M3 12h18 M12 3c5 5 5 13 0 18 M12 3c-5 5-5 13 0 18',
  play: 'm8 5 11 7-11 7V5Z',
  pause: 'M8 5v14 M16 5v14',
  folder: 'M3 6h7l2 3h9v11H3V6Z M3 9h18',
  'chevron-right': 'm9 5 7 7-7 7',
  'chevron-left': 'm15 5-7 7 7 7',
  'chevron-down': 'm5 9 7 7 7-7',
  'chevron-up': 'm5 15 7-7 7 7',
  copy: 'M9 9h12v12H9V9Z M15 9V3H3v12h6',
  logout: 'M9 4H4v16h5 M8 12h13 M16 7l5 5-5 5',
  bell: 'M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9 M10 21h4',
  filter: 'M3 4h18l-7 8v7l-4 2v-9L3 4Z',
  sort: 'M5 4v16 M2 17l3 3 3-3 M11 5h9 M11 10h7 M11 15h5 M11 20h3',
  film: 'M4 3h16v18H4V3Z M4 8h16 M4 16h16 M8 3v18 M16 3v18',
  movie: 'M4 3h16v18H4V3Z M4 8h16 M4 16h16 M8 3v18 M16 3v18',
  tv: 'M3 7h18v14H3V7Z M8 2l4 5 4-5',
  history: 'M3 11a9 9 0 1 1 2 7 M3 4v7h7 M12 7v5l3 2',
  jobs: 'm3 6 2 2 4-4 M12 6h9 M3 13l2 2 4-4 M12 13h9 M3 20l2 2 4-4 M12 20h9',
  logs: 'M3 4h18v16H3V4Z m4 4 4 4-4 4 M14 16h4',
  server: 'M3 3h18v7H3V3Z M3 14h18v7H3v-7Z M7 6.5h.01 M7 17.5h.01 M16 6.5h2 M16 17.5h2',
  star: 'm12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-3-5.6 3L7.5 14 3 9.6l6.2-.9L12 3Z',
  heart:
    'M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 21l8.8-8.6a5.5 5.5 0 0 0 0-7.8Z',
  menu: 'M4 6h16 M4 12h16 M4 18h16',
  info: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M12 11v6 M12 7h.01',
  'check-circle': 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z m-13-1 3 3 5-5',
  calendar: 'M3 6h18v15H3V6Z M7 3v6 M17 3v6 M3 11h18 M7 15h2 M15 15h2',
  settings:
    'M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1 1-3Z M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z',
  warning: 'm12 3 10 18H2L12 3Z M12 9v5 M12 17h.01',
};
</script>

<template>
  <svg
    class="app-icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.75"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path :d="paths[name] || paths.globe" />
  </svg>
</template>

<style scoped>
.app-icon {
  width: 20px;
  height: 20px;
  display: inline-block;
  flex: none;
  vertical-align: middle;
}
</style>
