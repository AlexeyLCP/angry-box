<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useServersStore } from '@/stores/servers'
import { computed } from 'vue'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const servers = useServersStore()

interface NavItem {
  label?: string
  icon?: string
  to?: string
  active?: boolean
  badge?: string
  separator?: boolean
}

const items = computed<NavItem[]>(() => [
  {
    label: 'Dashboard',
    icon: 'pi pi-th-large',
    to: '/',
    active: route.path === '/' || route.path.startsWith('/servers'),
    badge: `${servers.onlineCount}/${servers.servers.length}`,
  },
  {
    label: 'Chains',
    icon: 'pi pi-link',
    to: '/chains',
    active: route.path.startsWith('/chains'),
  },
  {
    label: 'Logs',
    icon: 'pi pi-history',
    to: '/logs',
    active: route.path === '/logs',
  },
  { separator: true },
  {
    label: 'Settings',
    icon: 'pi pi-cog',
    to: '/settings',
    active: route.path === '/settings',
  },
])

const navigate = (to: string) => router.push(to)
</script>

<template>
  <nav class="sidebar">
    <div class="sidebar-header">
      <span class="logo">LucX</span>
      <span class="version">v0.1</span>
    </div>

    <div class="sidebar-nav">
      <template v-for="(item, i) in items" :key="i">
        <hr v-if="item.separator" class="sidebar-sep" />
        <button
          v-else
          :class="['nav-item', { active: item.active }]"
          @click="navigate(item.to!)"
        >
          <i :class="item.icon" />
          <span>{{ item.label }}</span>
          <span v-if="item.badge" class="badge">{{ item.badge }}</span>
        </button>
      </template>
    </div>

    <div class="sidebar-footer">
      <button class="nav-item" @click="auth.logout(); router.push('/login')">
        <i class="pi pi-sign-out" />
        <span>Logout</span>
      </button>
    </div>
  </nav>
</template>

<style scoped>
.sidebar {
  width: 240px;
  min-width: 240px;
  height: 100vh;
  background: var(--p-surface-card);
  border-right: 1px solid var(--p-surface-border);
  display: flex;
  flex-direction: column;
  user-select: none;
}

.sidebar-header {
  padding: 20px 20px 16px;
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.logo {
  font-size: 22px;
  font-weight: 800;
  color: var(--p-primary-color);
  letter-spacing: -0.5px;
}

.version {
  font-size: 11px;
  color: var(--p-text-muted-color);
}

.sidebar-nav {
  flex: 1;
  padding: 0 10px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.sidebar-sep {
  margin: 8px 10px;
  border: none;
  border-top: 1px solid var(--p-surface-border);
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 8px;
  border: none;
  background: transparent;
  color: var(--p-text-color);
  font-size: 14px;
  cursor: pointer;
  transition: background 0.15s;
  width: 100%;
  text-align: left;
}

.nav-item:hover {
  background: var(--p-surface-hover);
}

.nav-item.active {
  background: var(--p-primary-color);
  color: var(--p-primary-contrast-color);
}

.nav-item i {
  font-size: 18px;
  width: 22px;
  text-align: center;
}

.badge {
  margin-left: auto;
  font-size: 11px;
  padding: 1px 7px;
  border-radius: 10px;
  background: var(--p-surface-hover);
}

.nav-item.active .badge {
  background: rgba(255, 255, 255, 0.2);
}

.sidebar-footer {
  padding: 10px;
  border-top: 1px solid var(--p-surface-border);
}

@media (max-width: 768px) {
  .sidebar {
    width: 100%; min-width: 0; height: 56px; flex-shrink: 0;
    border-right: none; border-top: 1px solid var(--p-surface-border);
    position: fixed; bottom: 0; left: 0; right: 0; z-index: 100;
  }
  .sidebar-nav {
    flex-direction: row; justify-content: space-around;
    padding: 0; gap: 0; height: 100%;
  }
  .nav-item { font-size: 10px; padding: 6px 4px; flex-direction: column; gap: 2px; border-radius: 0; }
  .nav-item span { font-size: 10px; }
  .nav-item i { font-size: 20px; width: auto; }
  .nav-item.active { background: transparent; color: var(--p-primary-color); }
  .sidebar-header, .sidebar-footer, .sidebar-sep { display: none; }
  .badge { font-size: 9px; padding: 0 5px; }
}
</style>
