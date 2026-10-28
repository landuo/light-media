<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VDivider } from 'vuetify/components/VDivider';
import { VTextField } from 'vuetify/components/VTextField';
import { VInfiniteScroll } from 'vuetify/components/VInfiniteScroll';
import { VAlert } from 'vuetify/components/VAlert';
import { VImg } from 'vuetify/components/VImg';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { api, errorText } from '../api';
import { systemMessageRelativeTime } from '../features/system-messages';
import {
  pluginWebMessages,
  pluginMessageKey,
  pluginMessageTime,
  pluginMessageCompareTime,
  pluginMessageNoteItems,
  pluginMessageHasNote,
  renderPluginWebMessage,
  type PluginWebMessage,
} from '../features/plugin-web-message';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';

const props = withDefaults(defineProps<{ modelValue?: boolean }>(), { modelValue: true });
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; close: [] }>();
const display = useDisplay();
const visible = computed({
  get: () => props.modelValue,
  set: (value) => {
    emit('update:modelValue', value);
    if (!value) emit('close');
  },
});
const user_message = ref(''),
  sendButtonDisabled = ref(false),
  error = ref('');
const messages = ref<PluginWebMessage[]>([]),
  historyError = ref(''),
  loading = ref(false),
  isLoaded = ref(false),
  page = ref(1),
  lastTime = ref('');
const list = ref<any>(null),
  keys = new Set<string>();
let active = true;
let scrollTimer: number | undefined, scrollReleaseTimer: number | undefined;
let boundScrollContainer: HTMLElement | null = null;
const shouldAutoScroll = ref(true),
  isSyncingScroll = ref(false);
const MESSAGE_AUTO_SCROLL_THRESHOLD = 64;
/** 判断元素自身是否是真正承载滚动的位置。 */
function isScrollableElement(element: HTMLElement) {
  const { overflowY } = window.getComputedStyle(element);
  const canScroll = overflowY === 'auto' || overflowY === 'scroll' || overflowY === 'overlay';

  return canScroll && element.scrollHeight > element.clientHeight + 1;
}

/** 获取消息列表所在的真实滚动容器。 */
function getScrollContainer() {
  const element = list.value?.$el ?? list.value;

  if (!(element instanceof HTMLElement)) {
    return null;
  }

  let container: HTMLElement | null = element;
  while (container) {
    if (isScrollableElement(container)) {
      return container;
    }

    container = container.parentElement;
  }

  const dialogCardText = element.closest('.v-card-text');

  return dialogCardText instanceof HTMLElement ? dialogCardText : element;
}

function isNearBottom(container: HTMLElement) {
  const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight;

  return distanceFromBottom <= Math.max(MESSAGE_AUTO_SCROLL_THRESHOLD, container.clientHeight / 3);
}

function updateAutoScrollState() {
  const container = getScrollContainer();
  if (!container || isSyncingScroll.value) {
    return;
  }

  shouldAutoScroll.value = isNearBottom(container);
}

function handleScroll() {
  updateAutoScrollState();
}

function bindScrollListener() {
  const container = getScrollContainer();
  if (!container) {
    return;
  }

  if (boundScrollContainer && boundScrollContainer !== container) {
    boundScrollContainer.removeEventListener('scroll', handleScroll);
  }

  container.removeEventListener('scroll', handleScroll);
  container.addEventListener('scroll', handleScroll, { passive: true });
  boundScrollContainer = container;
  updateAutoScrollState();
}

function unbindScrollListener() {
  boundScrollContainer?.removeEventListener('scroll', handleScroll);
  boundScrollContainer = null;
}

/** 滚动到底部，并在布局稳定前连续几帧校正滚动位置。 */
function scrollContainerToEnd(retryCount = 1) {
  const container = getScrollContainer();
  if (!container) {
    return;
  }

  bindScrollListener();
  isSyncingScroll.value = true;
  container.scrollTop = Math.max(0, container.scrollHeight - container.clientHeight);

  requestAnimationFrame(() => {
    const latestContainer = getScrollContainer();
    if (!latestContainer) {
      isSyncingScroll.value = false;
      return;
    }

    latestContainer.scrollTop = Math.max(
      0,
      latestContainer.scrollHeight - latestContainer.clientHeight,
    );
    shouldAutoScroll.value = true;

    if (retryCount > 0) {
      scrollContainerToEnd(retryCount - 1);
      return;
    }

    if (scrollReleaseTimer) {
      window.clearTimeout(scrollReleaseTimer);
    }

    scrollReleaseTimer = window.setTimeout(() => {
      isSyncingScroll.value = false;
      updateAutoScrollState();
    }, 80);
  });
}

function requestScrollToEnd(force = false) {
  if (!force && !shouldAutoScroll.value) {
    return;
  }

  if (scrollTimer) {
    window.clearTimeout(scrollTimer);
  }

  scrollTimer = window.setTimeout(
    () => {
      nextTick(() => {
        requestAnimationFrame(() => {
          scrollContainerToEnd(force ? 6 : 1);
        });
      });
    },
    force ? 0 : 80,
  );
}

function forceScrollToEnd() {
  requestScrollToEnd(true);
}

function openMessageLink(link: string) {
  window.open(link, '_blank');
}
function mergeMessages(value: unknown) {
  let changed = false;
  for (const item of pluginWebMessages(value)) {
    const key = pluginMessageKey(item);
    if (keys.has(key)) continue;
    keys.add(key);
    messages.value.push(item);
    changed = true;
    const date = pluginMessageTime(item);
    if (date && pluginMessageCompareTime(date, lastTime.value) > 0) lastTime.value = date;
  }
  if (changed) messages.value = pluginWebMessages(messages.value);
  return changed;
}
async function loadMessages({ done }: { done: (status: 'ok' | 'empty' | 'error') => void }) {
  if (loading.value) {
    done('ok');
    return;
  }
  loading.value = true;
  const first = page.value === 1;
  try {
    const rows = await api('/message/web?page=' + page.value + '&size=20');
    if (!active) return;
    const values = pluginWebMessages(rows);
    isLoaded.value = true;
    historyError.value = '';
    if (values.length) {
      mergeMessages(values);
      page.value++;
      done('ok');
      if (first) requestScrollToEnd(true);
    } else done('empty');
  } catch (e) {
    if (active) {
      historyError.value = errorText(e);
      done('error');
    }
  } finally {
    if (active) loading.value = false;
  }
}
async function refreshLatestMessages() {
  try {
    const rows = await api('/message/web?page=1&size=20');
    if (!active) return;
    if (mergeMessages(rows)) requestScrollToEnd();
  } catch (e) {
    if (active) historyError.value = errorText(e);
  }
}
async function sendMessage() {
  const messageText = user_message.value.trim();
  if (!messageText) return;
  try {
    sendButtonDisabled.value = true;
    error.value = '';
    await api('/message/web?text=' + encodeURIComponent(messageText), undefined, 'POST');
    if (!active) return;
    user_message.value = '';
    forceScrollToEnd();
  } catch (e) {
    if (active) error.value = errorText(e);
  } finally {
    if (active) sendButtonDisabled.value = false;
  }
}
watch(visible, (value) => {
  if (value) void refreshLatestMessages();
});
onMounted(() => {
  nextTick(bindScrollListener);
});
onUnmounted(() => {
  active = false;
  window.clearTimeout(scrollTimer);
  window.clearTimeout(scrollReleaseTimer);
  unbindScrollListener();
});
</script>
<template>
  <VDialog
    v-if="visible"
    v-model="visible"
    max-width="50rem"
    scrollable
    :fullscreen="!display.mdAndUp.value"
    content-class="plugin-dialog-surface shortcut-message-dialog"
  >
    <VCard>
      <VCardItem class="shortcut-message-heading">
        <VCardTitle><VIcon icon="mdi-message" class="me-2" />消息中心</VCardTitle>
        <PluginDialogCloseBtn :model-value="visible" @update:model-value="visible = $event" />
      </VCardItem>
      <VDivider />
      <VCardText class="shortcut-message-list">
        <VAlert v-if="historyError" type="error" variant="tonal" class="mb-3">{{
          historyError
        }}</VAlert>
        <VInfiniteScroll
          ref="list"
          :mode="!isLoaded ? 'intersect' : 'manual'"
          side="start"
          :items="messages"
          class="shortcut-message-scroll"
          load-more-text="加载更多 ..."
          @load="loadMessages"
        >
          <template #empty>没有更多数据</template>
          <article
            v-for="item in messages"
            :key="pluginMessageKey(item)"
            class="shortcut-message-entry"
            :class="{ 'is-outgoing': item.action === 0 }"
          >
            <VCard
              variant="tonal"
              class="shortcut-message-card"
              @click="item.link && openMessageLink(item.link)"
            >
              <VImg
                v-if="item.image"
                :src="item.image"
                aspect-ratio="3/2"
                cover
                position="top"
                min-height="10rem"
                @load="requestScrollToEnd()"
              >
                <template #placeholder><VSkeletonLoader class="w-100 h-100" /></template>
              </VImg>
              <VCardTitle
                v-if="item.title"
                :class="{
                  'message-outgoing-title':
                    item.action === 0 &&
                    !item.text &&
                    !item.image &&
                    !pluginMessageHasNote(item.note),
                }"
                >{{ item.title }}</VCardTitle
              >
              <VCardText
                v-if="item.text && (item.action === 0 || item.action === 1)"
                :class="{ 'message-outgoing-text': item.action === 0 }"
                class="markdown-body"
                v-html="renderPluginWebMessage(item.text)"
              />
              <VCardText v-if="pluginMessageHasNote(item.note)">
                <VList>
                  <VListItem v-for="(value, key) in pluginMessageNoteItems(item.note)" :key="key">
                    <VListItemTitle v-if="value.title_year" class="font-weight-bold text-wrap"
                      >{{ Number(key) + 1 }}. {{ value.title_year }}</VListItemTitle
                    >
                    <VListItemTitle v-if="value.enclosure" class="font-weight-bold text-wrap"
                      >{{ Number(key) + 1 }}. {{ value.title }} {{ value.volume_factor }} ↑{{
                        value.seeders
                      }}</VListItemTitle
                    >
                    <VListItemSubtitle v-if="value.type"
                      >类型：{{ value.type }} 评分：{{ value.vote_average }}</VListItemSubtitle
                    >
                    <VListItemSubtitle v-if="value.enclosure" class="text-wrap">{{
                      value.description
                    }}</VListItemSubtitle>
                  </VListItem>
                </VList>
              </VCardText>
            </VCard>
            <time
              ><span v-if="item.action === 0">{{ item.userid }} </span
              >{{ systemMessageRelativeTime(pluginMessageTime(item)) }}</time
            >
          </article>
        </VInfiniteScroll>
      </VCardText>
      <VDivider />
      <VCardActions class="shortcut-message-actions">
        <div class="w-100">
          <VAlert v-if="error" type="error" variant="tonal" class="mb-3">{{ error }}</VAlert>
          <div class="d-flex w-100 ga-2">
            <VTextField
              v-model="user_message"
              variant="outlined"
              hide-details
              density="compact"
              placeholder="输入消息或命令"
              aria-label="输入消息或命令"
              @keyup.enter="sendMessage"
            />
            <VBtn
              variant="elevated"
              :disabled="sendButtonDisabled"
              :loading="sendButtonDisabled"
              color="primary"
              prepend-icon="mdi-send"
              @click="sendMessage"
              >发送</VBtn
            >
          </div>
        </div>
      </VCardActions>
    </VCard>
  </VDialog>
</template>
<style scoped>
.shortcut-message-heading {
  padding: 16px 20px;
  position: relative;
}
.shortcut-message-list {
  min-height: 240px;
  height: min(55vh, 560px);
  overflow-y: auto;
}
.shortcut-message-entry {
  margin: 20px 0 32px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
}
.shortcut-message-card {
  max-width: 23rem;
  max-inline-size: 100%;
}
.shortcut-message-card .v-card-title,
.shortcut-message-card .v-card-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.shortcut-message-entry.is-outgoing {
  align-items: flex-end;
}
.message-outgoing-title,
.message-outgoing-text {
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
}
.shortcut-message-scroll {
  height: 100%;
}
.shortcut-message-entry time {
  margin-top: 4px;
  font-size: 12px;
  font-style: italic;
  opacity: 0.7;
}
.shortcut-message-actions {
  padding: 16px;
}
</style>
