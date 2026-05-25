import { createRouter, createWebHashHistory } from 'vue-router'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
    },
    {
      path: '/',
      component: () => import('@/components/layout/AppLayout.vue'),
      children: [
        {
          path: '',
          name: 'dashboard',
          component: () => import('@/views/DashboardView.vue'),
        },
        {
          path: 'chains',
          name: 'chains',
          component: () => import('@/views/ChainListView.vue'),
        },
        {
          path: 'chains/new',
          name: 'chain-builder',
          component: () => import('@/views/ChainBuilderView.vue'),
        },
        {
          path: 'chains/:id',
          name: 'chain-detail',
          component: () => import('@/views/ChainDetailView.vue'),
        },
        {
          path: 'servers',
          redirect: '/',
        },
        {
          path: 'servers/:id',
          name: 'server-detail',
          component: () => import('@/views/ServerDetailView.vue'),
        },
        {
          path: 'settings',
          name: 'settings',
          component: () => import('@/views/SettingsView.vue'),
        },
        {
          path: 'logs',
          name: 'logs',
          component: () => import('@/views/LogsView.vue'),
        },
      ],
    },
  ],
})

router.beforeEach((to, _from) => {
  const token = localStorage.getItem('lucx_token')
  if (!token && to.path !== '/login') {
    return { path: '/login' }
  }
  if (token && to.path === '/login') {
    return { path: '/' }
  }
  return true
})

export default router
