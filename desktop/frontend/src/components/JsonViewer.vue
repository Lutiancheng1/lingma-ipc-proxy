<script setup>
import { computed } from 'vue'
import JsonTree from './JsonTree.vue'

const props = defineProps({
  body: {
    type: String,
    default: '',
  },
  emptyText: {
    type: String,
    default: '空内容',
  },
})

const parsed = computed(() => {
  const raw = String(props.body || '').trim()
  if (!raw) {
    return { valid: false, empty: true, text: props.emptyText }
  }
  try {
    return { valid: true, value: JSON.parse(raw) }
  } catch (e) {
    return { valid: false, empty: false, text: props.body }
  }
})
</script>

<template>
  <div class="json-viewer hidden-scrollbar" :class="{ empty: parsed.empty }">
    <JsonTree v-if="parsed.valid" :value="parsed.value" root />
    <pre v-else>{{ parsed.text }}</pre>
  </div>
</template>
