import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import CampaignView from './views/CampaignView.vue'
import ComposeView from './views/ComposeView.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: ComposeView },
    { path: '/campaigns/:id', component: CampaignView, props: true },
  ],
})

createApp(App).use(router).mount('#app')
