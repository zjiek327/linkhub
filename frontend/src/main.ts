import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import App from './App.vue'
import router from './router'
import { i18n } from './api/i18n'
import { settings } from './api/settings'
import './style.css'

// element-plus 组件库语言跟随应用语言
const elLocale = (settings.language === 'system'
  ? (navigator.language.startsWith('zh') ? zhCn : en)
  : (settings.language === 'zh' ? zhCn : en))

createApp(App).use(router).use(i18n).use(ElementPlus, { locale: elLocale }).mount('#app')
