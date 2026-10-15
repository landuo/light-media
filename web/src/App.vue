<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue';
import { api, action, ui, refreshSettings, refreshStatus, go, toast, errorText, date } from './api';
import Catalog from './components/Catalog.vue';
import Resources from './components/Resources.vue';
import Subscriptions from './components/Subscriptions.vue';
import Operations from './components/Operations.vue';
import Settings from './components/Settings.vue';
const route = ref(location.hash.slice(1) || 'discover'),
  auth = ref<any>(null),
  username = ref(''),
  password = ref(''),
  authError = ref(''),
  starting = ref(true),
  mobile = ref(false);
const nav = [
  ['discover', '◈', '发现影视'],
  ['subscriptions', '◉', '我的订阅'],
  ['calendar', '▦', '追剧日历'],
  ['downloads', '↓', '下载任务'],
  ['organize', '⇄', '手动整理'],
  ['library', '▤', '媒体库'],
  ['jobs', '☷', '任务队列'],
  ['logs', '⌁', '日志中心'],
  ['settings', '⚙', '设置'],
];
const title = computed(
  () =>
    nav.find((n) => n[0] === route.value.split('/')[0])?.[2] ||
    ({ detail: '影视详情', person: '演员资料', resources: '搜索资源' } as any)[
      route.value.split('/')[0]!
    ] ||
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
});
</script>
<template>
  <div v-if="starting" class="auth-shell">
    <div class="auth-card">
      <div class="brand-symbol">◈</div>
      <h1>光影</h1>
      <p class="muted">正在连接你的媒体空间…</p>
    </div>
  </div>
  <div v-else-if="!auth?.authenticated" class="auth-shell">
    <form class="auth-card" @submit.prevent="login">
      <div class="brand-symbol">◈</div>
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
        ><span class="brand-mark">◈</span><span>光影<small>LIGHT MEDIA</small></span></a
      >
      <div class="nav-caption">你的媒体空间</div>
      <nav>
        <a
          v-for="n in nav"
          :key="n[0]"
          :href="'#' + n[0]"
          :class="{ active: route.split('/')[0] === n[0] }"
          ><span class="nav-icon">{{ n[1] }}</span
          >{{ n[2]
          }}<span v-if="n[0] === 'jobs' && ui.status?.counts?.active_jobs" class="counter">{{
            ui.status.counts.active_jobs
          }}</span></a
        >
      </nav>
      <div class="sidebar-bottom">
        <span class="dot" :class="{ amber: paused }"></span
        >{{ paused ? '自动流程已暂停' : ui.status ? '服务运行中' : '服务状态待确认'
        }}<small>轻量，自在，井然有序</small>
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
          ☰
        </button>
        <div class="breadcrumb">
          我的媒体空间 <span>/</span> <strong>{{ title }}</strong>
        </div>
        <div class="topbar-actions">
          <span v-if="ui.busy" class="loading-dot" aria-label="正在加载"></span
          ><button class="small ghost" @click="control">
            {{ paused ? '▶ 恢复自动流程' : 'Ⅱ 暂停自动流程' }}</button
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
          ><button class="avatar" title="退出登录" aria-label="退出登录" @click="logout">退</button>
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
          v-if="['discover', 'detail', 'person'].includes(route.split('/')[0]!)"
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
        /><Settings v-else-if="route === 'settings'" /><Operations
          v-else
          :key="route"
          :view="route.split('/')[0]!"
          :initial-job="
            route.startsWith('jobs/') || route.startsWith('logs/') ? route.split('/')[1] : undefined
          "
        />
      </main>
      <footer>Light Media · Go + Vue <span>所有任务状态均来自当前服务</span></footer>
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
