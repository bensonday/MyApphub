import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { loadProjectBrand } from './composables/useProjectBrand'
import './styles/tokens.css'
import './styles/global.css'

createApp(App).use(router).mount('#app')
void loadProjectBrand()
