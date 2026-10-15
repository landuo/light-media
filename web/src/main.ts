import { createApp } from 'vue';
import App from './App.vue';
import './style.css';
import { reportFrontendError } from './frontend-errors';
const app = createApp(App);
app.config.errorHandler = () => reportFrontendError('VUE_RENDER_ERROR');
window.addEventListener('unhandledrejection', () => reportFrontendError('UNHANDLED_REJECTION'));
window.addEventListener('error', () => reportFrontendError('SCRIPT_ERROR'));
app.mount('#app');
