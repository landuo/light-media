<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { VCard, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VTextarea } from 'vuetify/components/VTextarea';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText, toast } from '../api';
const text = ref(''),
  error = ref(''),
  loaded = ref(true),
  busy = ref(false);
async function load() {
  busy.value = true;
  error.value = '';
  try {
    const response = await api('/settings/recognition-words');
    const value = response.value;
    // Like the source card, this editor is a line-list view. Never coerce a
    // plugin's non-list value into a replacement list and silently overwrite it.
    if (value && !Array.isArray(value))
      throw new Error(
        '当前识别词不是列表，无法在逐行编辑器中显示；原值未修改。可明确输入新词表或清空后保存修复。',
      );
    if (value) text.value = value.join('\n');
    loaded.value = true;
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function save() {
  if (!loaded.value || busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    await api('/settings/recognition-words', { text: text.value }, 'PUT');
    toast('自定义识别词保存成功');
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
onMounted(load);
</script>
<template>
  <VCard class="recognition-words-settings" variant="flat">
    <VCardTitle>自定义识别词</VCardTitle>
    <VCardSubtitle>预处理媒体标题；与插件共享同一全局词表。</VCardSubtitle>
    <VCardText>
      <VAlert v-if="error" type="error" variant="tonal" role="alert">{{ error }}</VAlert>
      <VTextarea
        v-if="loaded"
        v-model="text"
        aria-label="全局自定义识别词"
        placeholder="屏蔽词&#10;被替换词 => 替换词&#10;前定位词 &lt;&gt; 后定位词 &gt;&gt; 集数偏移量"
        hint="一行一条规则；支持正则表达式，# 开头的行是注释。"
        persistent-hint
        prepend-inner-icon="mdi-tag-text"
      />
    </VCardText>
    <VCardText>
      <VBtn
        :disabled="!loaded || busy"
        :loading="busy"
        prepend-icon="mdi-content-save"
        @click="save"
        >保存识别词</VBtn
      >
      <VBtn :disabled="busy" variant="text" @click="load">重新载入识别词</VBtn>
    </VCardText>
  </VCard>
</template>
<style scoped>
.recognition-words-settings {
  margin-top: 24px;
}
.recognition-words-settings :deep(.v-field textarea) {
  border: 0;
  background: transparent;
  box-shadow: none;
  color: inherit;
  font: inherit;
}
</style>
