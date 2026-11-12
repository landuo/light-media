<script setup lang="ts">
import { VTextField } from 'vuetify/components/VTextField';
import { ref, onMounted, onUnmounted, computed, watch, defineAsyncComponent } from 'vue';
import { api, action, ui, refreshSettings, refreshStatus, go, errorText, date } from './api';
import { createPluginNavigation, pluginNavigationTarget } from './features/plugin-navigation';
import { abortPluginAPIRequests } from './features/plugin-remote-api';
import { appInterface, compactAppInterface, observeInterfaceEnvironment } from './features/interface-mode';
import {
  createSystemMessageFeed,
  systemMessageIcon,
  systemMessageRelativeTime as formatSystemMessageRelativeTime,
  type SystemMessageState,
} from './features/system-messages';
import { VIcon } from 'vuetify/components/VIcon';
const Catalog = defineAsyncComponent(() => import('./components/Catalog.vue'));
const Explore = defineAsyncComponent(() => import('./components/Explore.vue'));
const ExploreDetail = defineAsyncComponent(() => import('./components/ExploreDetail.vue'));
const Recommend = defineAsyncComponent(() => import('./components/Recommend.vue'));
const RecommendBrowse = defineAsyncComponent(() => import('./components/RecommendBrowse.vue'));
const Resources = defineAsyncComponent(() => import('./components/Resources.vue'));
const DownloaderTasks = defineAsyncComponent(() => import('./components/DownloaderTasks.vue'));
const KeywordResources = defineAsyncComponent(() => import('./components/KeywordResources.vue'));
const Subscriptions = defineAsyncComponent(() => import('./components/Subscriptions.vue'));
const Operations = defineAsyncComponent(() => import('./components/Operations.vue'));
const Settings = defineAsyncComponent(() => import('./components/Settings.vue'));
const SiteManager = defineAsyncComponent(() => import('./components/SiteManager.vue'));
const FileManager = defineAsyncComponent(() => import('./components/FileManager.vue'));
const TransferHistory = defineAsyncComponent(() => import('./components/TransferHistory.vue'));
import BrandMark from './components/BrandMark.vue';
import AppIcon from './components/AppIcon.vue';
import { searchSession, startSearch, clearSearchSession } from './features/search-session';
const ShortcutTools = defineAsyncComponent(() => import('./components/ShortcutTools.vue'));
const Plugins = defineAsyncComponent(() => import('./components/Plugins.vue'));
const PluginAppPage = defineAsyncComponent(() => import('./components/PluginAppPage.vue'));
const PluginDashboard = defineAsyncComponent(() => import('./components/PluginDashboard.vue'));
const PluginQuickAccess = defineAsyncComponent(() => import('./components/PluginQuickAccess.vue'));
const PluginApplicationCenter = defineAsyncComponent(() => import('./components/PluginApplicationCenter.vue'));
const InterfaceModeMenu = defineAsyncComponent(() => import('./components/InterfaceModeMenu.vue'));
const AppModeFooter = defineAsyncComponent(() => import('./components/AppModeFooter.vue'));
const quickAccessOpen = ref(false);
const quickAccessPull = ref({ transform: 'translateY(0)', transition: 'none' });
const PluginAuthLogin = defineAsyncComponent(() => import('./components/PluginAuthLogin.vue'));
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
const mobileToggle = ref<HTMLButtonElement>();
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
    section: 'start',
    items: [['search', 'search', '搜索结果']],
  },
  {
    label: '发现',
    section: 'discovery',
    items: [
      ['discover', 'star', '推荐'],
      ['explore', 'compass', '探索'],
      ['dashboard', 'chart', '仪表板'],
    ],
  },
  {
    label: '订阅',
    section: 'subscribe',
    items: [
      ['subscriptions?type=movie', 'movie', '电影'],
      ['subscriptions?type=tv', 'tv', '电视剧'],
      ['calendar', 'calendar', '日历'],
    ],
  },
  {
    label: '整理',
    section: 'organize',
    items: [
      ['downloads', 'download', '下载管理'],
      ['history', 'history', '整理历史'],
      ['files', 'folder', '文件管理'],
    ],
  },
  {
    label: '系统',
    section: 'system',
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
  notificationsOpen = ref(false),
  notificationTab = ref<'system' | 'tasks'>('system'),
  notificationTime = ref(Date.now()),
  systemMessages = ref<SystemMessageState>({ items: [], hasNew: false });
const systemMessageFeed = createSystemMessageFeed((state) => {
  systemMessages.value = state;
});
// The reactive argument keeps the existing 15-second render trigger, but its
// cached value is not the wall clock after a long login page or background pause.
function systemMessageRelativeTime(date: string | null, _renderTick: number) {
  return formatSystemMessageRelativeTime(date);
}
let authGeneration = 0;
async function loadNotifications() {
  const generation = authGeneration;
  const result = await api('/notifications');
  if (generation === authGeneration && auth.value?.authenticated) notifications.value = result;
}
function readAllSystemMessages() {
  systemMessageFeed.markAllRead();
  notificationsOpen.value = false;
}
async function readNotification(n: any, jump = false) {
  const generation = authGeneration;
  const r = await action(() => api('/notifications/' + encodeURIComponent(n.id) + '/read', {}));
  if (generation !== authGeneration || !auth.value?.authenticated) return;
  if (r !== undefined) await loadNotifications();
  if (generation !== authGeneration || !auth.value?.authenticated) return;
  if (jump && n.job_id) {
    notificationsOpen.value = false;
    go('jobs/' + encodeURIComponent(n.job_id));
  }
}
function change() {
  abortPluginAPIRequests();
  route.value = normalizeRoute(location.hash.slice(1));
  if (searchComponent.value) searchSession.lastRoute = route.value;
  mobile.value = false;
  window.scrollTo(0, 0);
  if (pageKey.value !== 'files') initialFilePaths.value = [];
}
function goBack() { window.history.back(); }
function searchMedia(resource = false) {
  const query = globalQuery.value.trim();
  if (!query) return;
  searchMenuOpen.value = false;
  searchInput.value?.blur();
  startSearch((resource ? 'resource-search' : 'search') + '?query=' + encodeURIComponent(query));
}
function searchBlur(event: FocusEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null))
    searchMenuOpen.value = false;
}
function searchShortcut(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    notificationsOpen.value = false;
    if (mobile.value) {
      mobile.value = false;
      mobileToggle.value?.focus();
    }
  }
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
  abortPluginAPIRequests();
  authGeneration++;
  systemMessageFeed.setAuthenticated(false);
  notifications.value = { items: [], unread: 0 };
  notificationsOpen.value = false;
  notificationTab.value = 'system';
  starting.value = false;
  auth.value = { authenticated: false, setup_required: false };
  ui.settings = null;
  clearSearchSession();
  pluginNavigation.reset();
}
async function bootstrap() {
  const generation = authGeneration;
  try {
    const status = await api('/auth/status');
    if (generation !== authGeneration) return;
    auth.value = status;
    if (auth.value.authenticated) {
      await Promise.all([refreshSettings(), refreshStatus()]);
      if (generation !== authGeneration || !auth.value?.authenticated) return;
    } else {
      systemMessageFeed.setAuthenticated(false);
    }
  } catch (e) {
    if (generation === authGeneration) authError.value = errorText(e);
  } finally {
    if (generation === authGeneration) starting.value = false;
  }
}
const serviceStarting = computed(() => ui.status?.starting === true);
const businessReady = computed(() => !!auth.value?.authenticated && !starting.value && !!ui.status && !serviceStarting.value);
watch(businessReady, (ready) => {
  systemMessageFeed.setAuthenticated(ready);
  if (ready) {
    loadNotifications().catch(() => {});
    pluginNavigation.ensure();
  }
});
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
  // Stop consuming before the network request: a late frame cannot cross the
  // logout/login boundary, even if the logout response is delayed or fails.
  expired();
  await action(() => api('/auth/logout', {}));
}
let timer: number;
let stopInterfaceMode = () => {};
onMounted(() => {
  stopInterfaceMode = observeInterfaceEnvironment();
  change();
  window.addEventListener('hashchange', change);
  window.addEventListener('session-expired', expired);
  window.addEventListener('keydown', searchShortcut);
  bootstrap();
  timer = window.setInterval(() => {
    if (auth.value?.authenticated && !document.hidden) {
      notificationTime.value = Date.now();
      refreshStatus().catch(() => {});
      if (businessReady.value) loadNotifications().catch(() => {});
    }
  }, 15000);
});
onUnmounted(() => {
  stopInterfaceMode();
  authGeneration++;
  systemMessageFeed.destroy();
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
      <VTextField
        v-model="username"
        autocomplete="username"
        required
        maxlength="64"
        label="用户名"
      /><VTextField
        v-model="password"
        type="password"
        :autocomplete="auth?.setup_required ? 'new-password' : 'current-password'"
        required
        :minlength="auth?.setup_required ? 12 : 1"
        label="密码"
      />
      <p v-if="auth?.setup_required" class="hint">管理员密码至少 12 位。</p>
      <p v-if="authError" class="error-box">{{ authError }}</p>
      <button class="primary full" :disabled="ui.busy > 0">
        {{ auth?.setup_required ? '创建并开始' : '登录' }}
      </button>
      <PluginAuthLogin v-if="!auth?.setup_required" @authenticated="bootstrap" />
    </form>
  </div>
  <div v-else :class="['app-shell', { 'app-interface': appInterface, 'drawer-open': mobile && !appInterface }]">
    <div v-if="mobile && !appInterface" class="sidebar-shade" @click="mobile = false"></div>
    <aside v-if="!appInterface" id="main-navigation" :class="['sidebar', { open: mobile }]">
      <a class="brand" href="#discover" @click="mobile = false"
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
            @click="mobile = false"
            ><span class="nav-icon"><AppIcon :name="n[1]!" /></span>{{ n[2]
            }}<span v-if="n[0] === 'jobs' && ui.status?.counts?.active_jobs" class="counter">{{
              ui.status.counts.active_jobs
            }}</span></a
          >
          <a
            v-for="item in pluginNavItems.items.filter((item) => item.section === group.section)"
            :key="item.instance_id + ':' + item.nav_key"
            :href="'#' + pluginNavigationTarget(item)"
            :class="{ active: route === pluginNavigationTarget(item) }"
            :aria-current="route === pluginNavigationTarget(item) ? 'page' : undefined"
            @click="mobile = false"
          >
            <span class="nav-icon"><VIcon :icon="item.icon" size="22" /></span>{{ item.title }}
          </a>
        </template>
      </nav>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <button
          v-if="!appInterface"
          ref="mobileToggle"
          class="icon-button mobile-toggle"
          :aria-label="mobile ? '关闭导航' : '打开导航'"
          :aria-expanded="mobile"
          aria-controls="main-navigation"
          @click="mobile = !mobile"
        >
          <AppIcon name="menu" />
        </button>
        <button v-else class="icon-button" aria-label="返回" @click="goBack"><VIcon icon="mdi-arrow-left" /></button>
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
            :placeholder="compactAppInterface ? '搜索影视…' : '搜索电影、剧集…'"
            enterkeyhint="search"
            @focus="searchMenuOpen = true"
            @input="searchMenuOpen = true"
          />
          <kbd>⌘K</kbd>
          <div v-if="searchMenuOpen && globalQuery.trim()" class="global-search-menu">
            <button type="button" @pointerdown.prevent @click="searchMedia()">
              <AppIcon name="movie" /><span
                ><strong>电影、电视剧</strong><small>搜索 {{ globalQuery }} 标题</small></span
              >
            </button>
            <button type="button" @pointerdown.prevent @click="searchMedia(true)">
              <AppIcon name="search" /><span
                ><strong>在站点中搜索种子资源</strong
                ><small>搜索 {{ globalQuery }} 相关资源</small></span
              >
            </button>
          </div>
        </form>
        <div class="topbar-actions">
          <InterfaceModeMenu v-if="!compactAppInterface" />
          <ShortcutTools v-if="businessReady" />
          <span v-if="ui.busy" class="loading-dot" aria-label="正在加载"></span
          ><button
            class="bell-button"
            aria-label="通知中心"
            :aria-expanded="notificationsOpen"
            @click="
              notificationsOpen = !notificationsOpen;
              loadNotifications().catch(() => {});
            "
          >
            <AppIcon name="bell" /><span
              v-if="systemMessages.hasNew || notifications.unread"
              class="notification-unread-dot"
              aria-label="有未读通知"
            ></span></button
          ><button v-if="!compactAppInterface" class="icon-button" title="退出登录" aria-label="退出登录" @click="logout">
            <AppIcon name="logout" />
          </button>
        </div>
      </header>
      <div id="explore-header" v-show="pageKey === 'explore'" class="explore-tabs-host"></div>
      <div id="recommend-header" v-show="pageKey === 'discover'" class="explore-tabs-host"></div>
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
      <section
        v-if="notificationsOpen"
        class="notification-panel"
        role="dialog"
        aria-label="通知中心"
      >
        <div class="section-heading">
          <h3>通知中心</h3>
          <div class="button-row">
            <button
              v-if="notificationTab === 'system'"
              class="icon-button"
              title="全部已读"
              aria-label="系统消息全部已读"
              @click="readAllSystemMessages"
            >
              <VIcon icon="mdi-email-check-outline" size="20" />
            </button>
            <button class="icon-button" aria-label="关闭通知" @click="notificationsOpen = false">
              <AppIcon name="close" />
            </button>
          </div>
        </div>
        <div class="notification-tabs" role="tablist" aria-label="通知类型">
          <button
            id="system-message-tab"
            role="tab"
            :aria-selected="notificationTab === 'system'"
            aria-controls="system-message-list"
            @click="notificationTab = 'system'"
          >
            系统消息<span
              v-if="systemMessages.hasNew"
              class="tab-unread-dot"
              aria-label="有未读系统消息"
            ></span>
          </button>
          <button
            id="task-notification-tab"
            role="tab"
            :aria-selected="notificationTab === 'tasks'"
            aria-controls="task-notification-list"
            @click="notificationTab = 'tasks'"
          >
            后台任务<span v-if="notifications.unread" class="badge">{{
              notifications.unread
            }}</span>
          </button>
        </div>
        <div
          v-if="notificationTab === 'system'"
          id="system-message-list"
          class="notification-list-container"
          role="tabpanel"
          aria-labelledby="system-message-tab"
        >
          <article
            v-for="n in systemMessages.items"
            :key="n.id"
            class="system-message-item"
            @click="notificationsOpen = false"
          >
            <span class="system-message-icon"
              ><VIcon :icon="systemMessageIcon(n.type)" size="28"
            /></span>
            <div class="system-message-content">
              <strong>{{ n.title }}</strong>
              <p>{{ n.text }}</p>
              <small>{{ systemMessageRelativeTime(n.date, notificationTime) }}</small>
            </div>
          </article>
          <div v-if="!systemMessages.items.length" class="empty-state">
            <VIcon icon="mdi-bell-sleep-outline" size="40" />
            <p>暂无通知</p>
          </div>
        </div>
        <div
          v-else
          id="task-notification-list"
          class="notification-list-container"
          role="tabpanel"
          aria-labelledby="task-notification-tab"
        >
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
        </div>
      </section>
      <main>
        <div v-if="serviceStarting" class="service-alert" role="status">
          服务正在初始化，完成后将自动载入。你可以先查看日志。
          <button v-if="pageKey !== 'logs'" @click="go('logs')">查看日志</button>
        </div>
        <Operations v-if="serviceStarting && pageKey === 'logs'" view="logs" />
        <template v-if="!serviceStarting">
        <PluginQuickAccess v-if="pageKey === 'dashboard' && appInterface" @visibility="quickAccessOpen = $event" @pull="quickAccessPull = $event" />
        <div v-if="pageKey === 'apps' && compactAppInterface" class="mobile-account-actions">
          <InterfaceModeMenu labeled />
          <button type="button" class="mobile-logout" @click="logout">
            <AppIcon name="logout" />退出登录
          </button>
        </div>
        <PluginApplicationCenter v-if="pageKey === 'apps'" :navigation="nav" :plugins="pluginNavItems.items" :error="pluginNavItems.error" :search-route="searchSession.lastRoute" @retry="pluginNavigation.ensure(true)" />
        <KeepAlive>
          <Recommend v-if="pageKey === 'discover'" />
        </KeepAlive>
        <KeepAlive :max="15">
          <RecommendBrowse v-if="pageKey === 'recommend-browse'" :key="route" :route="route" />
        </KeepAlive>
        <KeepAlive>
          <Explore v-if="pageKey === 'explore'" />
        </KeepAlive>
        <KeepAlive>
          <Plugins v-if="pageKey === 'plugins'" @changed="pluginNavigation.ensure(true)" />
        </KeepAlive>
        <div :style="quickAccessPull">
          <KeepAlive>
            <PluginDashboard v-if="pageKey === 'dashboard'" />
          </KeepAlive>
        </div>
        <ExploreDetail v-if="pageKey === 'explore-detail'" :key="route" :route="route" />
        <KeepAlive :max="24">
          <component :is="searchComponent" v-if="searchComponent" :key="route" :route="route" />
        </KeepAlive>
        <Catalog
          v-if="['detail', 'person'].includes(pageKey)"
          :key="route"
          :route="route"
        /><Subscriptions
          v-else-if="route.startsWith('subscriptions') || route === 'calendar'"
          :key="route"
          :view="route"
        /><SiteManager v-else-if="pageKey === 'sites'" />
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
          v-else-if="!searchComponent && !['discover', 'recommend-browse', 'explore', 'explore-detail', 'apps', 'plugins', 'dashboard'].includes(pageKey)"
          :key="route"
          :view="route.split('/')[0]!"
          :initial-job="
            route.startsWith('jobs/') || route.startsWith('logs/') ? route.split('/')[1] : undefined
          "
        />
        </template>
      </main>
    </div>
    <AppModeFooter v-if="appInterface && !quickAccessOpen" :route="route" />
  </div>
  <div :class="['toasts', { 'above-app-footer': auth?.authenticated && appInterface && !quickAccessOpen }]" role="status">
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
.app-interface .main-shell { margin-left: 0; }
.mobile-account-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px;
  margin: 0 0 8px;
  padding: 4px;
  border-radius: var(--radius);
  background: var(--surface);
}
.mobile-account-actions :deep(.v-btn),
.mobile-logout { min-height: 44px; }
.mobile-logout { border: 0; background: transparent; }
.mobile-logout svg { width: 20px; height: 20px; }
.explore-tabs-host {
  position: sticky;
  top: var(--topbar-height);
  z-index: 34;
  min-height: 48px;
  padding: 0 8px;
  background: rgba(244, 245, 250, 0.98);
}
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
  white-space: normal;
  border: 0;
  border-radius: 6px;
  background: transparent;
}
.global-search-menu button:hover {
  background: var(--accent-soft);
}
.global-search-menu span {
  display: grid;
  min-width: 0;
  gap: 4px;
  overflow-wrap: anywhere;
}
.global-search-menu small {
  color: var(--muted);
}
.global-search-menu svg {
  flex: none;
  width: 24px;
  height: 24px;
}
@media (max-width: 600px) {
  .global-search { position: static; }
  .global-search-menu {
    inset-inline: 8px;
    top: 100%;
    width: auto;
    max-height: min(320px, calc(100dvh - var(--topbar-height) - 8px));
    overflow-y: auto;
  }
}
</style>
