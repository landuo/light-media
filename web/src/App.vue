<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue';
import { api, action, ui, refreshSettings, refreshStatus, go, toast, errorText, date } from './api';
import Catalog from './components/Catalog.vue';
import Resources from './components/Resources.vue';
import KeywordResources from './components/KeywordResources.vue';
import Subscriptions from './components/Subscriptions.vue';
import Operations from './components/Operations.vue';
import Settings from './components/Settings.vue';
import SiteManager from './components/SiteManager.vue';
import DownloaderManager from './components/DownloaderManager.vue';
import DirectoryManager from './components/DirectoryManager.vue';
import FileManager from './components/FileManager.vue';
import TransferHistory from './components/TransferHistory.vue';
import BrandMark from './components/BrandMark.vue';
const route = ref(location.hash.slice(1) || 'discover'),
  auth = ref<any>(null),
  username = ref(''),
  password = ref(''),
  authError = ref(''),
  starting = ref(true),
  mobile = ref(false);
const globalQuery = ref(''),
  searchInput = ref<HTMLInputElement>(),
  initialFilePaths = ref<string[]>([]);
const pageKey = computed(() => route.value.split('?')[0]!.split('/')[0]!);
const icons: Record<string, string> = {
  search: 'M21 21l-5-5M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',
  star: 'm12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-3-5.6 3 1.1-6.2L3 9.6l6.2-.9Z',
  movie: 'M3 6h18v15H3ZM3 6l3-4m3 4 3-4m3 4 3-4M3 11h18',
  tv: 'M3 5h18v14H3ZM8 23l4-4 4 4',
  calendar: 'M3 5h18v16H3ZM7 2v6m10-6v6M3 10h18M7 14h2m3 0h2m3 0h1M7 18h2m3 0h2',
  download: 'M12 2v13m-5-5 5 5 5-5M3 16v5h18v-5',
  history: 'M3 12a9 9 0 1 1 3 6M3 3v9h9m0-6v6l4 3',
  folder: 'M2 5h8l2 3h10v13H2Z',
  library: 'M3 3h4v18H3Zm7 0h4v18h-4Zm7 1 4-1 3 17-4 1Z',
  globe: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0M3 12h18M12 3c5 5 5 13 0 18-5-5-5-13 0-18',
  server: 'M3 3h18v7H3Zm0 11h18v7H3ZM7 6h1m-1 11h1',
  jobs: 'M9 5h12M9 12h12M9 19h12M2 5l2 2 3-4M2 12l2 2 3-4M2 19l2 2 3-4',
  logs: 'm3 5 5 5-5 5m9 0h9',
  settings:
    'M12 8a4 4 0 1 1 0 8 4 4 0 0 1 0-8M10 2h4l1 3 3 1 3-1 2 4-2 2v3l2 2-2 4-3-1-3 1-1 3h-4l-1-3-3-1-3 1-2-4 2-2v-3L1 9l2-4 3 1 3-1Z',
};
const nav = [
  {
    label: '',
    items: [
      ['discover', 'star', '发现'],
      ['search', 'search', '搜索结果'],
    ],
  },
  {
    label: '订阅',
    items: [
      ['subscriptions?type=movie', 'movie', '电影'],
      ['subscriptions?type=tv', 'tv', '电视剧'],
      ['calendar', 'calendar', '日历'],
    ],
  },
  {
    label: '整理',
    items: [
      ['downloads', 'download', '下载管理'],
      ['history', 'history', '整理历史'],
      ['files', 'folder', '文件管理'],
      ['library', 'library', '媒体库'],
    ],
  },
  {
    label: '系统',
    items: [
      ['sites', 'globe', '站点管理'],
      ['downloaders', 'server', '下载器'],
      ['directories', 'folder', '目录管理'],
      ['jobs', 'jobs', '任务队列'],
      ['logs', 'logs', '日志'],
      ['settings', 'settings', '设置'],
    ],
  },
];
const activeNav = (target: string) =>
  target.includes('?')
    ? route.value.startsWith(target)
    : pageKey.value === target || (target === 'search' && pageKey.value === 'resource-search');
const navHref = (target: string) =>
  '#' +
  (target === 'search' && globalQuery.value.trim()
    ? 'search?query=' + encodeURIComponent(globalQuery.value.trim())
    : target);
const title = computed(
  () =>
    nav.flatMap((g) => g.items).find((n) => activeNav(n[0]!))?.[2] ||
    (
      {
        detail: '影视详情',
        person: '演员资料',
        resources: '搜索资源',
        'resource-search': '资源搜索',
      } as any
    )[pageKey.value] ||
    '光影',
);
const paused = computed(() =>
  typeof ui.status?.control === 'string'
    ? ui.status.control !== 'running'
    : !!ui.status?.control?.paused,
);
const notifications = ref<any>({ items: [], unread: 0 }),
  notificationsOpen = ref(false);
async function loadNotifications() {
  notifications.value = await api('/notifications');
}
async function readNotification(n: any, jump = false) {
  const r = await action(() => api('/notifications/' + encodeURIComponent(n.id) + '/read', {}));
  if (r !== undefined) await loadNotifications();
  if (jump && n.job_id) {
    notificationsOpen.value = false;
    go('jobs/' + encodeURIComponent(n.job_id));
  }
}
function change() {
  route.value = location.hash.slice(1) || 'discover';
  mobile.value = false;
  window.scrollTo(0, 0);
  if (pageKey.value !== 'files') initialFilePaths.value = [];
}
function searchMedia() {
  if (globalQuery.value.trim()) go('search?query=' + encodeURIComponent(globalQuery.value.trim()));
}
function searchShortcut(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    searchInput.value?.focus();
  }
}
function organizePaths(paths: string[]) {
  initialFilePaths.value = paths;
  go('files');
}
function expired() {
  auth.value = { authenticated: false, setup_required: false };
  ui.settings = null;
}
async function bootstrap() {
  try {
    auth.value = await api('/auth/status');
    if (auth.value.authenticated) {
      await Promise.all([refreshSettings(), refreshStatus()]);
      loadNotifications().catch(() => {});
    }
  } catch (e) {
    authError.value = errorText(e);
  } finally {
    starting.value = false;
  }
}
async function login() {
  authError.value = '';
  try {
    await api(auth.value.setup_required ? '/auth/setup' : '/auth/login', {
      username: username.value,
      password: password.value,
    });
    password.value = '';
    await bootstrap();
    if (auth.value.setup_required === false && !ui.settings?.sites?.length) go('settings');
  } catch (e) {
    authError.value = (e as Error).message;
  }
}
async function logout() {
  await action(() => api('/auth/logout', {}));
  expired();
}
async function control() {
  const next = !paused.value;
  const r = await action(
    () => api('/control', { paused: next }),
    next ? '自动流程已暂停' : '自动流程已恢复',
  );
  if (r) await refreshStatus();
}
let timer: number;
onMounted(() => {
  window.addEventListener('hashchange', change);
  window.addEventListener('session-expired', expired);
  window.addEventListener('keydown', searchShortcut);
  bootstrap();
  timer = window.setInterval(() => {
    if (auth.value?.authenticated && !document.hidden) {
      refreshStatus().catch(() => {});
      loadNotifications().catch(() => {});
    }
  }, 15000);
});
onUnmounted(() => {
  clearInterval(timer);
  window.removeEventListener('hashchange', change);
  window.removeEventListener('session-expired', expired);
  window.removeEventListener('keydown', searchShortcut);
});
</script>
<template>
  <div v-if="starting" class="auth-shell">
    <div class="auth-card">
      <div class="brand-symbol"><BrandMark /></div>
      <h1>光影</h1>
      <p class="muted">正在连接你的媒体空间…</p>
    </div>
  </div>
  <div v-else-if="!auth?.authenticated" class="auth-shell">
    <form class="auth-card" @submit.prevent="login">
      <div class="brand-symbol"><BrandMark /></div>
      <p class="eyebrow">LIGHT MEDIA</p>
      <h1>{{ auth?.setup_required ? '创建你的媒体空间' : '欢迎回来' }}</h1>
      <p class="muted">
        {{
          auth?.setup_required
            ? '设置管理员账号，然后连接站点、下载器和媒体库。'
            : '登录后继续发现、收藏与整理。'
        }}
      </p>
      <label
        >用户名<input v-model="username" autocomplete="username" required maxlength="64" /></label
      ><label
        >密码<input
          v-model="password"
          type="password"
          :autocomplete="auth?.setup_required ? 'new-password' : 'current-password'"
          required
          :minlength="auth?.setup_required ? 12 : 1"
      /></label>
      <p v-if="auth?.setup_required" class="hint">管理员密码至少 12 位。</p>
      <p v-if="authError" class="error-box">{{ authError }}</p>
      <button class="primary full" :disabled="ui.busy > 0">
        {{ auth?.setup_required ? '创建并开始' : '登录' }}
      </button>
    </form>
  </div>
  <div v-else class="app-shell">
    <div v-if="mobile" class="sidebar-shade" @click="mobile = false"></div>
    <aside :class="['sidebar', { open: mobile }]">
      <a class="brand" href="#discover"
        ><span class="brand-mark"><BrandMark /></span><span>光影<small>LIGHT MEDIA</small></span></a
      >
      <nav>
        <template v-for="group in nav" :key="group.label">
          <div v-if="group.label" class="nav-caption">{{ group.label }}</div>
          <a
            v-for="n in group.items.filter((item) => item[0] !== 'settings')"
            :key="n[0]"
            :href="navHref(n[0]!)"
            :class="{ active: activeNav(n[0]!) }"
            :aria-current="activeNav(n[0]!) ? 'page' : undefined"
            ><span class="nav-icon"
              ><svg
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.7"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
              >
                <path :d="icons[n[1]!]" /></svg></span
            >{{ n[2]
            }}<span v-if="n[0] === 'jobs' && ui.status?.counts?.active_jobs" class="counter">{{
              ui.status.counts.active_jobs
            }}</span></a
          ></template
        >
      </nav>
      <div class="sidebar-bottom">
        <a
          href="#settings"
          class="sidebar-settings"
          :class="{ active: pageKey === 'settings' }"
          :aria-current="pageKey === 'settings' ? 'page' : undefined"
          ><svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path :d="icons.settings" /></svg
          >设置<span aria-hidden="true">›</span></a
        >
        <span class="dot" :class="{ amber: paused }"></span
        >{{ paused ? '自动流程已暂停' : ui.status ? '服务运行中' : '服务状态待确认' }}
      </div>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <button
          class="icon-button mobile-toggle"
          aria-label="打开导航"
          :aria-expanded="mobile"
          @click="mobile = !mobile"
        >
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            aria-hidden="true"
          >
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </button>
        <form class="global-search" role="search" @submit.prevent="searchMedia">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.7"
            aria-hidden="true"
          >
            <path :d="icons.search" />
          </svg>
          <input
            ref="searchInput"
            v-model="globalQuery"
            aria-label="搜索电影、剧集"
            placeholder="搜索电影、剧集…"
          />
          <button type="submit" aria-label="搜索影视">搜索</button><kbd>⌘K</kbd>
        </form>
        <div class="topbar-actions">
          <span v-if="ui.busy" class="loading-dot" aria-label="正在加载"></span
          ><button
            class="small ghost"
            :aria-label="paused ? '恢复自动流程' : '暂停自动流程'"
            :title="paused ? '恢复自动流程' : '暂停自动流程'"
            @click="control"
          >
            <svg class="control-icon" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
              <path :d="paused ? 'M4 2l10 6-10 6V2Z' : 'M3 2h3v12H3zm7 0h3v12h-3z'" />
            </svg>
            <span class="control-label">{{
              paused ? '恢复自动流程' : '暂停自动流程'
            }}</span></button
          ><button
            class="bell-button"
            aria-label="任务通知"
            :aria-expanded="notificationsOpen"
            @click="
              notificationsOpen = !notificationsOpen;
              loadNotifications().catch(() => {});
            "
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M9 21h6" /></svg
            ><span v-if="notifications.unread">{{
              notifications.unread > 99 ? '99+' : notifications.unread
            }}</span></button
          ><button class="avatar" title="退出登录" aria-label="退出登录" @click="logout">A</button>
        </div>
      </header>
      <div
        v-if="ui.status?.logging?.error || ui.status?.logging?.status === 'failed'"
        class="service-alert"
      >
        日志输出异常：{{
          ui.status.logging.error || '无法写入日志，请检查数据目录权限与可用空间'
        }}。请及时处理，避免排障记录缺失。
      </div>
      <div
        v-if="notificationsOpen"
        class="notification-shade"
        @click="notificationsOpen = false"
      ></div>
      <section v-if="notificationsOpen" class="notification-panel">
        <div class="section-heading">
          <h3>
            后台任务通知 <span class="badge">{{ notifications.unread || 0 }} 条未读</span>
          </h3>
          <button class="icon-button" aria-label="关闭通知" @click="notificationsOpen = false">
            ×
          </button>
        </div>
        <article
          v-for="n in notifications.items || []"
          :key="n.id"
          :class="['notification-item', { unread: !n.read && !n.read_at }]"
        >
          <strong>{{ n.title || '后台任务失败' }}</strong>
          <p>{{ n.message || n.error_message }}</p>
          <small>{{ date(n.created_at || n.time) }}</small>
          <div class="button-row">
            <button v-if="n.job_id" class="small" @click="readNotification(n, true)">
              查看关联任务</button
            ><button v-if="!n.read && !n.read_at" class="small" @click="readNotification(n)">
              标记已读
            </button>
          </div>
        </article>
        <div v-if="!notifications.items?.length" class="empty-state">
          <h3>暂时没有后台失败通知</h3>
          <p>后台任务失败后会在这里持续保留，便于你回来处理。</p>
        </div>
      </section>
      <main>
        <Catalog
          v-if="['discover', 'search', 'detail', 'person'].includes(pageKey)"
          :key="route"
          :route="route"
        /><KeywordResources
          v-else-if="pageKey === 'resource-search'"
          :key="route"
          :route="route"
        /><Resources
          v-else-if="route.startsWith('resources/')"
          :key="route"
          :route="route"
        /><Subscriptions
          v-else-if="route.startsWith('subscriptions') || route === 'calendar'"
          :key="route"
          :view="route"
        /><SiteManager v-else-if="pageKey === 'sites'" />
        <DownloaderManager v-else-if="pageKey === 'downloaders'" />
        <DirectoryManager v-else-if="pageKey === 'directories'" @organize="organizePaths" />
        <FileManager
          v-else-if="pageKey === 'files' || pageKey === 'organize'"
          :key="route"
          :initial-paths="initialFilePaths"
          @history="go('history')"
          @job="(job: string) => go('jobs/' + encodeURIComponent(job))"
        />
        <TransferHistory
          v-else-if="pageKey === 'history'"
          @organize="organizePaths"
          @job="(job: string) => go('jobs/' + encodeURIComponent(job))"
        />
        <Settings v-else-if="route === 'settings'" /><Operations
          v-else
          :key="route"
          :view="route.split('/')[0]!"
          :initial-job="
            route.startsWith('jobs/') || route.startsWith('logs/') ? route.split('/')[1] : undefined
          "
        />
      </main>
      <footer>
        Light Media <span>{{ title }}</span>
      </footer>
    </div>
  </div>
  <div class="toasts" role="status">
    <button
      v-for="t in ui.toasts"
      :key="t.id"
      :class="['toast', { error: t.error }]"
      @click="ui.toasts = ui.toasts.filter((x) => x.id !== t.id)"
    >
      {{ t.error ? '!' : '✓' }} {{ t.text }}<span>×</span>
    </button>
  </div>
</template>
