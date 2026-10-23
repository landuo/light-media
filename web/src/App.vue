<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, defineAsyncComponent } from 'vue';
import { api, action, ui, refreshSettings, refreshStatus, go, errorText, date } from './api';
import { createPluginNavigation, pluginNavigationTarget } from './features/plugin-navigation';
import { VIcon } from 'vuetify/components/VIcon';
import Catalog from './components/Catalog.vue';
import Resources from './components/Resources.vue';
import DownloaderTasks from './components/DownloaderTasks.vue';
import KeywordResources from './components/KeywordResources.vue';
import Subscriptions from './components/Subscriptions.vue';
import Operations from './components/Operations.vue';
import Settings from './components/Settings.vue';
import SiteManager from './components/SiteManager.vue';
import FileManager from './components/FileManager.vue';
import TransferHistory from './components/TransferHistory.vue';
import BrandMark from './components/BrandMark.vue';
import AppIcon from './components/AppIcon.vue';
import { searchSession, startSearch, clearSearchSession } from './features/search-session';
const ShortcutTools = defineAsyncComponent(() => import('./components/ShortcutTools.vue'));
const Plugins = defineAsyncComponent(() => import('./components/Plugins.vue'));
const PluginAppPage = defineAsyncComponent(() => import('./components/PluginAppPage.vue'));
const PluginDashboard = defineAsyncComponent(() => import('./components/PluginDashboard.vue'));
const pluginNavigation = createPluginNavigation(() => api('/plugins/navigation'));
const pluginNavItems = pluginNavigation.state;
function normalizeRoute(value: string) {
  if (['library', 'assets'].includes(value.split('?')[0]!.split('/')[0]!)) return 'files';
  if (value.split('?')[0] === 'downloaders') return 'settings?tab=system';
  if (value.split('?')[0] === 'directories') return 'settings?tab=directory';
  return value || 'discover';
}
const route = ref(normalizeRoute(location.hash.slice(1))),
  auth = ref<any>(null),
  username = ref(''),
  password = ref(''),
  authError = ref(''),
  starting = ref(true),
  mobile = ref(false);
const globalQuery = ref(''),
  searchMenuOpen = ref(false),
  searchInput = ref<HTMLInputElement>(),
  initialFilePaths = ref<string[]>([]);
const pageKey = computed(() => route.value.split('?')[0]!.split('/')[0]!);
const searchComponent = computed(() =>
  pageKey.value === 'search'
    ? Catalog
    : pageKey.value === 'resource-search'
      ? KeywordResources
      : pageKey.value === 'resources'
        ? Resources
        : null,
);
const nav = [
  {
    label: '',
    items: [['search', 'search', '搜索结果']],
  },
  {
    label: '发现',
    items: [
      ['discover', 'star', '推荐'],
      ['dashboard', 'chart', '仪表板'],
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
    ],
  },
  {
    label: '系统',
    items: [
      ['sites', 'globe', '站点管理'],
      ['plugins', 'plugin', '插件'],
      ['jobs', 'jobs', '任务队列'],
      ['logs', 'logs', '日志'],
      ['settings', 'settings', '设置'],
    ],
  },
];
const activeNav = (target: string) =>
  target.includes('?')
    ? route.value.startsWith(target)
    : pageKey.value === target || (target === 'search' && !!searchComponent.value);
const navHref = (target: string) => '#' + (target === 'search' ? searchSession.lastRoute : target);
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
  route.value = normalizeRoute(location.hash.slice(1));
  if (searchComponent.value) searchSession.lastRoute = route.value;
  mobile.value = false;
  window.scrollTo(0, 0);
  if (pageKey.value !== 'files') initialFilePaths.value = [];
}
function searchMedia(resource = false) {
  if (!globalQuery.value.trim()) return;
  searchMenuOpen.value = false;
  startSearch(
    (resource ? 'resource-search' : 'search') +
      '?query=' +
      encodeURIComponent(globalQuery.value.trim()),
  );
}
function searchBlur(event: FocusEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null))
    searchMenuOpen.value = false;
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
  clearSearchSession();
  pluginNavigation.reset();
}
async function bootstrap() {
  try {
    auth.value = await api('/auth/status');
    if (auth.value.authenticated) {
      await Promise.all([refreshSettings(), refreshStatus()]);
      loadNotifications().catch(() => {});
      pluginNavigation.ensure();
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
let timer: number;
onMounted(() => {
  change();
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
            v-for="n in group.items"
            :key="n[0]"
            :href="navHref(n[0]!)"
            :class="{ active: activeNav(n[0]!) }"
            :aria-current="activeNav(n[0]!) ? 'page' : undefined"
            ><span class="nav-icon"><AppIcon :name="n[1]!" /></span>{{ n[2]
            }}<span v-if="n[0] === 'jobs' && ui.status?.counts?.active_jobs" class="counter">{{
              ui.status.counts.active_jobs
            }}</span></a
          >
          <template v-if="group.label === '整理'">
            <a
              v-for="item in pluginNavItems.items.filter((item) => item.section === 'organize')"
              :key="item.instance_id + ':' + item.nav_key"
              :href="'#' + pluginNavigationTarget(item)"
              :class="{ active: route === pluginNavigationTarget(item) }"
              :aria-current="route === pluginNavigationTarget(item) ? 'page' : undefined"
            >
              <span class="nav-icon"><VIcon :icon="item.icon" size="22" /></span>{{ item.title }}
            </a>
          </template>
        </template>
      </nav>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <button
          class="icon-button mobile-toggle"
          aria-label="打开导航"
          :aria-expanded="mobile"
          @click="mobile = !mobile"
        >
          <AppIcon name="menu" />
        </button>
        <form
          class="global-search"
          role="search"
          @submit.prevent="searchMedia()"
          @focusout="searchBlur"
          @keydown.esc="searchMenuOpen = false"
        >
          <button type="submit" class="global-search-submit" aria-label="搜索影视">
            <AppIcon name="search" />
          </button>
          <input
            ref="searchInput"
            v-model="globalQuery"
            aria-label="搜索电影、剧集"
            placeholder="搜索电影、剧集…"
            @focus="searchMenuOpen = true"
            @input="searchMenuOpen = true"
          />
          <kbd>⌘K</kbd>
          <div v-if="searchMenuOpen && globalQuery.trim()" class="global-search-menu">
            <button type="button" @click="searchMedia()">
              <AppIcon name="movie" /><span
                ><strong>电影、电视剧</strong><small>搜索 {{ globalQuery }} 标题</small></span
              >
            </button>
            <button type="button" @click="searchMedia(true)">
              <AppIcon name="search" /><span
                ><strong>在站点中搜索种子资源</strong
                ><small>搜索 {{ globalQuery }} 相关资源</small></span
              >
            </button>
          </div>
        </form>
        <div class="topbar-actions">
          <ShortcutTools />
          <span v-if="ui.busy" class="loading-dot" aria-label="正在加载"></span
          ><button
            class="bell-button"
            aria-label="任务通知"
            :aria-expanded="notificationsOpen"
            @click="
              notificationsOpen = !notificationsOpen;
              loadNotifications().catch(() => {});
            "
          >
            <AppIcon name="bell" /><span v-if="notifications.unread">{{
              notifications.unread > 99 ? '99+' : notifications.unread
            }}</span></button
          ><button class="icon-button" title="退出登录" aria-label="退出登录" @click="logout">
            <AppIcon name="logout" />
          </button>
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
            <AppIcon name="close" />
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
        <KeepAlive :max="24">
          <component :is="searchComponent" v-if="searchComponent" :key="route" :route="route" />
        </KeepAlive>
        <Catalog
          v-if="['discover', 'detail', 'person'].includes(pageKey)"
          :key="route"
          :route="route"
        /><Subscriptions
          v-else-if="route.startsWith('subscriptions') || route === 'calendar'"
          :key="route"
          :view="route"
        /><SiteManager v-else-if="pageKey === 'sites'" />
        <Plugins v-else-if="pageKey === 'plugins'" @changed="pluginNavigation.ensure(true)" />
        <PluginDashboard v-else-if="pageKey === 'dashboard'" />
        <PluginAppPage
          v-else-if="pageKey === 'plugin-app'"
          :key="route"
          :route="route"
          @changed="pluginNavigation.ensure(true)"
        />
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
        <Settings v-else-if="pageKey === 'settings'" /><DownloaderTasks
          v-else-if="pageKey === 'downloads'"
        /><Operations
          v-else-if="!searchComponent"
          :key="route"
          :view="route.split('/')[0]!"
          :initial-job="
            route.startsWith('jobs/') || route.startsWith('logs/') ? route.split('/')[1] : undefined
          "
        />
      </main>
    </div>
  </div>
  <div class="toasts" role="status">
    <button
      v-for="t in ui.toasts"
      :key="t.id"
      :class="['toast', { error: t.error }]"
      @click="ui.toasts = ui.toasts.filter((x) => x.id !== t.id)"
    >
      <AppIcon :name="t.error ? 'warning' : 'check-circle'" /><span class="toast-message">{{
        t.text
      }}</span
      ><AppIcon name="close" class="toast-close" />
    </button>
  </div>
</template>
<style scoped>
.global-search {
  position: relative;
}
.global-search-menu {
  position: absolute;
  top: calc(100% + 8px);
  left: 0;
  width: min(420px, 85vw);
  padding: 8px;
  background: var(--surface);
  border-radius: 8px;
  box-shadow: var(--shadow);
  border: 1px solid var(--line);
  z-index: 20;
}
.global-search-menu button {
  display: flex;
  align-items: center;
  gap: 16px;
  width: 100%;
  min-height: 64px;
  height: auto;
  padding: 12px 16px;
  text-align: left;
  border: 0;
  border-radius: 6px;
  background: transparent;
}
.global-search-menu button:hover {
  background: var(--accent-soft);
}
.global-search-menu span {
  display: grid;
  gap: 4px;
}
.global-search-menu small {
  color: var(--muted);
}
.global-search-menu svg {
  flex: none;
  width: 24px;
  height: 24px;
}
</style>
