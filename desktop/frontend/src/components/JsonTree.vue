<script>
export default {
  name: 'JsonTree',
  props: {
    value: {
      type: null,
      required: true,
    },
    name: {
      type: [String, Number],
      default: '',
    },
    root: {
      type: Boolean,
      default: false,
    },
    level: {
      type: Number,
      default: 0,
    },
  },
  data() {
    return {
      collapsed: false,
    }
  },
  computed: {
    kind() {
      if (Array.isArray(this.value)) return 'array'
      if (this.value === null) return 'null'
      return typeof this.value
    },
    isBranch() {
      return this.kind === 'array' || this.kind === 'object'
    },
    entries() {
      if (this.kind === 'array') {
        return this.value.map((item, index) => [index, item])
      }
      if (this.kind === 'object') {
        return Object.entries(this.value)
      }
      return []
    },
    openToken() {
      return this.kind === 'array' ? '[' : '{'
    },
    closeToken() {
      return this.kind === 'array' ? ']' : '}'
    },
    summary() {
      const count = this.entries.length
      return `${count} ${this.kind === 'array' ? 'items' : 'keys'}`
    },
    scalarClass() {
      return `json-${this.kind}`
    },
    scalarText() {
      if (this.kind === 'string') return JSON.stringify(this.value)
      if (this.kind === 'null') return 'null'
      return String(this.value)
    },
  },
  methods: {
    toggle() {
      if (this.isBranch) {
        this.collapsed = !this.collapsed
      }
    },
  },
}
</script>

<template>
  <div class="json-node" :class="{ root }">
    <div class="json-line" :style="{ paddingLeft: root ? '0px' : `${level * 14}px` }">
      <button
        v-if="isBranch"
        class="json-toggle"
        type="button"
        :aria-label="collapsed ? '展开 JSON 节点' : '收起 JSON 节点'"
        @click.stop="toggle"
      >
        <i :class="collapsed ? 'bi bi-chevron-right' : 'bi bi-chevron-down'"></i>
      </button>
      <span v-else class="json-toggle-spacer"></span>

      <span v-if="!root" class="json-key">{{ JSON.stringify(String(name)) }}</span>
      <span v-if="!root" class="json-punctuation">: </span>

      <template v-if="isBranch">
        <span class="json-punctuation">{{ openToken }}</span>
        <button class="json-summary" type="button" @click.stop="toggle">{{ summary }}</button>
        <span v-if="collapsed" class="json-punctuation">{{ closeToken }}</span>
      </template>
      <span v-else class="json-scalar" :class="scalarClass">{{ scalarText }}</span>
    </div>

    <template v-if="isBranch && !collapsed">
      <JsonTree
        v-for="([childName, childValue], index) in entries"
        :key="`${String(childName)}-${index}`"
        :name="childName"
        :value="childValue"
        :level="level + 1"
      />
      <div class="json-line" :style="{ paddingLeft: root ? '0px' : `${level * 14}px` }">
        <span class="json-toggle-spacer"></span>
        <span class="json-punctuation">{{ closeToken }}</span>
      </div>
    </template>
  </div>
</template>
