import { createRouter, createWebHistory } from 'vue-router'
import AppInstallView from './views/AppInstallView.vue'
import NotFoundView from './views/NotFoundView.vue'
import UdidView from './views/UdidView.vue'
import AdminView from './views/AdminView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/udid' },
    { path: '/app/:slug', name: 'app', component: AppInstallView },
    { path: '/udid', name: 'udid', component: UdidView },
    { path: '/admin', name: 'admin', component: AdminView },
    { path: '/404', name: 'not-found', component: NotFoundView },
    { path: '/:pathMatch(.*)*', redirect: '/404' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})
