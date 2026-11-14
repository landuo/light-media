<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { VTabs, VTab } from 'vuetify/components/VTabs';
import { VTextField } from 'vuetify/components/VTextField';
import { VCombobox } from 'vuetify/components/VCombobox';
import { VIcon } from 'vuetify/components/VIcon';
import { VSwitch } from 'vuetify/components/VSwitch';
import { VBtn } from 'vuetify/components/VBtn';
import { api, errorText, toast } from '../api';

const emit = defineEmits<{ saved: [] }>();
const tab = ref('basic'),
  localPaths = ref(''),
  autoReload = ref(false);
const serverURL = ref(''),
  statisticShare = ref(true);
const loading = ref(true),
  saving = ref(false),
  loaded = ref(false),
  error = ref('');
const parsedLocalPaths = computed(() => localPaths.value
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean));
const githubProxy = ref<string | null>(''), savedGitHubProxy = ref('');
const githubToken = ref(''), repoGitHubToken = ref('');
const githubTokenSet = ref(false), repoGitHubTokenSet = ref(false);
const clearGitHubToken = ref(false), clearRepoGitHubToken = ref(false);
function removeGitHubToken() { githubToken.value = ''; clearGitHubToken.value = true; }
function removeRepoGitHubToken() { repoGitHubToken.value = ''; clearRepoGitHubToken.value = true; }
function inputGitHubToken(value: unknown) { if (typeof value === 'string' && value.trim()) clearGitHubToken.value = false; }
function inputRepoGitHubToken(value: unknown) { if (typeof value === 'string' && value.trim()) clearRepoGitHubToken.value = false; }
const saveDisabled = computed(() => !loaded.value || loading.value || saving.value);
async function load() {
  loading.value = true;
  error.value = '';
  try {
    const result = await api('/plugins/markets');
    if (
      !Array.isArray(result?.plugin_local_repo_paths) ||
      !result.plugin_local_repo_paths.every((value: unknown) => typeof value === 'string') ||
      typeof result?.plugin_auto_reload !== 'boolean'
    )
      throw new Error('插件系统设置响应无效');
    localPaths.value = result.plugin_local_repo_paths.join(',');
    autoReload.value = result.plugin_auto_reload;
    serverURL.value = typeof result.plugin_server_url === 'string' ? result.plugin_server_url : '';
    statisticShare.value =
      typeof result.plugin_statistic_share === 'boolean' ? result.plugin_statistic_share : true;
    githubProxy.value = typeof result.github_proxy === 'string' ? result.github_proxy : '';
    savedGitHubProxy.value = githubProxy.value || '';
    githubTokenSet.value = result.github_token_set === true;
    repoGitHubTokenSet.value = result.repo_github_token_set === true;
    githubToken.value = typeof result.github_token === 'string' ? result.github_token : '';
    repoGitHubToken.value = typeof result.repo_github_token === 'string' ? result.repo_github_token : '';
    clearGitHubToken.value = clearRepoGitHubToken.value = false;
    loaded.value = true;
  } catch (value) {
    error.value = errorText(value);
  } finally {
    loading.value = false;
  }
}
async function save() {
  if (saveDisabled.value) return;
  saving.value = true;
  error.value = '';
  try {
    // Source AccountSettingSystem saves plugin dev/advanced fields separately
    // from the market editor. Omit repositories so a newer source list survives.
    const result = await api(
      '/plugins/markets',
      {
        plugin_local_repo_paths: parsedLocalPaths.value,
        plugin_auto_reload: autoReload.value,
        plugin_server_url: serverURL.value.trim(),
        plugin_statistic_share: statisticShare.value,
        ...((githubProxy.value || '').trim() !== savedGitHubProxy.value ? { github_proxy: (githubProxy.value || '').trim() } : {}),
        ...(clearGitHubToken.value ? { clear_github_token: true } : githubToken.value?.trim() ? { github_token: githubToken.value } : {}),
        ...(clearRepoGitHubToken.value ? { clear_repo_github_token: true } : repoGitHubToken.value?.trim() ? { repo_github_token: repoGitHubToken.value } : {}),
      },
      'PUT',
    );
    if (result?.success !== true) throw new Error(result?.message || '插件系统设置保存失败');
    savedGitHubProxy.value = (githubProxy.value || '').trim();
    if (clearGitHubToken.value) githubTokenSet.value = false;
    else if (githubToken.value?.trim()) githubTokenSet.value = true;
    if (clearRepoGitHubToken.value) repoGitHubTokenSet.value = false;
    else if (repoGitHubToken.value?.trim()) repoGitHubTokenSet.value = true;
    clearGitHubToken.value = clearRepoGitHubToken.value = false;
    toast('插件系统设置已保存');
    emit('saved');
  } catch (value) {
    error.value = errorText(value);
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>
<template>
  <section class="panel">
    <header class="section-heading"><h2>插件</h2></header>
    <p v-if="loading" role="status">正在加载插件设置…</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <VTabs v-model="tab"><VTab value="basic">基础</VTab><VTab value="advanced">高级</VTab><VTab value="dev">开发</VTab></VTabs>
    <div v-show="tab === 'basic'" class="form-grid mt-4">
      <VTextField v-model="githubToken" label="Github Token" type="text" prepend-inner-icon="mdi-github" :disabled="loading || saving" :placeholder="githubTokenSet && !clearGitHubToken ? '已保存；留空保留' : 'ghp_**** 或 github_pat_****'" persistent-placeholder hint="用于提高访问Github API时的限流阈值" persistent-hint :clearable="!!githubToken" @click:clear="removeGitHubToken" @update:model-value="inputGitHubToken">
        <template v-if="githubTokenSet && !clearGitHubToken && !githubToken" #append-inner><VBtn icon size="x-small" variant="text" aria-label="清除已保存的Github Token" @click.stop="removeGitHubToken"><VIcon icon="mdi-close-circle" /></VBtn></template>
      </VTextField>
    </div>
    <div v-show="tab === 'advanced'" class="form-grid mt-4">
      <VCombobox v-model="githubProxy" label="Github加速代理" :items="[]" clearable prepend-inner-icon="mdi-github" :disabled="loading || saving" placeholder="留空表示不使用代理" hint="使用代理加速Github访问速度" persistent-hint />
      <VSwitch
        v-model="statisticShare"
        label="上报插件安装数据"
        :disabled="loading || saving"
        hint="关闭后不发送也不读取插件安装统计"
        persistent-hint
      />
      <VTextField
        v-model="serverURL"
        label="插件统计与评分服务地址"
        :disabled="loading || saving"
        hint="留空时不读取统计或提交评分"
        persistent-hint
      />
    </div>
    <div v-show="tab === 'dev'" class="form-grid mt-4">
      <VTextField v-model="repoGitHubToken" label="指定仓库Github Token" type="text" prepend-inner-icon="mdi-github" :disabled="loading || saving" :placeholder="repoGitHubTokenSet && !clearRepoGitHubToken ? '已保存；留空保留' : 'owner/repo:ghp_****'" persistent-placeholder hint="多个仓库用逗号分隔" persistent-hint :clearable="!!repoGitHubToken" @click:clear="removeRepoGitHubToken" @update:model-value="inputRepoGitHubToken">
        <template v-if="repoGitHubTokenSet && !clearRepoGitHubToken && !repoGitHubToken" #append-inner><VBtn icon size="x-small" variant="text" aria-label="清除已保存的指定仓库Github Token" @click.stop="removeRepoGitHubToken"><VIcon icon="mdi-close-circle" /></VBtn></template>
      </VTextField>
      <VTextField
        v-model="localPaths"
        label="本地插件仓库路径"
        :disabled="loading || saving"
        hint="多个服务器目录用英文逗号分隔，目录包含 go-plugins.v1.json 和 Go 制品"
        persistent-hint
      />
      <VSwitch
        v-model="autoReload"
        label="插件热加载"
        :disabled="loading || saving"
        hint="已安装插件的 Go 制品更新后自动同步并重载"
        persistent-hint
      />
    </div>
    <div class="button-row mt-4">
      <VBtn v-if="error && !loaded" variant="text" :disabled="loading" @click="load"
        >重新读取设置</VBtn
      >
      <VBtn color="primary" variant="flat" :disabled="saveDisabled" :loading="saving" @click="save"
        >保存</VBtn
      >
    </div>
  </section>
</template>
