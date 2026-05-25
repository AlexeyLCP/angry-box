<script setup lang="ts">
import AppSidebar from './AppSidebar.vue'
import Toast from 'primevue/toast'
</script>

<template>
  <Toast position="bottom-right" />
  <div class="app-layout">
    <AppSidebar />
    <main class="app-main">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.app-layout {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

.app-main {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
  background: var(--p-surface-ground);
}

@media (max-width: 768px) {
  .app-layout { flex-direction: column-reverse; }
  .app-main { padding: 12px; overflow-y: auto; }
}
</style>
