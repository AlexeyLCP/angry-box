<script setup lang="ts">
import { ref } from 'vue'

const props = defineProps<{ text: string; label?: string }>()

const copied = ref(false)

const copy = async () => {
  try {
    await navigator.clipboard.writeText(props.text)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    // Fallback for non-HTTPS
    const ta = document.createElement('textarea')
    ta.value = props.text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  }
}
</script>

<template>
  <button class="copy-btn" :class="{ copied }" @click="copy">
    <i :class="copied ? 'pi pi-check' : 'pi pi-copy'" />
    <span>{{ copied ? 'Copied' : label ?? 'Copy' }}</span>
  </button>
</template>

<style scoped>
.copy-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 10px;
  border: 1px solid var(--p-surface-border);
  border-radius: 6px;
  background: var(--p-surface-card);
  color: var(--p-text-color);
  cursor: pointer;
  font-size: 12px;
  transition: all 0.15s;
}
.copy-btn:hover { border-color: var(--p-primary-color); }
.copy-btn.copied { border-color: var(--p-green-500); color: var(--p-green-500); }
</style>
